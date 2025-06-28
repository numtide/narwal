package analyzepaths

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/parquet-go/parquet-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/store"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze-paths",
		Short: "Analyze all paths in parquet files to detect compatibility issues",
		Long: `Analyzes all object paths in the inventory parquet files using AnalyzePath
to detect potential data compatibility issues. This command reads the S3 inventory 
parquet files and runs path analysis on every entry to identify patterns that
might cause import failures.

The parquet files must already be downloaded using 'narwal inventory download'.

This is useful for:
- Pre-validating inventory data before import
- Identifying problematic object patterns
- Understanding data compatibility issues`,
		Example: `  # Analyze paths for a specific report
  narwal inventory analyze-paths --bucket nix-cache-inventory --report 2025-06-07T01-00Z

  # Use custom workarea and parallel processing
  narwal inventory analyze-paths --bucket nix-cache-inventory --report 2025-06-07T01-00Z \
    --workarea.path /tmp/my-cache --parallel 16

  # Write errors to a custom file
  narwal inventory analyze-paths --bucket nix-cache-inventory --report 2025-06-07T01-00Z \
    --error-file /tmp/path-errors.txt`,
		RunE: runE,
	}

	// Add inventory-specific flags
	appconfig.SetInventoryFlags(cmd.Flags())

	// Add parallelism flag
	cmd.Flags().Int("parallel", 20, "Number of parallel workers")

	// Add error output file flag
	cmd.Flags().String("error-file", "./analyze-paths-errors.txt", "File to write path analysis errors to")

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

	// Bind flags to viper using shared utility
	appconfig.BindInventoryFlagsToViper(cmd)

	// parse viper into our config object
	var fullCfg appconfig.Config
	if err := appconfig.FromViper(viper.GetViper(), &fullCfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	cfg := &fullCfg.Inventory
	if err := cfg.Validate(ctx, nil, fullCfg.Workarea.Path); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Require report ID for analyze-paths command
	if cfg.ReportID == "" {
		return errors.New("report ID is required for analyze-paths command (use --report flag)")
	}

	parallelism := viper.GetInt("parallel")
	if parallelism <= 0 {
		parallelism = 20
	}

	errorFilePath := viper.GetString("error-file")
	if errorFilePath == "" {
		errorFilePath = "./analyze-paths-errors.txt"
	}

	log.Info("Starting path analysis from inventory",
		"report_id", cfg.ReportID,
		"bucket", cfg.Bucket,
		"prefix", cfg.Prefix,
		"parallelism", parallelism,
		"error_file", errorFilePath)

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

	log.Info("Resolved inventory files for path analysis",
		"manifest_files", len(manifest.Files),
		"parquet_files_found", len(parquetFiles),
		"skipped_files", skippedFiles,
		"total_data_size_gb", fmt.Sprintf("%.2f", float64(totalManifestSize)/(1024*1024*1024)))

	// Analyze all paths in the parquet files
	return analyzePathsInParquetFiles(ctx, parquetFiles, parallelism, errorFilePath)
}

// PathAnalysisError contains information about a failed path analysis.
type PathAnalysisError struct {
	Path  string
	Error string
}

// ParquetAnalysisResult holds the result of analyzing a single parquet file.
type ParquetAnalysisResult struct {
	FileName        string
	ProcessedCount  int64
	ErrorCount      int64
	Errors          []PathAnalysisError
	ProcessingError error
}

