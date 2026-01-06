package awssdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/numtide/narwal/pkg/config"
)

// BucketClient is a bucket-bound S3 client that provides operations
// for a specific bucket without requiring the bucket name in each method call.
type BucketClient struct {
	bucket string
	client *s3.Client
}

// NewBucketClientFromSDK creates a BucketClient from an existing S3 client.
// Useful for testing where the client is constructed with custom options.
func NewBucketClientFromSDK(client *s3.Client, bucket string) *BucketClient {
	return &BucketClient{client: client, bucket: bucket}
}

// NewBucketClientFromConfig creates a new bucket-bound S3 client from config objects.
func NewBucketClientFromConfig(ctx context.Context, awsCfg *config.AWS, s3Cfg *config.S3) (*BucketClient, error) {
	// Load AWS SDK config
	sdkCfg, err := LoadSDKConfig(ctx, awsCfg)
	if err != nil {
		return nil, err
	}

	// Build S3 client options
	var s3Opts []func(*s3.Options)

	// Use path-style URLs for localstack/minio if a custom endpoint is configured
	if awsCfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	// Create the S3 client
	client := s3.NewFromConfig(*sdkCfg, s3Opts...)

	return &BucketClient{
		client: client,
		bucket: s3Cfg.Bucket,
	}, nil
}

// StatObject gets metadata of an object in the bucket (HeadObject).
func (bc *BucketClient) StatObject(
	ctx context.Context,
	key string,
) (*s3.HeadObjectOutput, error) {
	output, err := bc.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bc.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to stat object %s: %w", key, err)
	}

	return output, nil
}

// RemoveObject removes an object from the bucket.
func (bc *BucketClient) RemoveObject(
	ctx context.Context,
	key string,
) error {
	_, err := bc.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bc.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete object %s: %w", key, err)
	}

	return nil
}

// RemoveObjects removes multiple objects from the bucket in a single call.
// Returns a map of failed keys to their errors, or nil if all succeeded.
func (bc *BucketClient) RemoveObjects(
	ctx context.Context,
	keys []string,
) (map[string]types.Error, error) {
	if len(keys) == 0 {
		return nil, nil //nolint:nilnil // intentional: no keys means no errors
	}

	// AWS limit is 1000 objects per DeleteObjects call
	const maxBatchSize = 1000

	failures := make(map[string]types.Error)

	for i := 0; i < len(keys); i += maxBatchSize {
		end := min(i+maxBatchSize, len(keys))

		batch := keys[i:end]

		objects := make([]types.ObjectIdentifier, len(batch))

		for j, key := range batch {
			objects[j] = types.ObjectIdentifier{Key: aws.String(key)}
		}

		output, err := bc.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bc.bucket),
			Delete: &types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(false),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to delete objects: %w", err)
		}

		// Record individual failures
		for _, s3Err := range output.Errors {
			failures[aws.ToString(s3Err.Key)] = s3Err
		}
	}

	if len(failures) > 0 {
		return failures, nil
	}

	return nil, nil //nolint:nilnil // intentional: all deletions succeeded
}

// ListObjectsOutput represents an object returned from listing.
type ListObjectsOutput struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	Err          error
}

// ListObjects lists objects in the bucket with the given prefix.
// Returns a channel that yields objects one at a time.
func (bc *BucketClient) ListObjects(
	ctx context.Context,
	prefix string,
	recursive bool,
) <-chan ListObjectsOutput {
	ch := make(chan ListObjectsOutput)

	go func() {
		defer close(ch)

		input := &s3.ListObjectsV2Input{
			Bucket: aws.String(bc.bucket),
		}

		if prefix != "" {
			input.Prefix = aws.String(prefix)
		}

		if !recursive {
			input.Delimiter = aws.String("/")
		}

		paginator := s3.NewListObjectsV2Paginator(bc.client, input)

		for paginator.HasMorePages() {
			output, err := paginator.NextPage(ctx)
			if err != nil {
				ch <- ListObjectsOutput{Err: err}
				return
			}

			for _, obj := range output.Contents {
				ch <- ListObjectsOutput{
					Key:          aws.ToString(obj.Key),
					Size:         aws.ToInt64(obj.Size),
					LastModified: aws.ToTime(obj.LastModified),
					ETag:         aws.ToString(obj.ETag),
				}
			}
		}
	}()

	return ch
}

// BucketName returns the name of the bucket this client is bound to.
func (bc *BucketClient) BucketName() string {
	return bc.bucket
}

// UnderlyingClient returns the underlying s3.Client for advanced operations.
func (bc *BucketClient) UnderlyingClient() *s3.Client {
	return bc.client
}

// DetectBucketRegion detects the AWS region of a bucket.
// This function can be called before creating an S3 client to automatically
// determine the appropriate region.
func DetectBucketRegion(ctx context.Context, bucketName string, creds aws.CredentialsProvider) (string, error) {
	if creds == nil {
		return "", errors.New("credentials are required for bucket region detection")
	}

	// Create a temporary S3 client to detect bucket region
	// Use defaultRegion for the lookup
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithCredentialsProvider(creds),
		awsconfig.WithRegion(defaultRegion),
	)
	if err != nil {
		return "", fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)

	output, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get bucket location for %s: %w", bucketName, err)
	}

	// AWS returns empty/nil LocationConstraint for us-east-1
	region := string(output.LocationConstraint)
	if region == "" {
		return defaultRegion, nil
	}

	// Handle legacy "EU" region constraint which maps to eu-west-1
	if region == "EU" {
		return "eu-west-1", nil
	}

	return region, nil
}

// CreateBucket creates a new S3 bucket (primarily for testing).
func (bc *BucketClient) CreateBucket(ctx context.Context) error {
	_, err := bc.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bc.bucket),
	})
	if err != nil {
		return fmt.Errorf("failed to create bucket %s: %w", bc.bucket, err)
	}

	return nil
}

// PutObject uploads an object to the bucket.
func (bc *BucketClient) PutObject(
	ctx context.Context,
	key string,
	body io.Reader,
	contentLength int64,
	contentType string,
) error {
	_, err := bc.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bc.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(contentLength),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("failed to put object %s: %w", key, err)
	}

	return nil
}
