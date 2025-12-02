package awssdk

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// BucketClient is a bucket-bound S3 client that provides operations
// for a specific bucket without requiring the bucket name in each method call.
type BucketClient struct {
	client   *s3.Client
	uploader *manager.Uploader
	bucket   string
}

// BucketConfig holds configuration for creating a BucketClient.
type BucketConfig struct {
	// S3 bucket name (required)
	Bucket string

	// S3 endpoint configuration (use either AWS region OR custom endpoint, not both)
	Region string

	// Custom endpoint options (for custom endpoints like LocalStack)
	Endpoint string // use this OR Region, not both
	UseSSL   bool   // defaults to true for AWS, configurable for custom endpoints
}

// NewBucketClient creates a new bucket-bound S3 client.
func NewBucketClient(
	ctx context.Context,
	bucketCfg BucketConfig,
	creds aws.CredentialsProvider,
) (*BucketClient, error) {
	// Validate basic requirements
	if bucketCfg.Bucket == "" {
		return nil, errors.New("bucket name is required")
	}

	if creds == nil {
		return nil, errors.New("credentials are required")
	}

	// Validate configuration: cannot specify both Region and Endpoint
	if bucketCfg.Region != "" && bucketCfg.Endpoint != "" {
		return nil, errors.New("cannot specify both Region and Endpoint - " +
			"use Region for AWS S3 or Endpoint for custom S3-compatible services")
	}

	var (
		region   string
		useSSL   bool
		endpoint string
	)

	//nolint:nestif
	if bucketCfg.Endpoint != "" {
		// Custom endpoint mode (LocalStack, MinIO, etc.)
		endpoint = bucketCfg.Endpoint
		useSSL = bucketCfg.UseSSL

		region = bucketCfg.Region
		if region == "" {
			region = "us-east-1" // Default region for custom endpoints
		}
	} else {
		// AWS S3 mode - determine region
		useSSL = true
		region = bucketCfg.Region

		// If region is not specified, try to detect it automatically
		if region == "" {
			detectedRegion, err := DetectBucketRegion(ctx, bucketCfg.Bucket, creds)
			if err != nil {
				return nil, fmt.Errorf("failed to detect bucket region for %s: %w", bucketCfg.Bucket, err)
			}

			region = detectedRegion
		}
	}

	// Create custom HTTP client with high connection limits for concurrency
	httpClient := &http.Client{
		Transport: createHTTPTransport(useSSL),
	}

	// Load AWS config
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(creds),
		config.WithRegion(region),
		config.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Build S3 client options
	var s3Opts []func(*s3.Options)

	if endpoint != "" {
		// Custom endpoint - use path style and set base endpoint
		scheme := "http"
		if useSSL {
			scheme = "https"
		}

		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(fmt.Sprintf("%s://%s", scheme, endpoint))
			o.UsePathStyle = true // Required for LocalStack/MinIO
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)

	// Create the uploader for streaming uploads
	uploader := manager.NewUploader(client)

	return &BucketClient{
		client:   client,
		uploader: uploader,
		bucket:   bucketCfg.Bucket,
	}, nil
}

// createHTTPTransport creates an HTTP transport optimized for S3 operations.
func createHTTPTransport(useSSL bool) *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   2048, // High limit for concurrent narinfo downloads
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if useSSL {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	return transport
}

// GetObject retrieves an object from the bucket.
func (bc *BucketClient) GetObject(
	ctx context.Context,
	key string,
) (*s3.GetObjectOutput, error) {
	output, err := bc.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bc.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get object %s: %w", key, err)
	}

	return output, nil
}

// PutObject uploads an object to the bucket.
// Uses the S3 Upload Manager to handle streaming uploads (unseekable readers).
func (bc *BucketClient) PutObject(
	ctx context.Context,
	key string,
	reader io.Reader,
	objectSize int64,
	contentType string,
	contentEncoding string,
) (*manager.UploadOutput, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(bc.bucket),
		Key:    aws.String(key),
		Body:   reader,
	}

	if objectSize >= 0 {
		input.ContentLength = aws.Int64(objectSize)
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	if contentEncoding != "" {
		input.ContentEncoding = aws.String(contentEncoding)
	}

	output, err := bc.uploader.Upload(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to upload object %s: %w", key, err)
	}

	return output, nil
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
) (map[string]error, error) {
	if len(keys) == 0 {
		return nil, nil //nolint:nilnil // intentional: no keys means no errors
	}

	// AWS limit is 1000 objects per DeleteObjects call
	const maxBatchSize = 1000

	failures := make(map[string]error)

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
			// All deletions in this batch failed
			for _, key := range batch {
				failures[key] = err
			}

			continue
		}

		// Record individual failures
		for _, e := range output.Errors {
			failures[aws.ToString(e.Key)] = fmt.Errorf("%s: %s", aws.ToString(e.Code), aws.ToString(e.Message))
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
	// Use us-east-1 as the default region for the lookup
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(creds),
		config.WithRegion("us-east-1"),
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
		return "us-east-1", nil
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

// NewStaticCredentials is a convenience function for creating static credentials.
//
//nolint:ireturn // AWS SDK uses CredentialsProvider interface
func NewStaticCredentials(accessKeyID, secretAccessKey, sessionToken string) aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, sessionToken)
}
