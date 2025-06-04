package inventory

import (
	"context"
	"fmt"
	"log"
)

// ExampleCustomProcessor demonstrates how to create a custom processor
type ExampleCustomProcessor struct {
	processedCount int
	totalSize      int64
}

// ProcessBatch processes a batch of inventory objects
func (p *ExampleCustomProcessor) ProcessBatch(ctx context.Context, objects []InventoryObject) error {
	for _, object := range objects {
		if err := p.ProcessObject(ctx, object); err != nil {
			return err
		}
	}
	return nil
}

// ProcessObject processes a single inventory object
func (p *ExampleCustomProcessor) ProcessObject(ctx context.Context, object InventoryObject) error {
	p.processedCount++
	p.totalSize += object.Size

	// Example: only process objects larger than 1MB
	if object.Size > 1024*1024 {
		fmt.Printf("Large object: %s (size: %d)\n", object.Key, object.Size)
	}

	return nil
}

// GetStats returns processing statistics
func (p *ExampleCustomProcessor) GetStats() (int, int64) {
	return p.processedCount, p.totalSize
}

// Example_customProcessor demonstrates how to use a custom processor
func Example_customProcessor() {
	ctx := context.Background()
	processor := &ExampleCustomProcessor{}

	config := ProcessorConfig{
		BatchSize: 500,
		LogBatch:  false,
	}

	// This would process a real parquet file
	// err := ProcessParquetFileWithProcessor(ctx, "inventory.parquet", processor, config)
	// if err != nil {
	//     log.Fatal(err)
	// }

	count, totalSize := processor.GetStats()
	log.Printf("Processed %d objects with total size %d bytes", count, totalSize)

	// Use variables to avoid unused variable errors
	_ = ctx
	_ = config
}

// Example_loggingProcessor demonstrates the default logging processor
func Example_loggingProcessor() {
	ctx := context.Background()

	// Using the simple ProcessParquetFile function (uses LoggingProcessor internally)
	// err := ProcessParquetFile(ctx, "inventory.parquet")
	// if err != nil {
	//     log.Fatal(err)
	// }

	// Or explicitly using the LoggingProcessor
	processor := &LoggingProcessor{}
	config := DefaultProcessorConfig()

	// err := ProcessParquetFileWithProcessor(ctx, "inventory.parquet", processor, config)
	// if err != nil {
	//     log.Fatal(err)
	// }

	// Use variables to avoid unused variable errors
	_ = ctx
	_ = processor
	_ = config
}

// Example_inventoryClient demonstrates how to use the inventory client to get available dates
func Example_inventoryClient() {
	ctx := context.Background()
	
	// In real usage, you would create an S3 client like this:
	// cfg, err := config.LoadDefaultConfig(ctx)
	// if err != nil {
	//     log.Fatal(err)
	// }
	// s3Client := s3.NewFromConfig(cfg)
	
	// For this example, we'll use a mock
	mockS3 := &MockS3Client{
		CommonPrefixes: []string{
			"inventory/2025-06-01T01-00Z/",
			"inventory/2025-06-02T01-00Z/",
			"inventory/2025-06-03T01-00Z/",
		},
	}
	
	// Create inventory client
	client, err := NewClient(mockS3, "my-inventory-bucket", "inventory/", "/tmp/test-cache")
	if err != nil {
		log.Fatal(err)
	}
	
	// Get available dates
	dates, err := client.GetDates(ctx)
	if err != nil {
		log.Fatal(err)
	}
	
	if len(dates) > 0 {
		fmt.Printf("Latest available date: %s\n", dates[len(dates)-1])
		fmt.Printf("Total available dates: %d\n", len(dates))
	}
	
	// Output:
	// Latest available date: 2025-06-03T01-00Z
	// Total available dates: 3
}
