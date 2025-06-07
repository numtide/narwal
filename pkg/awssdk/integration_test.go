// Package awssdk_test contains integration tests for the awssdk package.
// These tests verify real-world functionality against actual S3 services.
//
// Integration tests included:
// - TestIntegration_NixCacheBucket: Tests against s3://nix-cache bucket
// - TestIntegration_NixReleasesBucket: Tests against s3://nix-releases bucket
// - TestIntegration_AutoDetectRegion: Tests automatic region detection
// - TestIntegration_GetObjectMetadata: Tests object metadata retrieval
// - TestIntegration_UnderlyingClientAccess: Tests underlying MinIO client access
// - TestIntegration_MinIOServer: Tests against a temporary MinIO server binary
//
// Prerequisites:
// - AWS CLI credentials configured (tests will skip if not available)
// - MinIO binary available for MinIO server test
//
// Run with: go test ./pkg/awssdk/... -run TestIntegration -v
package awssdk_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/awssdk"
)

// skipIfNoAWSCredentials checks if AWS credentials are available and skips
// the test if not.
func skipIfNoAWSCredentials(t *testing.T) {
	t.Helper()

	// Try to create credentials using AWS CLI default
	_, err := awssdk.NewCredentials(t.Context(), awssdk.CredentialsConfig{})
	if err != nil {
		t.Skipf("Skipping integration test: AWS credentials not available (%v)", err)
	}
}

func TestIntegration_NixCacheBucket(t *testing.T) {
	t.Parallel()
	skipIfNoAWSCredentials(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Get AWS credentials
	creds, err := awssdk.NewCredentials(ctx, awssdk.CredentialsConfig{})
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
	client, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
		Bucket: "nix-cache",
		Region: region,
		UseSSL: true,
	}, creds)
	if err != nil {
		t.Fatalf("Failed to create S3 client for nix-cache: %v", err)
	}

	// Verify bucket name
	if client.BucketName() != "nix-cache" {
		t.Errorf("Expected bucket name 'nix-cache', got '%s'", client.BucketName())
	}

	// List some objects to verify connectivity
	listOpts := minio.ListObjectsOptions{
		Prefix:    "", // Start with no prefix to be more flexible
		Recursive: false,
		MaxKeys:   5, // Just get a few objects to test
	}

	objectCh := client.ListObjects(ctx, listOpts)
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

	// Get AWS credentials
	creds, err := awssdk.NewCredentials(ctx, awssdk.CredentialsConfig{})
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
	client, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
		Bucket: "nix-releases",
		Region: region,
		UseSSL: true,
	}, creds)
	if err != nil {
		t.Fatalf("Failed to create S3 client for nix-releases: %v", err)
	}

	// Verify bucket name
	if client.BucketName() != "nix-releases" {
		t.Errorf("Expected bucket name 'nix-releases', got '%s'", client.BucketName())
	}

	// List some objects to verify connectivity
	listOpts := minio.ListObjectsOptions{
		Prefix:    "", // Start with no prefix to be more flexible
		Recursive: false,
		MaxKeys:   5, // Just get a few objects to test
	}

	objectCh := client.ListObjects(ctx, listOpts)
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
			// Create credentials
			creds, err := awssdk.NewCredentials(ctx, awssdk.CredentialsConfig{})
			if err != nil {
				t.Skipf("Failed to get AWS credentials: %v", err)
			}

			// Create S3 client without specifying region (should auto-detect)
			client, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
				Bucket: tt.bucket,
				UseSSL: true,
				// No Region specified - should auto-detect
			}, creds)
			if err != nil {
				t.Skipf("Failed to create S3 client with auto-detect for %s (access denied?): %v", tt.bucket, err)
			}

			// Verify bucket name
			if client.BucketName() != tt.bucket {
				t.Errorf("Expected bucket name '%s', got '%s'", tt.bucket, client.BucketName())
			}

			// Try a simple operation to verify the client works
			listOpts := minio.ListObjectsOptions{
				MaxKeys: 1, // Just get one object to verify connectivity
			}

			objectCh := client.ListObjects(ctx, listOpts)
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

	// Create credentials
	creds, err := awssdk.NewCredentials(ctx, awssdk.CredentialsConfig{})
	if err != nil {
		t.Fatalf("Failed to get AWS credentials: %v", err)
	}

	// Create S3 client for nix-cache bucket (more likely to be accessible)
	client, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
		Bucket: "nix-cache",
		UseSSL: true,
	}, creds)
	if err != nil {
		t.Fatalf("Failed to create S3 client: %v", err)
	}

	// First, find an actual object key by listing (skip directories)
	listOpts := minio.ListObjectsOptions{
		Prefix:  "",
		MaxKeys: 10, // Get a few objects to find a non-directory
	}

	var testKey string

	objectCh := client.ListObjects(ctx, listOpts)
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
	statOpts := minio.StatObjectOptions{}

	objectInfo, err := client.StatObject(ctx, testKey, statOpts)
	if err != nil {
		t.Fatalf("Failed to stat object %s: %v", testKey, err)
	}

	t.Logf("Object info for %s:", testKey)
	t.Logf("  Size: %d bytes", objectInfo.Size)
	t.Logf("  Last Modified: %s", objectInfo.LastModified)
	t.Logf("  Content Type: %s", objectInfo.ContentType)

	if objectInfo.Size <= 0 {
		t.Error("Expected object size to be greater than 0")
	}
}

