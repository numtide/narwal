// Package inventory provides types and functions for working with S3 inventory data
package inventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/parquet-go/parquet-go"
)

// ParquetFileReader provides an interface for reading inventory objects from a parquet file.
type ParquetFileReader interface {
	// Read fills the provided array with inventory objects and returns how many were read
	// Returns 0 when EOF is reached, or an error if reading fails
	Read(objects []InventoryObject) (int, error)
	// Close releases resources associated with the reader
	Close() error
}

// parquetFileReader implements ParquetFileReader.
type parquetFileReader struct {
	file   *os.File
	reader *parquet.GenericReader[InventoryObject]
}

// NewParquetFileReader creates a new parquet file reader for the given file.
func NewParquetFileReader(localPath string, fileSize int64) (ParquetFileReader, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open parquet file: %w", err)
	}

	// Create parquet file reader
	pf, err := parquet.OpenFile(file, fileSize)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to open parquet file: %w", err)
	}

	// Create reader for InventoryObject
	reader := parquet.NewGenericReader[InventoryObject](pf)

	return &parquetFileReader{
		file:   file,
		reader: reader,
	}, nil
}

// Read implements ParquetFileReader.
func (r *parquetFileReader) Read(objects []InventoryObject) (int, error) {
	n, err := r.reader.Read(objects)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("failed to read from parquet file: %w", err)
	}

	return n, nil
}

// Close implements ParquetFileReader.
func (r *parquetFileReader) Close() error {
	if r.reader != nil {
		r.reader.Close()
	}

	if r.file != nil {
		return r.file.Close()
	}

	return nil
}

// ObjectProcessor defines the interface for processing inventory objects.
type ObjectProcessor interface {
	// ProcessBatch processes a batch of inventory objects
	ProcessBatch(ctx context.Context, objects []InventoryObject) error
	// ProcessObject processes a single inventory object
	ProcessObject(ctx context.Context, object InventoryObject) error
}

// ProcessorConfig contains configuration for parquet file processing.
type ProcessorConfig struct {
	BatchSize int
	LogBatch  bool
}

// DefaultProcessorConfig returns a default processor configuration.
func DefaultProcessorConfig() ProcessorConfig {
	return ProcessorConfig{
		BatchSize: 1000,
		LogBatch:  true,
	}
}

// InventoryObject represents a single object in an S3 inventory parquet file
// This is the file schema we see in the manifest.json:
//
//	message s3.inventory {
//	  required binary bucket (STRING);
//	  required binary key (STRING);
//	  optional int64 size;
//	  optional int64 last_modified_date (TIMESTAMP(MILLIS,true));
//	  optional binary e_tag (STRING);
//	  optional binary storage_class (STRING);
//	}
type InventoryObject struct {
	Bucket       string    `parquet:"bucket"`
	Key          string    `parquet:"key"`
	Size         int64     `parquet:"size"`
	LastModified time.Time `parquet:"last_modified_date"`
	Etag         string    `parquet:"e_tag"`
	StorageClass string    `parquet:"storage_class"`
}

// ProcessParquetFile reads and processes a parquet file from local storage
// This is the legacy function that maintains backward compatibility.
func ProcessParquetFile(ctx context.Context, localPath string) error {
	processor := &LoggingProcessor{}
	return ProcessParquetFileWithProcessor(ctx, localPath, processor, DefaultProcessorConfig())
}

// ProcessParquetFileWithProcessor reads and processes a parquet file using a custom processor.
func ProcessParquetFileWithProcessor(ctx context.Context, localPath string, processor ObjectProcessor, config ProcessorConfig) error {
	log.Debug("Processing local parquet file", "path", localPath)

	// Open the parquet file
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open parquet file: %w", err)
	}
	defer file.Close()

	// Get file info for size
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Create parquet file reader
	pf, err := parquet.OpenFile(file, stat.Size())
	if err != nil {
		return fmt.Errorf("failed to open parquet file: %w", err)
	}

	// Get total number of rows
	num := pf.NumRows()
	log.Info("Parquet file objects", "count", num, "path", localPath)

	// Create reader for InventoryObject
	reader := parquet.NewGenericReader[InventoryObject](pf)
	defer reader.Close()

	// Read and process objects in batches to avoid memory issues
	objects := make([]InventoryObject, config.BatchSize)

	for {
		// Check if context was cancelled
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, err := reader.Read(objects)

		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("failed to read batch: %w", err)
		}

		if n == 0 {
			break
		}

		if config.LogBatch {
			log.Info("Processing batch", "count", n)
		}

		// Process the batch
		batch := objects[:n]
		if err := processor.ProcessBatch(ctx, batch); err != nil {
			return fmt.Errorf("failed to process batch: %w", err)
		}
	}

	return nil
}

// LoggingProcessor is a simple processor that logs inventory objects.
type LoggingProcessor struct{}

// ProcessBatch processes a batch of inventory objects by logging each one.
func (p *LoggingProcessor) ProcessBatch(ctx context.Context, objects []InventoryObject) error {
	for _, object := range objects {
		// Check if context was cancelled during batch processing
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := p.ProcessObject(ctx, object); err != nil {
			return err
		}
	}

	return nil
}

// ProcessObject processes a single inventory object by logging it.
func (p *LoggingProcessor) ProcessObject(ctx context.Context, object InventoryObject) error {
	log.Print(object)
	return nil
}
