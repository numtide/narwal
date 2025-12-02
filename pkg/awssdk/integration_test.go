// Package awssdk_test contains integration tests for the awssdk package.
// These tests verify real-world functionality against actual S3 services.
//
// Integration tests included:
// - TestIntegration_NixCacheBucket: Tests against s3://nix-cache bucket
// - TestIntegration_NixReleasesBucket: Tests against s3://nix-releases bucket
// - TestIntegration_AutoDetectRegion: Tests automatic region detection
// - TestIntegration_GetObjectMetadata: Tests object metadata retrieval
// - TestIntegration_UnderlyingClientAccess: Tests underlying S3 client access
//
// Prerequisites:
// - AWS CLI credentials configured (tests will skip if not available)
//
// Run with: go test ./pkg/awssdk/... -run TestIntegration -v
package awssdk_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
)

// skipIfNoAWSCredentials checks if AWS credentials are available and skips
// the test if not.
func skipIfNoAWSCredentials(t *testing.T) {
	t.Helper()

	// Try to create credentials using AWS CLI default
	_, err := awssdk.NewCredentials(t.Context(), config.CredentialsConfig{})
	if err != nil {
		t.Skipf("Skipping integration test: AWS credentials not available (%v)", err)
	}
}

func TestIntegration_NixCacheBucket(t *testing.T) {
	t.Parallel()
	skipIfNoAWSCredentials(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Get AWS credentials to detect region first
	creds, err := awssdk.NewCredentials(ctx, config.CredentialsConfig{})
	if err != nil {
		t.Fatalf("Failed to get AWS credentials: %v", err)
	}

	// Test bucket region detection
	region, err := awssdk.DetectBucketRegion(ctx, "nix-cache", creds)
	if err != nil {
		t.Fatalf("Failed to detect region for nix-cache bucket: %v", err)
	}

	t.Logf("Detected region for nix-cache: %s", region)

	// Create S3 client for nix-cache bucket
	client, err := awssdk.NewS3Client(ctx, &config.AWS{
		Region: region,
	}, &config.S3{
		Bucket: "nix-cache",
	})
	if err != nil {
		t.Fatalf("Failed to create S3 client for nix-cache: %v", err)
	}

	// Verify bucket name
	if client.BucketName() != "nix-cache" {
		t.Errorf("Expected bucket name 'nix-cache', got '%s'", client.BucketName())
	}

	// List some objects to verify connectivity
	objectCh := client.ListObjects(ctx, "", false)
	objectCount := 0

	for object := range objectCh {
		if object.Err != nil {
			t.Fatalf("Error listing objects: %v", object.Err)
		}

		objectCount++

		t.Logf("Found object: %s (size: %d)", object.Key, object.Size)

		if objectCount >= 3 {
			break // We just want to verify we can list objects
		}
	}

	if objectCount == 0 {
		t.Error("Expected to find at least some objects in nix-cache bucket")
	}
}

func TestIntegration_NixReleasesBucket(t *testing.T) {
	t.Parallel()
	skipIfNoAWSCredentials(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Get AWS credentials to detect region first
	creds, err := awssdk.NewCredentials(ctx, config.CredentialsConfig{})
	if err != nil {
		t.Fatalf("Failed to get AWS credentials: %v", err)
	}

	// Test bucket region detection
	region, err := awssdk.DetectBucketRegion(ctx, "nix-releases", creds)
	if err != nil {
		t.Skipf("Cannot detect region for nix-releases bucket (access denied?): %v", err)
	}

	t.Logf("Detected region for nix-releases: %s", region)

	// Create S3 client for nix-releases bucket
	client, err := awssdk.NewS3Client(ctx, &config.AWS{
		Region: region,
	}, &config.S3{
		Bucket: "nix-releases",
	})
	if err != nil {
		t.Fatalf("Failed to create S3 client for nix-releases: %v", err)
	}

	// Verify bucket name
	if client.BucketName() != "nix-releases" {
		t.Errorf("Expected bucket name 'nix-releases', got '%s'", client.BucketName())
	}

	// List some objects to verify connectivity
	objectCh := client.ListObjects(ctx, "", false)
	objectCount := 0

	var listError error

	for object := range objectCh {
		if object.Err != nil {
			listError = object.Err
			break
		}

		objectCount++

		t.Logf("Found object: %s (size: %d)", object.Key, object.Size)

		if objectCount >= 3 {
			break // We just want to verify we can list objects
		}
	}

	if listError != nil {
		t.Skipf("Cannot list objects in nix-releases bucket (access denied?): %v", listError)
	}

	if objectCount == 0 {
		t.Error("Expected to find at least some objects in nix-releases bucket")
	}
}

func TestIntegration_AutoDetectRegion(t *testing.T) {
	t.Parallel()
	skipIfNoAWSCredentials(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	tests := []struct {
		name   string
		bucket string
	}{
		{"nix-cache auto-detect", "nix-cache"},
		{"nix-releases auto-detect", "nix-releases"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create S3 client without specifying region (should auto-detect)
			client, err := awssdk.NewS3Client(ctx, &config.AWS{
				// No Region specified - should auto-detect
			}, &config.S3{
				Bucket: tt.bucket,
			})
			if err != nil {
				t.Skipf("Failed to create S3 client with auto-detect for %s (access denied?): %v", tt.bucket, err)
			}

			// Verify bucket name
			if client.BucketName() != tt.bucket {
				t.Errorf("Expected bucket name '%s', got '%s'", tt.bucket, client.BucketName())
			}

			// Try a simple operation to verify the client works
			objectCh := client.ListObjects(ctx, "", true)
			objectFound := false

			//nolint:staticcheck
			for object := range objectCh {
				if object.Err != nil {
					t.Skipf("Cannot list objects in %s bucket (access denied?): %v", tt.bucket, object.Err)
				}

				t.Logf("Successfully connected to %s bucket (found object: %s)", tt.bucket, object.Key)

				objectFound = true

				break // We just need to verify we can connect
			}

			if !objectFound {
				t.Skip("No objects found to verify connectivity")
			}
		})
	}
}

func TestIntegration_GetObjectMetadata(t *testing.T) {
	t.Parallel()
	skipIfNoAWSCredentials(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Create S3 client for nix-cache bucket (more likely to be accessible)
	client, err := awssdk.NewS3Client(ctx, &config.AWS{
		// No Region specified - should auto-detect
	}, &config.S3{
		Bucket: "nix-cache",
	})
	if err != nil {
		t.Fatalf("Failed to create S3 client: %v", err)
	}

	// First, find an actual object key by listing (skip directories)
	var testKey string

	objectCh := client.ListObjects(ctx, "", true)
	for object := range objectCh {
		if object.Err != nil {
			t.Skipf("Cannot list objects (access denied?): %v", object.Err)
		}
		// Skip directories (keys ending with /)
		if !strings.HasSuffix(object.Key, "/") && object.Size > 0 {
			testKey = object.Key
			break
		}
	}

	if testKey == "" {
		t.Skip("No objects found to test StatObject with")
	}

	// Test StatObject
	objectInfo, err := client.StatObject(ctx, testKey)
	if err != nil {
		t.Fatalf("Failed to stat object %s: %v", testKey, err)
	}

	t.Logf("Object info for %s:", testKey)
	t.Logf("  Size: %d bytes", aws.ToInt64(objectInfo.ContentLength))
	t.Logf("  Last Modified: %s", aws.ToTime(objectInfo.LastModified))
	t.Logf("  Content Type: %s", aws.ToString(objectInfo.ContentType))

	if aws.ToInt64(objectInfo.ContentLength) <= 0 {
		t.Error("Expected object size to be greater than 0")
	}
}

func TestIntegration_UnderlyingClientAccess(t *testing.T) {
	t.Parallel()
	skipIfNoAWSCredentials(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Create S3 client
	client, err := awssdk.NewS3Client(ctx, &config.AWS{
		// No Region specified - should auto-detect
	}, &config.S3{
		Bucket: "nix-cache",
	})
	if err != nil {
		t.Fatalf("Failed to create S3 client: %v", err)
	}

	// Test accessing underlying client
	underlyingClient := client.UnderlyingClient()
	if underlyingClient == nil {
		t.Fatal("Expected non-nil underlying client")
	}

	// Use underlying client for direct S3 operations
	_, err = underlyingClient.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String("nix-cache"),
	})
	if err != nil {
		t.Fatalf("Failed to check bucket existence: %v", err)
	}

	t.Log("Successfully verified bucket existence using underlying client")
}
