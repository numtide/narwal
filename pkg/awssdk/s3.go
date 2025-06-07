package awssdk

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	defaultS3Endpoint = "s3.amazonaws.com"
)

// BucketClient is a bucket-bound S3 client that provides operations
// for a specific bucket without requiring the bucket name in each method call.
type BucketClient struct {
	client *minio.Client
	bucket string
}

// BucketConfig holds configuration for creating a BucketClient.
type BucketConfig struct {
	// S3 bucket name (required)
	Bucket string

	// S3 endpoint configuration (use either AWS region OR custom endpoint, not both)
	Region string

	// MinIO-specific options (for custom endpoints)
	Endpoint string // use this OR Region, not both
	UseSSL   bool   // defaults to true for AWS, configurable for custom endpoints
}

// NewBucketClient creates a new bucket-bound S3 client.
func NewBucketClient(
	ctx context.Context,
	config BucketConfig,
	creds *credentials.Credentials,
) (*BucketClient, error) {
	// Validate basic requirements
	if config.Bucket == "" {
		return nil, errors.New("bucket name is required")
	}

	if creds == nil {
		return nil, errors.New("credentials are required")
	}

	// Validate configuration: cannot specify both Region and Endpoint
	if config.Region != "" && config.Endpoint != "" {
		return nil, errors.New("cannot specify both Region and Endpoint - " +
			"use Region for AWS S3 or Endpoint for custom S3-compatible services")
	}

	var (
		endpoint string
		useSSL   bool
		region   string
	)

	//nolint:nestif
	if config.Endpoint != "" {
		// Custom endpoint mode (MinIO, etc.)
		endpoint = config.Endpoint
		useSSL = config.UseSSL
		region = config.Region // can be empty for custom endpoints
	} else {
		// AWS S3 mode - determine region and construct endpoint
		useSSL = true
		region = config.Region

		// If region is not specified, try to detect it automatically
		if region == "" {
			detectedRegion, err := DetectBucketRegion(ctx, config.Bucket, creds)
			if err != nil {
				return nil, fmt.Errorf("failed to detect bucket region for %s: %w", config.Bucket, err)
			}

			region = detectedRegion
		}

		// Construct AWS S3 endpoint
		endpoint = defaultS3Endpoint
		if region != "us-east-1" {
			endpoint = fmt.Sprintf("s3.%s.amazonaws.com", region)
		}
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	return &BucketClient{
		client: client,
		bucket: config.Bucket,
	}, nil
}

// GetObject retrieves an object from the bucket.
func (bc *BucketClient) GetObject(
	ctx context.Context,
	key string,
	opts minio.GetObjectOptions,
) (*minio.Object, error) {
	return bc.client.GetObject(ctx, bc.bucket, key, opts) //nolint:wrapcheck
}

// PutObject uploads an object to the bucket.
func (bc *BucketClient) PutObject(
	ctx context.Context,
	key string,
	reader io.Reader,
	objectSize int64,
	opts minio.PutObjectOptions,
) (minio.UploadInfo, error) {
	return bc.client.PutObject(ctx, bc.bucket, key, reader, objectSize, opts) //nolint:wrapcheck
}

// StatObject gets metadata of an object in the bucket.
func (bc *BucketClient) StatObject(
	ctx context.Context,
	key string,
	opts minio.StatObjectOptions,
) (minio.ObjectInfo, error) {
	return bc.client.StatObject(ctx, bc.bucket, key, opts) //nolint:wrapcheck
}

// RemoveObject removes an object from the bucket.
func (bc *BucketClient) RemoveObject(
	ctx context.Context,
	key string,
	opts minio.RemoveObjectOptions,
) error {
	return bc.client.RemoveObject(ctx, bc.bucket, key, opts) //nolint:wrapcheck
}

// ListObjects lists objects in the bucket.
func (bc *BucketClient) ListObjects(
	ctx context.Context,
	opts minio.ListObjectsOptions,
) <-chan minio.ObjectInfo {
	return bc.client.ListObjects(ctx, bc.bucket, opts)
}

// BucketName returns the name of the bucket this client is bound to.
func (bc *BucketClient) BucketName() string {
	return bc.bucket
}

// UnderlyingClient returns the underlying minio.Client for advanced operations.
func (bc *BucketClient) UnderlyingClient() *minio.Client {
	return bc.client
}

// DetectBucketRegion detects the AWS region of a bucket using MinIO client.
// This function can be called before creating an S3 client to automatically
// determine the appropriate region.
func DetectBucketRegion(ctx context.Context, bucketName string, creds *credentials.Credentials) (string, error) {
	if creds == nil {
		return "", errors.New("credentials are required for bucket region detection")
	}

	// Create a temporary MinIO client to detect bucket region
	// Use empty region to avoid biasing the result
	tempClient, err := minio.New(defaultS3Endpoint, &minio.Options{
		Creds:  creds,
		Secure: true,
		Region: "", // Use empty region to get unbiased result
	})
	if err != nil {
		return "", fmt.Errorf("failed to create temporary S3 client: %w", err)
	}

	region, err := tempClient.GetBucketLocation(ctx, bucketName)
	if err != nil {
		return "", fmt.Errorf("failed to get bucket location for %s: %w", bucketName, err)
	}

	// AWS returns empty string for us-east-1
	if region == "" {
		return "us-east-1", nil
	}

	// Handle legacy "EU" region constraint which maps to eu-west-1
	if region == "EU" {
		return "eu-west-1", nil
	}

	return region, nil
}
