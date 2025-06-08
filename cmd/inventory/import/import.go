package import_

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/parquet-go/parquet-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/numtide/narwal/pkg/awssdk"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/store"
	"github.com/numtide/narwal/pkg/workarea"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import inventory parquet files into the Postgres database",
		Long: `Imports inventory parquet files into the Postgres database, indexing all objects
for garbage collection and cache management. The parquet files must already be downloaded.

This command reads the S3 inventory parquet files and stores object metadata in the
database, similar to how the HTTP PUT method works but in batch for inventory data.

Objects will be indexed with their paths, sizes, types, and metadata for efficient
garbage collection and cache management.`,
		Example: `  # Import a specific report
  narwal inventory import --bucket nix-cache-inventory --report 2025-06-07T01-00Z

  # Use custom workarea and database
  narwal inventory import --bucket nix-cache-inventory --report 2025-06-07T01-00Z \
    --workarea.path /tmp/my-cache --postgres.url "postgres://user:pass@localhost/db"`,
		RunE: runE,
	}

	// Add inventory-specific flags
	appconfig.SetInventoryFlags(cmd.Flags())

	// bind our command's flags to viper
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}

func runE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// Explicitly set flag values in viper since flag binding might not work properly
	if reportFlag := cmd.Flag("report"); reportFlag != nil && reportFlag.Changed {
		viper.Set("report", reportFlag.Value.String())
	}

	if bucketFlag := cmd.Flag("bucket"); bucketFlag != nil && bucketFlag.Changed {
		viper.Set("bucket", bucketFlag.Value.String())
	}

	if regionFlag := cmd.Flag("region"); regionFlag != nil && regionFlag.Changed {
		viper.Set("region", regionFlag.Value.String())
	}

	if prefixFlag := cmd.Flag("prefix"); prefixFlag != nil && prefixFlag.Changed {
		viper.Set("prefix", prefixFlag.Value.String())
	}

	// parse viper into our config object
	var fullCfg appconfig.Config
	if err := appconfig.FromViper(viper.GetViper(), &fullCfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	cfg := &fullCfg.Inventory
	if err := cfg.Validate(ctx, nil, fullCfg.Workarea.Path); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Require report ID for import command
	if cfg.ReportID == "" {
		return errors.New("report ID is required for import command (use --report flag)")
	}

	log.Info("Starting inventory import",
		"report_id", cfg.ReportID,
		"bucket", cfg.Bucket,
		"prefix", cfg.Prefix)

	// Connect to database and run migrations
	log.Info("Connecting to database and running migrations...")

	pgPool, err := db.Connect(ctx, fullCfg.Postgres.URL, true)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	defer pgPool.Close()

	log.Info("Connected to database and migrations completed")

	// Get the directory where parquet files should be
	bucket := cfg.Workarea.Bucket(cfg.Bucket, inventory.BucketConfig())
	manifestKey := fmt.Sprintf("%s%s/manifest.json", cfg.Prefix, cfg.ReportID)
	manifestPath := bucket.GetPath(manifestKey)
	reportDir := filepath.Dir(manifestPath)

	// Check if manifest exists
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return fmt.Errorf("manifest not found at %s\n"+
			"Run 'narwal inventory download' first to download the data", manifestPath)
	}

	// Read and parse the manifest
	log.Info("Reading inventory manifest", "manifest_path", manifestPath)

	manifestFile, err := os.Open(manifestPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open manifest file: %w", err)
	}
	defer manifestFile.Close() //nolint:errcheck

	var manifest inventory.InventoryManifest

	decoder := json.NewDecoder(manifestFile)

	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	log.Info("Parsed inventory manifest",
		"total_files", len(manifest.Files),
		"source_bucket", manifest.SourceBucket,
		"format", manifest.FileFormat)

	// Get parquet files from manifest and resolve their local paths
	log.Info("Resolving parquet files from manifest...")

	parquetFiles := make([]string, 0, len(manifest.Files))

	var (
		skippedFiles      int
		totalManifestSize int64
	)

	for i, file := range manifest.Files {
		if !strings.HasSuffix(file.Key, ".parquet") {
			skippedFiles++

			continue // Skip non-parquet files
		}

		totalManifestSize += file.Size

		// Resolve the local path in workarea
		localPath := bucket.GetPath(file.Key)

		// Check if the local file exists
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			log.Warn("Parquet file from manifest not found locally, skipping",
				"file", fmt.Sprintf("%d/%d", i+1, len(manifest.Files)),
				"key", file.Key,
				"size_mb", fmt.Sprintf("%.1f", float64(file.Size)/(1024*1024)))

			skippedFiles++

			continue
		}

		log.Debug("Found local parquet file",
			"file", fmt.Sprintf("%d/%d", i+1, len(manifest.Files)),
			"key", file.Key,
			"size_mb", fmt.Sprintf("%.1f", float64(file.Size)/(1024*1024)),
			"local_path", localPath)

		parquetFiles = append(parquetFiles, localPath)
	}

	if len(parquetFiles) == 0 {
		return fmt.Errorf("no parquet files from manifest found locally in %s\n"+
			"Run 'narwal inventory download' first to download the data", reportDir)
	}

	log.Info("Resolved inventory files for import",
		"manifest_files", len(manifest.Files),
		"parquet_files_found", len(parquetFiles),
		"skipped_files", skippedFiles,
		"total_data_size_gb", fmt.Sprintf("%.2f", float64(totalManifestSize)/(1024*1024*1024)))

	// Create bucket client for the cache bucket (where narinfos are stored)
	// using the existing Connect method from S3 config
	cacheBucketClient, err := fullCfg.S3.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to create cache bucket client: %w", err)
	}

	// Create workarea for narinfo downloads
	workArea, err := workarea.New(fullCfg.Workarea.Path)
	if err != nil {
		return fmt.Errorf("failed to create workarea: %w", err)
	}

	narinfoWorkareaBucket := workArea.Bucket(fullCfg.S3.Bucket, workarea.DefaultBucketConfig())

	// Import the parquet files
	return importParquetFiles(ctx, pgPool, cacheBucketClient, narinfoWorkareaBucket, parquetFiles)
}