func analyzePathsInParquetFiles(
	ctx context.Context,
	parquetFiles []string,
	parallelism int,
	errorFilePath string,
) error {
	startTime := time.Now()

	// Use a reasonable number of workers for parquet processing
	parquetWorkers := parallelism / 4
	parquetWorkers = max(parquetWorkers, 1)
	parquetWorkers = min(parquetWorkers, 8)

	log.Info("Starting parallel path analysis",
		"total_files", len(parquetFiles),
		"parquet_workers", parquetWorkers)

	// Create channels for work distribution
	filesChan := make(chan string, parquetWorkers*2)
	resultsChan := make(chan ParquetAnalysisResult, len(parquetFiles))

	var wg sync.WaitGroup

	// Start parquet processing workers
	for i := range parquetWorkers {
		wg.Add(1)

		go func(workerID int) {
			defer wg.Done()

			for parquetFile := range filesChan {
				log.Info("Analyzing parquet file",
					"file", filepath.Base(parquetFile),
					"worker", workerID)

				result := analyzePathsInSingleParquet(parquetFile)
				result.FileName = filepath.Base(parquetFile)

				select {
				case resultsChan <- result:
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}

	// Send all parquet files to workers
	go func() {
		defer close(filesChan)

		for _, parquetFile := range parquetFiles {
			select {
			case filesChan <- parquetFile:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Collect results as they come in
	var (
		totalProcessedCount int64
		totalErrorCount     int64
		allErrors           []PathAnalysisError
		processedFiles      int
	)

	// Wait for results from all files
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	for result := range resultsChan {
		processedFiles++

		if result.ProcessingError != nil {
			return fmt.Errorf("failed to analyze parquet file %s: %w", result.FileName, result.ProcessingError)
		}

		totalProcessedCount += result.ProcessedCount
		totalErrorCount += result.ErrorCount
		allErrors = append(allErrors, result.Errors...)

		log.Info("Completed parquet file analysis",
			"file", result.FileName,
			"progress", fmt.Sprintf("%d/%d", processedFiles, len(parquetFiles)),
			"processed", result.ProcessedCount,
			"errors", result.ErrorCount,
			"total_processed", totalProcessedCount,
			"total_errors", totalErrorCount)
	}

	analysisElapsed := time.Since(startTime)

	log.Info("Completed path analysis",
		"files_processed", processedFiles,
		"total_processed", totalProcessedCount,
		"total_errors", totalErrorCount,
		"error_rate_pct", fmt.Sprintf("%.2f", float64(totalErrorCount)/float64(totalProcessedCount)*100),
		"analysis_duration", analysisElapsed.Round(time.Second))

	// Write errors to file and display summary
	if totalErrorCount > 0 {
		log.Warn("Path analysis found compatibility issues", "total_errors", totalErrorCount)

		// Write all errors to file
		log.Info("Writing errors to file", "error_file", errorFilePath)

		if err := writeErrorsToFile(allErrors, errorFilePath); err != nil {
			log.Error("Failed to write errors to file", "error", err, "file", errorFilePath)
			return fmt.Errorf("failed to write errors to file %s: %w", errorFilePath, err)
		}

		// Group errors by error message for summary
		errorGroups := make(map[string][]string)
		for _, pathErr := range allErrors {
			errorGroups[pathErr.Error] = append(errorGroups[pathErr.Error], pathErr.Path)
		}

		log.Info("Error summary by type (detailed errors written to file)",
			"error_file", errorFilePath)

		for errMsg, paths := range errorGroups {
			samplePaths := paths
			if len(samplePaths) > 5 {
				samplePaths = samplePaths[:5]
			}

			log.Info("Error type",
				"error", errMsg,
				"count", len(paths),
				"sample_paths", samplePaths)
		}

		log.Info("All errors written to file", "error_file", errorFilePath, "total_errors", totalErrorCount)
	} else {
		log.Info("All paths analyzed successfully - no compatibility issues found")
	}

	return nil
}

func analyzePathsInSingleParquet(parquetFile string) ParquetAnalysisResult {
	fileName := filepath.Base(parquetFile)

	// Open the parquet file
	file, err := os.Open(parquetFile) //nolint:gosec
	if err != nil {
		return ParquetAnalysisResult{
			ProcessingError: fmt.Errorf("failed to open parquet file: %w", err),
		}
	}
	defer file.Close() //nolint:errcheck

	// Create a parquet reader from the file
	reader := parquet.NewGenericReader[inventory.S3InventoryRecord](file)
	defer reader.Close() //nolint:errcheck

	// Read records in batches
	const batchSize = 1000

	records := make([]inventory.S3InventoryRecord, batchSize)

	var (
		processedCount int64
		errorCount     int64
		pathErrors     []PathAnalysisError
	)

	for {
		n, err := reader.Read(records)
		if err != nil && !errors.Is(err, io.EOF) {
			return ParquetAnalysisResult{
				ProcessingError: fmt.Errorf("failed to read parquet records: %w", err),
			}
		}

		if n == 0 {
			break
		}

		// Process the batch of records
		for i := range n {
			record := records[i]

			// Skip records without a key
			if record.Key == "" {
				continue
			}

			processedCount++

			// Analyze the path
			_, err := store.AnalyzePath(record.Key)
			if err != nil {
				errorCount++

				pathErrors = append(pathErrors, PathAnalysisError{
					Path:  record.Key,
					Error: err.Error(),
				})

				// Log progress every 100 errors to show we're still working
				if errorCount%100 == 0 {
					log.Debug("Path analysis progress",
						"file", fileName,
						"processed", processedCount,
						"errors", errorCount)
				}
			}

			// Log progress every 10000 records to show activity
			if processedCount%10000 == 0 {
				log.Debug("Processing progress",
					"file", fileName,
					"processed", processedCount,
					"errors", errorCount)
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	log.Debug("Completed single parquet analysis",
		"file", fileName,
		"processed", processedCount,
		"errors", errorCount)

	return ParquetAnalysisResult{
		ProcessedCount: processedCount,
		ErrorCount:     errorCount,
		Errors:         pathErrors,
	}
}

// writeErrorsToFile writes all path analysis errors to a file directly.
func writeErrorsToFile(errors []PathAnalysisError, filePath string) error {
	file, err := os.Create(filePath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to create error file: %w", err)
	}
	defer file.Close() //nolint:errcheck

	// Write errors directly
	for _, pathErr := range errors {
		line := fmt.Sprintf("%s: %s\n", pathErr.Path, pathErr.Error)
		if _, err := file.WriteString(line); err != nil {
			return fmt.Errorf("failed to write error line: %w", err)
		}
	}

	return nil
}
