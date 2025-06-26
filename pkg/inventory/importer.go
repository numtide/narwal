package inventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/store"
	"github.com/numtide/narwal/pkg/workarea"
	"github.com/parquet-go/parquet-go"
)

// ImportResult contains the results of importing a parquet file.
type ImportResult struct {
	TotalRecords     int64
	ProcessedRecords int64
	SkippedRecords   int64
	NarinfoCount     int64
}

// ParquetImporter handles importing S3 inventory parquet files into the database
type ParquetImporter struct {
	pgPool                *pgxpool.Pool
	cacheBucketClient     *awssdk.BucketClient
	narinfoWorkareaBucket *workarea.Bucket
}

// NewParquetImporter creates a new parquet importer
func NewParquetImporter(
	pgPool *pgxpool.Pool,
	cacheBucketClient *awssdk.BucketClient,
	narinfoWorkareaBucket *workarea.Bucket,
) *ParquetImporter {
	return &ParquetImporter{
		pgPool:                pgPool,
		cacheBucketClient:     cacheBucketClient,
		narinfoWorkareaBucket: narinfoWorkareaBucket,
	}
}

// ImportFile imports a single parquet file into the database
func (p *ParquetImporter) ImportFile(ctx context.Context, parquetFile string) (*ImportResult, error) {
	fileName := filepath.Base(parquetFile)
	log.Info("Starting parquet file processing", "file", fileName)

	startTime := time.Now()

	// Open and setup parquet reader
	reader, file, err := p.openParquetFile(parquetFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()   //nolint:errcheck
	defer reader.Close() //nolint:errcheck

	// Get database connection
	conn, err := p.pgPool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)

	// Process records in batches
	result := &ImportResult{}
	err = p.processRecords(ctx, reader, queries, fileName, startTime, result)
	if err != nil {
		return nil, err
	}

	log.Debug("Parquet file processed",
		"file", fileName,
		"total_records_read", result.TotalRecords,
		"records_processed", result.ProcessedRecords,
		"records_skipped", result.SkippedRecords,
		"narinfo_files", result.NarinfoCount)

	return result, nil
}

// openParquetFile opens a parquet file and creates a reader
func (p *ParquetImporter) openParquetFile(parquetFile string) (
	*parquet.GenericReader[S3InventoryRecord],
	*os.File,
	error,
) {
	fileName := filepath.Base(parquetFile)
	log.Debug("Opening parquet file", "file", fileName)

	file, err := os.Open(parquetFile) //nolint:gosec
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open parquet file: %w", err)
	}

	log.Info("Creating parquet reader", "file", fileName)
	reader := parquet.NewGenericReader[S3InventoryRecord](file)

	return reader, file, nil
}