func importParquetFiles(ctx context.Context, pgPool *pgxpool.Pool, cacheBucketClient *awssdk.BucketClient, narinfoWorkareaBucket *workarea.Bucket, parquetFiles []string) error {
	log.Info("Starting import of parquet files", "count", len(parquetFiles))

	var totalRecords int64

	startTime := time.Now()

	for i, parquetFile := range parquetFiles {
		fileStartTime := time.Now()

		log.Info("Processing parquet file",
			"file", filepath.Base(parquetFile),
			"progress", fmt.Sprintf("%d/%d", i+1, len(parquetFiles)))

		records, err := importSingleParquetFile(ctx, pgPool, cacheBucketClient, narinfoWorkareaBucket, parquetFile)
		if err != nil {
			return fmt.Errorf("failed to import %s: %w", parquetFile, err)
		}

		totalRecords += records
		fileElapsed := time.Since(fileStartTime)

		log.Info("Completed parquet file",
			"file", filepath.Base(parquetFile),
			"progress", fmt.Sprintf("%d/%d", i+1, len(parquetFiles)),
			"records", records,
			"duration", fileElapsed.Round(time.Second),
			"records_per_sec", fmt.Sprintf("%.0f", float64(records)/fileElapsed.Seconds()))
	}

	totalElapsed := time.Since(startTime)
	log.Info("Import completed successfully",
		"files_processed", len(parquetFiles),
		"total_records", totalRecords,
		"total_duration", totalElapsed.Round(time.Second),
		"avg_records_per_sec", fmt.Sprintf("%.0f", float64(totalRecords)/totalElapsed.Seconds()))

	return nil
}

// S3InventoryRecord represents a record in the S3 inventory parquet file.
// Based on the schema from the manifest: bucket, key, size, last_modified_date, e_tag, storage_class.
type S3InventoryRecord struct {
	Bucket           string `parquet:"bucket"`
	Key              string `parquet:"key"`
	Size             *int64 `parquet:"size"`               // Optional field
	LastModifiedDate *int64 `parquet:"last_modified_date"` // Optional timestamp in millis
	ETag             string `parquet:"e_tag"`              // Optional field
	StorageClass     string `parquet:"storage_class"`      // Optional field
}

