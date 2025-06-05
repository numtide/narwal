package inventory

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestInventoryManifest(t *testing.T) {
	manifest := InventoryManifest{
		Files: []InventoryManifestInfo{
			{Key: "test/file1.parquet", Size: 1024},
			{Key: "test/file2.parquet", Size: 2048},
		},
	}

	if len(manifest.Files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(manifest.Files))
	}

	if manifest.Files[0].Key != "test/file1.parquet" {
		t.Errorf("Expected key 'test/file1.parquet', got %s", manifest.Files[0].Key)
	}

	if manifest.Files[0].Size != 1024 {
		t.Errorf("Expected size 1024, got %d", manifest.Files[0].Size)
	}
}

func TestLoggingProcessor(t *testing.T) {
	processor := &LoggingProcessor{}
	ctx := context.Background()

	// Test ProcessObject
	size := int64(1024)
	obj := InventoryObject{
		Bucket: "test-bucket",
		Key:    "test/object.txt",
		Size:   size,
	}

	err := processor.ProcessObject(ctx, obj)
	if err != nil {
		t.Errorf("ProcessObject should not return error, got %v", err)
	}

	// Test ProcessBatch
	objects := []InventoryObject{obj, obj}

	err = processor.ProcessBatch(ctx, objects)
	if err != nil {
		t.Errorf("ProcessBatch should not return error, got %v", err)
	}
}