// processRecords reads and processes all records from the parquet file
func (p *ParquetImporter) processRecords(ctx context.Context, reader *parquet.GenericReader[S3InventoryRecord], queries *db.Queries, fileName string, startTime time.Time, result *ImportResult) error {
	log.Info("Starting to read parquet records", "file", fileName)

	const batchSize = 1000
	records := make([]S3InventoryRecord, batchSize)

	batchCount := 0
	lastProgressTime := time.Now()

	for {
		n, err := reader.Read(records)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("failed to read parquet records: %w", err)
		}

		if n == 0 {
			if batchCount == 0 {
				log.Info("No records found in parquet file", "file", fileName)
			}
			break
		}

		batchCount++
		batchProcessed := 0

		if batchCount == 1 {
			log.Info("Successfully read first batch", "file", fileName, "records_in_batch", n)
		}

		// Process the batch of records
		err = p.processBatch(ctx, records[:n], queries, fileName, &batchProcessed, &lastProgressTime, startTime, result)
		if err != nil {
			return err
		}

		// Log progress every 5 batches or if it's the first few batches
		if batchCount%5 == 0 || batchCount <= 3 {
			log.Info("Batch progress",
				"file", fileName,
				"batch", batchCount,
				"batch_processed", batchProcessed,
				"batch_skipped", n-batchProcessed,
				"total_processed", result.ProcessedRecords,
				"total_read", result.TotalRecords)
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return nil
}

// processBatch processes a batch of records
func (p *ParquetImporter) processBatch(ctx context.Context, records []S3InventoryRecord, queries *db.Queries, fileName string, batchProcessed *int, lastProgressTime *time.Time, startTime time.Time, result *ImportResult) error {
	for _, record := range records {
		result.TotalRecords++

		// Log progress periodically
		p.logProgress(result.ProcessedRecords, fileName, lastProgressTime, startTime)

		// Skip invalid records
		if record.Key == "" || record.Size == nil {
			result.SkippedRecords++
			continue
		}

		// Process the record
		err := p.processRecord(ctx, record, queries, fileName, result)
		if err != nil {
			return err
		}

		result.ProcessedRecords++
		(*batchProcessed)++
	}

	return nil
}

// processRecord processes a single inventory record
func (p *ParquetImporter) processRecord(ctx context.Context, record S3InventoryRecord, queries *db.Queries, fileName string, result *ImportResult) error {
	// Analyze the path to determine object type
	analysis, err := store.AnalyzePath(record.Key)
	if err != nil {
		log.Error("Failed to analyze path - this may indicate data incompatibility",
			"key", record.Key,
			"error", err,
			"file", fileName,
			"processed_so_far", result.ProcessedRecords)
		return fmt.Errorf("failed to analyze path %s: %w (processed %d records before failure)", record.Key, err, result.ProcessedRecords)
	}

	// Generate hash from path
	hash, err := store.HashFromPath(record.Key, analysis.ObjectType)
	if err != nil {
		log.Error("Failed to generate hash - this may indicate data incompatibility",
			"key", record.Key,
			"error", err,
			"object_type", analysis.ObjectType,
			"file", fileName,
			"processed_so_far", result.ProcessedRecords)
		return fmt.Errorf("failed to generate hash for %s (type: %s): %w (processed %d records before failure)",
			record.Key, analysis.ObjectType, err, result.ProcessedRecords)
	}

	// Insert object into database
	err = queries.PutObject(ctx, db.PutObjectParams{
		Hash:            hash,
		ObjectType:      analysis.ObjectType,
		CompressionType: db.CompressionTypeNone, // Inventory doesn't track compression
		Path:            record.Key,
		Size:            *record.Size,
	})
	if err != nil {
		return fmt.Errorf("failed to insert object %s into database: %w", record.Key, err)
	}

	// If this is a narinfo file, download it and parse its metadata
	if analysis.ObjectType == db.ObjectTypeNarinfo {
		result.NarinfoCount++
		log.Debug("Processing narinfo file", "key", record.Key)

		err = p.processNarinfo(ctx, queries, record.Key, hash)
		if err != nil {
			log.Warn("Failed to download/process narinfo, continuing",
				"key", record.Key, "error", err)
			// Don't fail the entire import for narinfo processing errors
		} else {
			log.Debug("Successfully processed narinfo", "key", record.Key)
		}
	}

	return nil
}

// logProgress logs progress updates periodically
func (p *ParquetImporter) logProgress(processedRecords int64, fileName string, lastProgressTime *time.Time, startTime time.Time) {
	// Log progress every 100 records within a batch to show activity
	if processedRecords > 0 && processedRecords%100 == 0 {
		log.Info("Processing records",
			"file", fileName,
			"processed_so_far", processedRecords)
	}

	// Time-based progress logging every 30 seconds
	if time.Since(*lastProgressTime) > 30*time.Second {
		elapsed := time.Since(startTime)
		rate := float64(processedRecords) / elapsed.Seconds()

		log.Info("Time-based progress update",
			"file", fileName,
			"processed_records", processedRecords,
			"elapsed", elapsed.Round(time.Second),
			"records_per_sec", fmt.Sprintf("%.1f", rate))

		*lastProgressTime = time.Now()
	}
}

// processNarinfo downloads and processes a narinfo file
func (p *ParquetImporter) processNarinfo(ctx context.Context, queries *db.Queries, key, hash string) error {
	return p.downloadAndProcessNarinfo(ctx, queries, key, hash)
}

// downloadAndProcessNarinfo downloads a narinfo file from S3 and processes its metadata
func (p *ParquetImporter) downloadAndProcessNarinfo(ctx context.Context, queries *db.Queries, narinfoKey, hash string) error {
	// Create adapter for the bucket client
	s3Client := workarea.NewBucketClientAdapter(p.cacheBucketClient)

	// Download the narinfo file to workarea
	err := p.narinfoWorkareaBucket.Download(ctx, s3Client, narinfoKey, nil)
	if err != nil {
		return fmt.Errorf("failed to download narinfo %s: %w", narinfoKey, err)
	}

	// Read the downloaded narinfo file
	narinfoPath := p.narinfoWorkareaBucket.GetPath(narinfoKey)
	narinfoData, err := os.ReadFile(narinfoPath)
	if err != nil {
		return fmt.Errorf("failed to read narinfo file %s: %w", narinfoPath, err)
	}

	// Parse and insert narinfo data using the public store.PutNarInfo function
	return store.PutNarInfo(ctx, queries, hash, narinfoData)
}