func importSingleParquetFile(ctx context.Context, pgPool *pgxpool.Pool, cacheBucketClient *awssdk.BucketClient, narinfoWorkareaBucket *workarea.Bucket, parquetFile string) (int64, error) {
	fileName := filepath.Base(parquetFile)
	log.Info("Starting parquet file processing", "file", fileName)

	startTime := time.Now()

	// Open the parquet file
	log.Debug("Opening parquet file", "file", fileName)

	file, err := os.Open(parquetFile) //nolint:gosec
	if err != nil {
		return 0, fmt.Errorf("failed to open parquet file: %w", err)
	}
	defer file.Close() //nolint:errcheck

	// Create a parquet reader from the file
	log.Info("Creating parquet reader", "file", fileName)

	reader := parquet.NewGenericReader[S3InventoryRecord](file)
	defer reader.Close() //nolint:errcheck

	// Get a database connection
	log.Debug("Acquiring database connection", "file", fileName)

	conn, err := pgPool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)

	log.Info("Starting to read parquet records", "file", fileName)

	// Read records in batches
	const batchSize = 1000
	records := make([]S3InventoryRecord, batchSize)

	var (
		totalRecords     int64
		processedRecords int64
		skippedRecords   int64
		narinfoCount     int64
	)

	batchCount := 0
	lastProgressTime := time.Now()

	for {
		if batchCount == 0 {
			log.Info("Reading first batch from parquet file", "file", fileName)
		}

		n, err := reader.Read(records)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("failed to read parquet records: %w", err)
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
		for i := range n {
			record := records[i]
			totalRecords++

			// Log progress every 100 records within a batch to show activity
			if processedRecords > 0 && processedRecords%100 == 0 {
				recordKey := record.Key
				if len(recordKey) > 50 {
					recordKey = recordKey[:50] + "..."
				}

				log.Info("Processing records",
					"file", fileName,
					"batch", batchCount,
					"processed_so_far", processedRecords,
					"current_record", recordKey)
			}

			// Time-based progress logging every 30 seconds
			if time.Since(lastProgressTime) > 30*time.Second {
				elapsed := time.Since(startTime)
				rate := float64(processedRecords) / elapsed.Seconds()

				log.Info("Time-based progress update",
					"file", fileName,
					"processed_records", processedRecords,
					"elapsed", elapsed.Round(time.Second),
					"records_per_sec", fmt.Sprintf("%.1f", rate))

				lastProgressTime = time.Now()
			}

			// Skip records without a key or size
			if record.Key == "" || record.Size == nil {
				skippedRecords++
				continue
			}

			// Analyze the path to determine object type
			analysis, err := store.AnalyzePath(record.Key)
			if err != nil {
				log.Error("Failed to analyze path - this may indicate data incompatibility",
					"key", record.Key,
					"error", err,
					"file", fileName,
					"processed_so_far", processedRecords)

				return 0, fmt.Errorf("failed to analyze path %s: %w (processed %d records before failure)", record.Key, err, processedRecords)
			}

			// Generate hash from path (same as HTTP PUT)
			hash, err := store.HashFromPath(record.Key, analysis.ObjectType)
			if err != nil {
				log.Error("Failed to generate hash - this may indicate data incompatibility",
					"key", record.Key,
					"error", err,
					"object_type", analysis.ObjectType,
					"file", fileName,
					"processed_so_far", processedRecords)

				return 0, fmt.Errorf("failed to generate hash for %s (type: %s): %w (processed %d records before failure)",
					record.Key, analysis.ObjectType, err, processedRecords)
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
				return 0, fmt.Errorf("failed to insert object %s into database: %w", record.Key, err)
			}

			// If this is a narinfo file, download it and parse its metadata
			if analysis.ObjectType == db.ObjectTypeNarinfo {
				narinfoCount++

				log.Debug("Processing narinfo file", "key", record.Key)
				err = downloadAndProcessNarinfo(ctx, queries, cacheBucketClient, narinfoWorkareaBucket, record.Key, hash)
				if err != nil {
					log.Warn("Failed to download/process narinfo, continuing",
						"key", record.Key, "error", err)
					// Don't fail the entire import for narinfo processing errors
				} else {
					log.Debug("Successfully processed narinfo", "key", record.Key)
				}
			}

			processedRecords++
			batchProcessed++
		}

		// Log progress every 5 batches or if it's the first few batches
		if batchCount%5 == 0 || batchCount <= 3 {
			log.Info("Batch progress",
				"file", fileName,
				"batch", batchCount,
				"batch_processed", batchProcessed,
				"batch_skipped", n-batchProcessed,
				"total_processed", processedRecords,
				"total_read", totalRecords)
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	log.Debug("Parquet file processed",
		"file", filepath.Base(parquetFile),
		"total_records_read", totalRecords,
		"records_processed", processedRecords,
		"records_skipped", skippedRecords,
		"narinfo_files", narinfoCount,
		"batches", batchCount)

	return processedRecords, nil
}

// downloadAndProcessNarinfo downloads a narinfo file from S3 and processes
// its metadata.
func downloadAndProcessNarinfo(ctx context.Context, queries *db.Queries, cacheBucketClient *awssdk.BucketClient, narinfoWorkareaBucket *workarea.Bucket, narinfoKey, hash string) error {
	// Create adapter for the bucket client
	s3Client := workarea.NewBucketClientAdapter(cacheBucketClient)

	// Download the narinfo file to workarea
	err := narinfoWorkareaBucket.Download(ctx, s3Client, narinfoKey, nil)
	if err != nil {
		return fmt.Errorf("failed to download narinfo %s: %w", narinfoKey, err)
	}

	// Read the downloaded narinfo file
	narinfoPath := narinfoWorkareaBucket.GetPath(narinfoKey)
	narinfoData, err := os.ReadFile(narinfoPath)
	if err != nil {
		return fmt.Errorf("failed to read narinfo file %s: %w", narinfoPath, err)
	}

	// Parse and insert narinfo data using the public store.PutNarInfo function
	return store.PutNarInfo(ctx, queries, hash, narinfoData)
}