func TestLoggingProcessorWithCancelledContext(t *testing.T) {
	processor := &LoggingProcessor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	size := int64(1024)
	objects := []InventoryObject{
		{Bucket: "test-bucket", Key: "test/object1.txt", Size: size},
		{Bucket: "test-bucket", Key: "test/object2.txt", Size: size},
	}

	err := processor.ProcessBatch(ctx, objects)
	if err == nil {
		t.Error("ProcessBatch should return error for cancelled context")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

type MockProcessor struct {
	ProcessedObjects []InventoryObject
	ProcessedBatches int
	ShouldError      bool
}

func (m *MockProcessor) ProcessBatch(ctx context.Context, objects []InventoryObject) error {
	if m.ShouldError {
		return context.Canceled
	}

	m.ProcessedBatches++
	for _, obj := range objects {
		if err := m.ProcessObject(ctx, obj); err != nil {
			return err
		}
	}

	return nil
}

func (m *MockProcessor) ProcessObject(ctx context.Context, object InventoryObject) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if m.ShouldError {
		return context.Canceled
	}

	m.ProcessedObjects = append(m.ProcessedObjects, object)

	return nil
}

func TestMockProcessor(t *testing.T) {
	processor := &MockProcessor{}
	ctx := context.Background()

	size := int64(1024)
	objects := []InventoryObject{
		{Bucket: "test-bucket", Key: "test/object1.txt", Size: size},
		{Bucket: "test-bucket", Key: "test/object2.txt", Size: size},
	}

	err := processor.ProcessBatch(ctx, objects)
	if err != nil {
		t.Errorf("ProcessBatch should not return error, got %v", err)
	}

	if len(processor.ProcessedObjects) != 2 {
		t.Errorf("Expected 2 processed objects, got %d", len(processor.ProcessedObjects))
	}

	if processor.ProcessedBatches != 1 {
		t.Errorf("Expected 1 processed batch, got %d", processor.ProcessedBatches)
	}
}

func TestMockProcessorWithError(t *testing.T) {
	processor := &MockProcessor{ShouldError: true}
	ctx := context.Background()

	size := int64(1024)
	objects := []InventoryObject{
		{Bucket: "test-bucket", Key: "test/object1.txt", Size: size},
	}

	err := processor.ProcessBatch(ctx, objects)
	if err == nil {
		t.Error("ProcessBatch should return error when ShouldError is true")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestProcessParquetFile_WithSampleData(t *testing.T) {
	ctx := context.Background()

	// This test uses the legacy ProcessParquetFile function with sample.parquet
	// We can't easily capture the logged output, but we can verify it doesn't error
	err := ProcessParquetFile(ctx, "testdata/sample.parquet")
	if err != nil {
		t.Fatalf("ProcessParquetFile failed: %v", err)
	}
	// The function should complete without error
	// The LoggingProcessor will have logged the objects but we can't easily verify that in this test
}

// MockS3Client implements the S3Client interface for testing.
type MockS3Client struct {
	CommonPrefixes []string
	ShouldError    bool
}

func (m *MockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.ShouldError {
		return nil, context.Canceled
	}

	var commonPrefixes []types.CommonPrefix
	for _, prefix := range m.CommonPrefixes {
		commonPrefixes = append(commonPrefixes, types.CommonPrefix{
			Prefix: aws.String(prefix),
		})
	}

	return &s3.ListObjectsV2Output{
		CommonPrefixes: commonPrefixes,
		IsTruncated:    aws.Bool(false),
	}, nil
}

func (m *MockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	// Default implementation for existing tests that don't use manifest functionality
	return nil, &types.NoSuchKey{
		Message: aws.String("GetObject not implemented in this mock"),
	}
}

func TestNewClient(t *testing.T) {
	mockS3 := &MockS3Client{}

	tests := []struct {
		name           string
		bucket         string
		prefix         string
		expectedPrefix string
	}{
		{
			name:           "prefix without trailing slash",
			bucket:         "test-bucket",
			prefix:         "inventory/data",
			expectedPrefix: "inventory/data/",
		},
		{
			name:           "prefix with trailing slash",
			bucket:         "test-bucket",
			prefix:         "inventory/data/",
			expectedPrefix: "inventory/data/",
		},
		{
			name:           "empty prefix",
			bucket:         "test-bucket",
			prefix:         "",
			expectedPrefix: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(mockS3, tt.bucket, tt.prefix, "/tmp/test")
			if err != nil {
				t.Fatalf("NewClient failed: %v", err)
			}

			if client.bucket != tt.bucket {
				t.Errorf("Expected bucket %s, got %s", tt.bucket, client.bucket)
			}

			if client.prefix != tt.expectedPrefix {
				t.Errorf("Expected prefix %s, got %s", tt.expectedPrefix, client.prefix)
			}
		})
	}
}

func TestClient_GetDates(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		commonPrefixes []string
		prefix         string
		expectedDates  []string
		shouldError    bool
	}{
		{
			name: "valid dates sorted lexicographically",
			commonPrefixes: []string{
				"inventory/2025-06-01T01-00Z/",
				"inventory/2025-06-03T01-00Z/",
				"inventory/2025-06-02T01-00Z/",
			},
			prefix: "inventory/",
			expectedDates: []string{
				"2025-06-01T01-00Z",
				"2025-06-02T01-00Z",
				"2025-06-03T01-00Z",
			},
		},
		{
			name: "mixed valid and invalid directories",
			commonPrefixes: []string{
				"inventory/2025-06-01T01-00Z/",
				"inventory/invalid-dir/",
				"inventory/2025-06-02T01-00Z/",
				"inventory/another-invalid/",
			},
			prefix: "inventory/",
			expectedDates: []string{
				"2025-06-01T01-00Z",
				"2025-06-02T01-00Z",
			},
		},
		{
			name: "no valid dates",
			commonPrefixes: []string{
				"inventory/invalid-dir/",
				"inventory/another-invalid/",
			},
			prefix:        "inventory/",
			expectedDates: []string{},
		},
		{
			name:           "empty result",
			commonPrefixes: []string{},
			prefix:         "inventory/",
			expectedDates:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockS3 := &MockS3Client{
				CommonPrefixes: tt.commonPrefixes,
				ShouldError:    tt.shouldError,
			}

			client, err := NewClient(mockS3, "test-bucket", tt.prefix, "/tmp/test")
			if err != nil {
				t.Fatalf("NewClient failed: %v", err)
			}

			dates, err := client.GetDates(ctx)

			if tt.shouldError {
				if err == nil {
					t.Error("Expected error but got none")
				}

				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(dates) != len(tt.expectedDates) {
				t.Errorf("Expected %d dates, got %d", len(tt.expectedDates), len(dates))
				return
			}

			for i, expectedDate := range tt.expectedDates {
				if dates[i] != expectedDate {
					t.Errorf("Expected date[%d] = %s, got %s", i, expectedDate, dates[i])
				}
			}
		})
	}
}

func TestClient_GetDates_WithError(t *testing.T) {
	ctx := context.Background()

	mockS3 := &MockS3Client{
		ShouldError: true,
	}

	client, err := NewClient(mockS3, "test-bucket", "inventory/", "/tmp/test")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.GetDates(ctx)

	if err == nil {
		t.Error("Expected error but got none")
	}
}

func TestNewParquetFileReader(t *testing.T) {
	// Test with valid parquet file
	reader, err := NewParquetFileReader("testdata/sample.parquet", 1625)
	if err != nil {
		t.Fatalf("NewParquetFileReader failed: %v", err)
	}
	defer reader.Close()

	// Test reading objects
	objects := make([]InventoryObject, 10)

	n, err := reader.Read(objects)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if n != 2 {
		t.Errorf("Expected 2 objects, got %d", n)
	}

	// Verify first object
	obj1 := objects[0]
	if obj1.Bucket != "nix-cache" {
		t.Errorf("Expected bucket 'nix-cache', got %s", obj1.Bucket)
	}

	if obj1.Key != "error-pages/403" {
		t.Errorf("Expected key 'error-pages/403', got %s", obj1.Key)
	}

	// Test EOF
	n, err = reader.Read(objects)
	if err != nil {
		t.Fatalf("Read at EOF failed: %v", err)
	}

	if n != 0 {
		t.Errorf("Expected 0 objects at EOF, got %d", n)
	}
}

func TestNewParquetFileReader_NonExistentFile(t *testing.T) {
	_, err := NewParquetFileReader("nonexistent.parquet", 1625)
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}
}
