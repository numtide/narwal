package inventory

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// mockS3Client implements S3Client for testing
type mockS3Client struct {
	getObjectFunc func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return nil, nil // Not used in manifest tests
}

func (m *mockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(ctx, params, optFns...)
	}
	return nil, errors.New("not implemented")
}

// readTestData reads test manifest from testdata directory
func readTestData(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatalf("Failed to read test data %s: %v", filename, err)
	}
	return data
}

func TestGetManifest(t *testing.T) {
	testData := readTestData(t, "manifest.json")

	client := &Client{
		s3Client: &mockS3Client{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				if *params.Bucket != "test-bucket" {
					t.Errorf("Expected bucket 'test-bucket', got %s", *params.Bucket)
				}
				if *params.Key != "test-prefix/2025-05-13T01-00Z/manifest.json" {
					t.Errorf("Expected key 'test-prefix/2025-05-13T01-00Z/manifest.json', got %s", *params.Key)
				}

				return &s3.GetObjectOutput{
					Body: io.NopCloser(strings.NewReader(string(testData))),
				}, nil
			},
		},
		bucket: "test-bucket",
		prefix: "test-prefix/",
	}

	ctx := context.Background()
	manifest, err := client.GetManifest(ctx, "2025-05-13T01-00Z")
	if err != nil {
		t.Fatalf("GetManifest failed: %v", err)
	}

	// Verify manifest structure
	if manifest.SourceBucket != "nix-cache" {
		t.Errorf("Expected sourceBucket 'nix-cache', got %s", manifest.SourceBucket)
	}

	if manifest.DestBucket != "arn:aws:s3:::nix-cache-inventory" {
		t.Errorf("Expected destinationBucket 'arn:aws:s3:::nix-cache-inventory', got %s", manifest.DestBucket)
	}

	if manifest.FileFormat != "Parquet" {
		t.Errorf("Expected fileFormat 'Parquet', got %s", manifest.FileFormat)
	}

	if len(manifest.Files) != 3 {
		t.Errorf("Expected 3 files, got %d", len(manifest.Files))
	}

	// Verify first file
	firstFile := manifest.Files[0]
	if firstFile.Key != "nix-cache/nix-cache-inventory/data/970e1c02-f96c-488b-be19-ce40bc7d2c4b.parquet" {
		t.Errorf("Unexpected first file key: %s", firstFile.Key)
	}

	if firstFile.Size != 236045595 {
		t.Errorf("Expected first file size 236045595, got %d", firstFile.Size)
	}

	if firstFile.MD5Checksum != "b71ad288b57ddcb1de6fe1eead620220" {
		t.Errorf("Expected first file MD5 'b71ad288b57ddcb1de6fe1eead620220', got %s", firstFile.MD5Checksum)
	}
}

func TestGetManifest_S3Error(t *testing.T) {
	client := &Client{
		s3Client: &mockS3Client{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return nil, &types.NoSuchKey{
					Message: aws.String("The specified key does not exist."),
				}
			},
		},
		bucket: "test-bucket",
		prefix: "test-prefix/",
	}

	ctx := context.Background()
	_, err := client.GetManifest(ctx, "nonexistent-date")
	if err == nil {
		t.Fatal("Expected error for nonexistent manifest, got nil")
	}

	if !strings.Contains(err.Error(), "failed to get manifest file") {
		t.Errorf("Expected error message to contain 'failed to get manifest file', got: %v", err)
	}
}

func TestGetManifest_InvalidJSON(t *testing.T) {
	client := &Client{
		s3Client: &mockS3Client{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return &s3.GetObjectOutput{
					Body: io.NopCloser(strings.NewReader(`{"invalid": json,}`)),
				}, nil
			},
		},
		bucket: "test-bucket",
		prefix: "test-prefix/",
	}

	ctx := context.Background()
	_, err := client.GetManifest(ctx, "test-date")
	if err == nil {
		t.Fatal("Expected error for invalid JSON, got nil")
	}

	if !strings.Contains(err.Error(), "failed to parse manifest JSON") {
		t.Errorf("Expected error message to contain 'failed to parse manifest JSON', got: %v", err)
	}
}

func TestValidateManifest(t *testing.T) {
	tests := []struct {
		name        string
		manifest    InventoryManifest
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid manifest",
			manifest: InventoryManifest{
				FileFormat: "Parquet",
				Files: []InventoryManifestInfo{
					{Key: "test/file1.parquet", Size: 1000, MD5Checksum: "abc123"},
					{Key: "test/file2.parquet", Size: 2000, MD5Checksum: "def456"},
				},
			},
			expectError: false,
		},
		{
			name: "empty files",
			manifest: InventoryManifest{
				FileFormat: "Parquet",
				Files:      []InventoryManifestInfo{},
			},
			expectError: true,
			errorMsg:    "manifest contains no files",
		},
		{
			name: "missing file format",
			manifest: InventoryManifest{
				Files: []InventoryManifestInfo{
					{Key: "test/file1.parquet", Size: 1000, MD5Checksum: "abc123"},
				},
			},
			expectError: true,
			errorMsg:    "manifest missing file format",
		},
		{
			name: "file missing key",
			manifest: InventoryManifest{
				FileFormat: "Parquet",
				Files: []InventoryManifestInfo{
					{Key: "", Size: 1000, MD5Checksum: "abc123"},
				},
			},
			expectError: true,
			errorMsg:    "file 0 missing key",
		},
		{
			name: "file invalid size",
			manifest: InventoryManifest{
				FileFormat: "Parquet",
				Files: []InventoryManifestInfo{
					{Key: "test/file1.parquet", Size: 0, MD5Checksum: "abc123"},
				},
			},
			expectError: true,
			errorMsg:    "file 0 has invalid size: 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.manifest.Validate()
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestTotalSize(t *testing.T) {
	manifest := InventoryManifest{
		Files: []InventoryManifestInfo{
			{Key: "file1.parquet", Size: 1000},
			{Key: "file2.parquet", Size: 2000},
			{Key: "file3.parquet", Size: 3000},
		},
	}

	expected := int64(6000)
	actual := manifest.TotalSize()
	if actual != expected {
		t.Errorf("Expected total size %d, got %d", expected, actual)
	}
}

func TestTotalSize_EmptyManifest(t *testing.T) {
	manifest := InventoryManifest{
		Files: []InventoryManifestInfo{},
	}

	expected := int64(0)
	actual := manifest.TotalSize()
	if actual != expected {
		t.Errorf("Expected total size %d, got %d", expected, actual)
	}
}