func TestIntegration_UnderlyingClientAccess(t *testing.T) {
	t.Parallel()
	skipIfNoAWSCredentials(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Create credentials
	creds, err := awssdk.NewCredentials(ctx, awssdk.CredentialsConfig{})
	if err != nil {
		t.Fatalf("Failed to get AWS credentials: %v", err)
	}

	// Create S3 client
	client, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
		Bucket: "nix-cache",
		UseSSL: true,
	}, creds)
	if err != nil {
		t.Fatalf("Failed to create S3 client: %v", err)
	}

	// Test accessing underlying client
	underlyingClient := client.UnderlyingClient()
	if underlyingClient == nil {
		t.Fatal("Expected non-nil underlying client")
	}

	// Use underlying client for direct MinIO operations
	exists, err := underlyingClient.BucketExists(ctx, "nix-cache")
	if err != nil {
		t.Fatalf("Failed to check bucket existence: %v", err)
	}

	if !exists {
		t.Error("Expected nix-cache bucket to exist")
	}

	t.Log("Successfully verified bucket existence using underlying client")
}

func TestIntegration_MinIOServer(t *testing.T) {
	t.Parallel()
	// This test spins up a temporary MinIO server using the binary
	// Skip if MinIO binary is not available
	if !isMinIOAvailable(t) {
		t.Skip("MinIO binary not available, skipping MinIO integration test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	// Start MinIO server
	endpoint, cleanup := startMinIOServer(t, ctx)
	defer cleanup()

	// Wait a bit for MinIO to start up
	time.Sleep(2 * time.Second)

	// Create credentials for MinIO
	creds, err := awssdk.NewCredentials(ctx, awssdk.CredentialsConfig{
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	})
	if err != nil {
		t.Fatalf("Failed to create MinIO credentials: %v", err)
	}

	// Create S3 client for MinIO
	client, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
		Bucket:   "test-bucket",
		Endpoint: endpoint,
		UseSSL:   false, // Local MinIO typically runs without SSL
	}, creds)
	if err != nil {
		t.Fatalf("Failed to create S3 client for MinIO: %v", err)
	}

	// Create bucket
	err = client.UnderlyingClient().MakeBucket(ctx, "test-bucket", minio.MakeBucketOptions{})
	if err != nil {
		t.Fatalf("Failed to create test bucket: %v", err)
	}

	// Test basic operations
	testContent := "Hello, MinIO!"
	putOpts := minio.PutObjectOptions{
		ContentType: "text/plain",
	}

	// Put object
	uploadInfo, err := client.PutObject(ctx, "test-key",
		strings.NewReader(testContent),
		int64(len(testContent)),
		putOpts)
	if err != nil {
		t.Fatalf("Failed to put object: %v", err)
	}

	t.Logf("Uploaded object: %s (ETag: %s)", uploadInfo.Key, uploadInfo.ETag)

	// Get object metadata
	statOpts := minio.StatObjectOptions{}

	objectInfo, err := client.StatObject(ctx, "test-key", statOpts)
	if err != nil {
		t.Fatalf("Failed to stat object: %v", err)
	}

	t.Logf("Object info - Size: %d, ContentType: %s", objectInfo.Size, objectInfo.ContentType)

	if objectInfo.Size != int64(len(testContent)) {
		t.Errorf("Expected object size %d, got %d", len(testContent), objectInfo.Size)
	}

	// List objects
	listOpts := minio.ListObjectsOptions{}
	objectCh := client.ListObjects(ctx, listOpts)
	foundObject := false

	for object := range objectCh {
		if object.Err != nil {
			t.Fatalf("Error listing objects: %v", object.Err)
		}

		if object.Key == "test-key" {
			foundObject = true

			t.Logf("Found uploaded object: %s", object.Key)
		}
	}

	if !foundObject {
		t.Error("Expected to find uploaded object in list")
	}

	// Get object content
	getOpts := minio.GetObjectOptions{}

	object, err := client.GetObject(ctx, "test-key", getOpts)
	if err != nil {
		t.Fatalf("Failed to get object: %v", err)
	}

	defer object.Close() //nolint:errcheck

	content, err := io.ReadAll(object)
	if err != nil {
		t.Fatalf("Failed to read object content: %v", err)
	}

	if string(content) != testContent {
		t.Errorf("Expected object content %q, got %q", testContent, string(content))
	}

	// Remove object
	removeOpts := minio.RemoveObjectOptions{}

	err = client.RemoveObject(ctx, "test-key", removeOpts)
	if err != nil {
		t.Fatalf("Failed to remove object: %v", err)
	}

	t.Log("Successfully completed all MinIO operations")
}

// Helper functions for MinIO binary management

func isMinIOAvailable(t *testing.T) bool {
	t.Helper()

	cmd := exec.Command("minio", "--version")

	return cmd.Run() == nil
}

func startMinIOServer(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()

	// Create temporary data directory
	dataDir := t.TempDir()

	// Set MinIO credentials via environment
	env := []string{
		"MINIO_ROOT_USER=minioadmin",
		"MINIO_ROOT_PASSWORD=minioadmin",
		"MINIO_ADDRESS=:9090",
		"MINIO_CONSOLE_ADDRESS=:9091",
	}

	// Start MinIO server
	cmd := exec.CommandContext(ctx, "minio", "server", dataDir) //nolint:gosec

	cmd.Env = append(os.Environ(), env...)

	// Start the server in background
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start MinIO server: %v", err)
	}

	endpoint := "localhost:9090"
	t.Logf("Started MinIO server on %s with data dir: %s", endpoint, dataDir)

	cleanup := func() {
		// Kill the MinIO process
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				t.Logf("Warning: failed to kill MinIO process: %v", err)
			} else {
				t.Log("Stopped MinIO server")
			}

			_ = cmd.Wait()
		}
	}

	return endpoint, cleanup
}
