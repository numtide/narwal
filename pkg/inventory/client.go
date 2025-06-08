package inventory

import (
	"strings"

	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/workarea"
)

// BucketConfig returns the workarea bucket configuration for inventory buckets.
// Inventory buckets use no partitioning and preserve the original key structure.
func BucketConfig() workarea.BucketConfig {
	return workarea.BucketConfig{
		UsePartitioning:      false,
		PreserveKeyStructure: true,
	}
}

// Client provides functionality to interact with S3 inventory data.
type Client struct {
	bucketClient *awssdk.BucketClient
	prefix       string
}

// NewClient creates a new inventory client.
func NewClient(bucketClient *awssdk.BucketClient, prefix string) *Client {
	// Ensure prefix ends with a slash if not empty
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return &Client{
		bucketClient: bucketClient,
		prefix:       prefix,
	}
}
