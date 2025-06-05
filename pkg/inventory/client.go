package inventory

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client defines the interface for S3 operations needed by the inventory client.
type S3Client interface {
	ListObjectsV2(
		ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options),
	) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// Client provides functionality to interact with S3 inventory data.
type Client struct {
	s3Client S3Client
	bucket   string
	prefix   string
}

// NewClient creates a new inventory client.
func NewClient(s3Client S3Client, bucket, prefix string) *Client {
	// Ensure prefix ends with a slash if not empty
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return &Client{
		s3Client: s3Client,
		bucket:   bucket,
		prefix:   prefix,
	}
}
