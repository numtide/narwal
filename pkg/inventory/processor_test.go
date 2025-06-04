package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestProcessor is a test processor that records what it processes
type TestProcessor struct {
	ProcessedObjects []InventoryObject
	ProcessedBatches int
	BatchSizes       []int
	ShouldError      bool
	ErrorAfterN      int
	processedCount   int
}

func (tp *TestProcessor) ProcessBatch(ctx context.Context, objects []InventoryObject) error {
	tp.ProcessedBatches++
	tp.BatchSizes = append(tp.BatchSizes, len(objects))

	for _, obj := range objects {
		if tp.ShouldError && tp.processedCount >= tp.ErrorAfterN {
			return errors.New("test error")
		}
		if err := tp.ProcessObject(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

func (tp *TestProcessor) ProcessObject(ctx context.Context, object InventoryObject) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	tp.ProcessedObjects = append(tp.ProcessedObjects, object)
	tp.processedCount++
	return nil
}

func TestProcessParquetFileWithProcessor_ValidFile(t *testing.T) {
	processor := &TestProcessor{}
	ctx := context.Background()
	config := ProcessorConfig{
		BatchSize: 10,
		LogBatch:  false,
	}

	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err != nil {
		t.Fatalf("ProcessParquetFileWithProcessor failed: %v", err)
	}

	// Verify we processed the expected number of objects
	if len(processor.ProcessedObjects) != 2 {
		t.Errorf("Expected 2 objects, got %d", len(processor.ProcessedObjects))
	}

	// Verify batch processing
	if processor.ProcessedBatches != 1 {
		t.Errorf("Expected 1 batch, got %d", processor.ProcessedBatches)
	}

	if len(processor.BatchSizes) != 1 || processor.BatchSizes[0] != 2 {
		t.Errorf("Expected batch size [2], got %v", processor.BatchSizes)
	}

	// Verify object data
	obj1 := processor.ProcessedObjects[0]
	if obj1.Bucket != "nix-cache" {
		t.Errorf("Expected bucket 'nix-cache', got %s", obj1.Bucket)
	}
	if obj1.Key != "error-pages/403" {
		t.Errorf("Expected key 'error-pages/403', got %s", obj1.Key)
	}
	if obj1.Size != 3 {
		t.Errorf("Expected size 3, got %v", obj1.Size)
	}
	if obj1.Etag != "bbf94b34eb32268ada57a3be5062fe7d" {
		t.Errorf("Expected etag 'bbf94b34eb32268ada57a3be5062fe7d', got %v", obj1.Etag)
	}
	if obj1.StorageClass != "STANDARD" {
		t.Errorf("Expected storage class 'STANDARD', got %v", obj1.StorageClass)
	}

	obj2 := processor.ProcessedObjects[1]
	if obj2.Key != "error-pages/404" {
		t.Errorf("Expected key 'error-pages/404', got %s", obj2.Key)
	}
	if obj2.Etag != "4f4adcbf8c6f66dcfc8a3282ac2bf10a" {
		t.Errorf("Expected etag '4f4adcbf8c6f66dcfc8a3282ac2bf10a', got %v", obj2.Etag)
	}
}

func TestProcessParquetFileWithProcessor_SmallBatchSize(t *testing.T) {
	processor := &TestProcessor{}
	ctx := context.Background()
	config := ProcessorConfig{
		BatchSize: 1, // Process one object at a time
		LogBatch:  false,
	}

	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err != nil {
		t.Fatalf("ProcessParquetFileWithProcessor failed: %v", err)
	}

	// Should have 2 batches of size 1 each
	if processor.ProcessedBatches != 2 {
		t.Errorf("Expected 2 batches, got %d", processor.ProcessedBatches)
	}

	expectedBatchSizes := []int{1, 1}
	if len(processor.BatchSizes) != 2 || processor.BatchSizes[0] != 1 || processor.BatchSizes[1] != 1 {
		t.Errorf("Expected batch sizes %v, got %v", expectedBatchSizes, processor.BatchSizes)
	}

	if len(processor.ProcessedObjects) != 2 {
		t.Errorf("Expected 2 objects, got %d", len(processor.ProcessedObjects))
	}
}

func TestProcessParquetFileWithProcessor_CancelledContext(t *testing.T) {
	processor := &TestProcessor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	config := DefaultProcessorConfig()

	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err == nil {
		t.Error("Expected error for cancelled context")
	}

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestProcessParquetFileWithProcessor_ContextCancelledDuringProcessing(t *testing.T) {
	// Use a processor that will cause context cancellation during processing
	processor := &TestProcessor{}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	config := ProcessorConfig{
		BatchSize: 1,
		LogBatch:  false,
	}

	// Give it a moment to start processing, then it should be cancelled
	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err == nil {
		t.Error("Expected error for cancelled context during processing")
	}

	if err != context.DeadlineExceeded && err != context.Canceled {
		t.Errorf("Expected context cancellation error, got %v", err)
	}
}

func TestProcessParquetFileWithProcessor_ProcessorError(t *testing.T) {
	processor := &TestProcessor{
		ShouldError: true,
		ErrorAfterN: 1, // Error after processing first object
	}
	ctx := context.Background()
	config := ProcessorConfig{
		BatchSize: 10,
		LogBatch:  false,
	}

	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err == nil {
		t.Error("Expected processor error")
	}

	if err.Error() != "failed to process batch: test error" {
		t.Errorf("Expected 'failed to process batch: test error', got %v", err)
	}

	// Should have processed one object before erroring
	if len(processor.ProcessedObjects) != 1 {
		t.Errorf("Expected 1 processed object before error, got %d", len(processor.ProcessedObjects))
	}
}

func TestProcessParquetFileWithProcessor_NonExistentFile(t *testing.T) {
	processor := &TestProcessor{}
	ctx := context.Background()
	config := DefaultProcessorConfig()

	err := ProcessParquetFileWithProcessor(ctx, "nonexistent.parquet", processor, config)
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}

	if !strings.HasPrefix(err.Error(), "failed to open parquet file") {
		t.Errorf("Expected file open error, got %v", err)
	}
}

func TestProcessParquetFileWithProcessor_InvalidParquetFile(t *testing.T) {
	// Create a temporary invalid parquet file
	processor := &TestProcessor{}
	ctx := context.Background()
	config := DefaultProcessorConfig()

	// Use a Go source file as invalid parquet data
	err := ProcessParquetFileWithProcessor(ctx, "inventory.go", processor, config)
	if err == nil {
		t.Error("Expected error for invalid parquet file")
	}

	// Should get a parquet parsing error
	if !strings.HasPrefix(err.Error(), "failed to open parquet file") {
		t.Errorf("Expected parquet open error, got %v", err)
	}
}

func TestProcessParquetFileWithProcessor_LogBatchConfig(t *testing.T) {
	processor := &TestProcessor{}
	ctx := context.Background()

	// Test with LogBatch = true
	config := ProcessorConfig{
		BatchSize: 10,
		LogBatch:  true,
	}

	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err != nil {
		t.Fatalf("ProcessParquetFileWithProcessor failed: %v", err)
	}

	// Should process successfully regardless of LogBatch setting
	if len(processor.ProcessedObjects) != 2 {
		t.Errorf("Expected 2 objects, got %d", len(processor.ProcessedObjects))
	}
}

func TestProcessParquetFileWithProcessor_LargeBatchSize(t *testing.T) {
	processor := &TestProcessor{}
	ctx := context.Background()
	config := ProcessorConfig{
		BatchSize: 1000, // Much larger than number of objects
		LogBatch:  false,
	}

	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err != nil {
		t.Fatalf("ProcessParquetFileWithProcessor failed: %v", err)
	}

	// Should have 1 batch containing all objects
	if processor.ProcessedBatches != 1 {
		t.Errorf("Expected 1 batch, got %d", processor.ProcessedBatches)
	}

	if len(processor.BatchSizes) != 1 || processor.BatchSizes[0] != 2 {
		t.Errorf("Expected batch size [2], got %v", processor.BatchSizes)
	}

	if len(processor.ProcessedObjects) != 2 {
		t.Errorf("Expected 2 objects, got %d", len(processor.ProcessedObjects))
	}
}

// CountingProcessor counts objects by storage class
type CountingProcessor struct {
	Counts map[string]int
}

func (cp *CountingProcessor) ProcessBatch(ctx context.Context, objects []InventoryObject) error {
	if cp.Counts == nil {
		cp.Counts = make(map[string]int)
	}

	for _, obj := range objects {
		if err := cp.ProcessObject(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

func (cp *CountingProcessor) ProcessObject(ctx context.Context, object InventoryObject) error {
	if cp.Counts == nil {
		cp.Counts = make(map[string]int)
	}

	cp.Counts[object.StorageClass]++
	return nil
}

func TestProcessParquetFileWithProcessor_CustomProcessor(t *testing.T) {
	processor := &CountingProcessor{}
	ctx := context.Background()
	config := DefaultProcessorConfig()

	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err != nil {
		t.Fatalf("ProcessParquetFileWithProcessor failed: %v", err)
	}

	// Both objects should be STANDARD storage class
	if processor.Counts["STANDARD"] != 2 {
		t.Errorf("Expected 2 STANDARD objects, got %d", processor.Counts["STANDARD"])
	}

	if len(processor.Counts) != 1 {
		t.Errorf("Expected 1 storage class type, got %d: %v", len(processor.Counts), processor.Counts)
	}
}

// DebugProcessor helps us understand what's happening during processing
type DebugProcessor struct {
	ProcessedObjects []InventoryObject
	ProcessedBatches int
	BatchSizes       []int
}

func (dp *DebugProcessor) ProcessBatch(ctx context.Context, objects []InventoryObject) error {
	dp.ProcessedBatches++
	dp.BatchSizes = append(dp.BatchSizes, len(objects))

	for _, obj := range objects {
		if err := dp.ProcessObject(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

func (dp *DebugProcessor) ProcessObject(ctx context.Context, object InventoryObject) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	dp.ProcessedObjects = append(dp.ProcessedObjects, object)
	return nil
}

func TestDebugProcessor(t *testing.T) {
	processor := &DebugProcessor{}
	ctx := context.Background()
	config := ProcessorConfig{
		BatchSize: 10,
		LogBatch:  true,
	}

	t.Logf("Starting debug test")
	err := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", processor, config)
	if err != nil {
		t.Fatalf("ProcessParquetFileWithProcessor failed: %v", err)
	}

	t.Logf("Processed batches: %d", processor.ProcessedBatches)
	t.Logf("Batch sizes: %v", processor.BatchSizes)
	t.Logf("Processed objects count: %d", len(processor.ProcessedObjects))

	for i, obj := range processor.ProcessedObjects {
		t.Logf("Object %d: bucket=%s, key=%s, size=%v", i, obj.Bucket, obj.Key, obj.Size)
	}

	// Now test with the original TestProcessor to see if there's a difference
	testProcessor := &TestProcessor{}
	err2 := ProcessParquetFileWithProcessor(ctx, "testdata/sample.parquet", testProcessor, config)
	if err2 != nil {
		t.Fatalf("ProcessParquetFileWithProcessor with TestProcessor failed: %v", err2)
	}

	t.Logf("TestProcessor - Processed batches: %d", testProcessor.ProcessedBatches)
	t.Logf("TestProcessor - Batch sizes: %v", testProcessor.BatchSizes)
	t.Logf("TestProcessor - Processed objects count: %d", len(testProcessor.ProcessedObjects))
}
