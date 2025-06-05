// Package importer provides functionality to import parquet files from S3 buckets
package importer

import (
	"errors"
	"fmt"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCmd() *cobra.Command {
	// create the command
	cmd := &cobra.Command{
		Use:   "importer",
		Short: "Stream and process inventory data from S3 with local caching",
		Example: `  # Process latest available inventory data
  narwal importer --bucket nix-cache-inventory --prefix data/

  # Process specific inventory report
  narwal importer --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

  # Use custom cache directory
  narwal importer --bucket nix-cache-inventory --prefix data/ --workdir /tmp/cache

  # Specify bucket region to skip auto-detection
  narwal importer --bucket nix-cache-inventory --bucket-region us-east-1 --prefix data/

  # Skip processing file contents, only download files
  narwal importer --bucket nix-cache-inventory --prefix data/ --skip-processing`,
		RunE: runE,
	}

	// add our config flags to the command's flag set
	appconfig.SetImporterFlags(cmd.Flags())

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

	// Load default AWS configuration
	awscfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("error loading AWS config: %w", err)
	}

	// parse viper into our config object
	var cfg *appconfig.Importer

	if err := appconfig.FromViper(viper.GetViper(), &cfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err := cfg.Validate(ctx, awscfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())

	log.Info("Accessing S3 bucket", "bucket", cfg.Bucket, "prefix", cfg.Prefix, "region", cfg.BucketRegion)

	log.Info("Using work directory", "workdir", cfg.Workdir)

	// Create a new S3 client with the correct region
	regionCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.BucketRegion),
		// Add retry behavior for transient errors
		awsconfig.WithRetryMaxAttempts(5),
	)
	if err != nil {
		return fmt.Errorf("error loading AWS config with region: %w", err)
	}

	s3Client := s3.NewFromConfig(regionCfg)

	// Create inventory client with working directory support
	inventoryClient, err := inventory.NewClient(s3Client, cfg.Bucket, cfg.Prefix, cfg.Workdir)
	if err != nil {
		return fmt.Errorf("error creating inventory client: %w", err)
	}

	reportID := cfg.ReportID
	if reportID == "" {
		reports, err := inventoryClient.GetReports(ctx)
		if err != nil {
			return fmt.Errorf("error getting available reports: %w", err)
		}

		if len(reports) == 0 {
			return errors.New("no inventory reports found in bucket")
		}

		reportID = reports[len(reports)-1] // Get the latest report (lexicographically last)
		log.Info("Found latest inventory report", "report", reportID)
	}

	log.Info("Processing data for report", "report", reportID)

	// Get manifest info
	manifest, err := inventoryClient.GetManifest(ctx, reportID)
	if err != nil {
		return fmt.Errorf("error getting manifest: %w", err)
	}

	log.Info("Found parquet files in manifest", "count", len(manifest.Files))
	totalSize := manifest.TotalSize()
	log.Info("Total download size", "size", formatBytes(totalSize))

	// Create progress tracker for overall progress
	progressTracker := &OverallProgress{
		StartTime:  time.Now(),
		TotalFiles: len(manifest.Files),
		TotalSize:  totalSize,
	}

	log.Info("Starting inventory processing", "files", len(manifest.Files), "totalSize", formatBytes(totalSize))

	if cfg.SkipProcessing {
		log.Info("Parquet files will be downloaded only (content processing skipped)")
	} else {
		log.Info("Parquet files will be downloaded and cached for efficient processing")
	}

	objects := make([]inventory.InventoryObject, 1000) // Fixed array for reading

	// Main processing loop - download and optionally process each file
	for i, file := range manifest.Files {
		if ctx.Err() != nil {
			log.Warn("Processing cancelled by user")
			return fmt.Errorf("processing cancelled by user: %w", ctx.Err())
		}

		log.Info("Processing file", "file", i+1, "total", len(manifest.Files), "key", file.Key)

		// Download the file (with caching) and get parquet reader
		reader, err := inventoryClient.DownloadFile(ctx, file, func(key string, downloaded int64, total int64) {
			progressTracker.OnFileDownloadProgress(key, downloaded, total)
		})
		if err != nil {
			return fmt.Errorf("error downloading file %s: %w", file.Key, err)
		}

		progressTracker.OnFileDownloadCompleted(file.Key, file.Size)

		if cfg.SkipProcessing {
			log.Info("File downloaded (content processing skipped)", "key", file.Key)

			// Skip content processing, just close the reader
			_ = reader.Close()

			continue
		}

		// Process all objects in this file
		for {
			if ctx.Err() != nil {
				_ = reader.Close()
				return fmt.Errorf("processing cancelled by user: %w", ctx.Err())
			}

			n, err := reader.Read(objects)
			if err != nil {
				_ = reader.Close()
				return fmt.Errorf("error reading from parquet file %s: %w", file.Key, err)
			}

			if n == 0 {
				break // EOF
			}

			// Print each object to stdout
			for j := range n {
				fmt.Printf("%+v\n", objects[j])
			}
		}

		_ = reader.Close()
	}

	elapsed := time.Since(progressTracker.StartTime)
	log.Info("Processing completed", "elapsed", elapsed.Round(time.Second))
	log.Info("Cached files retained for efficient future runs", "workDir", cfg.Workdir)

	return nil
}

// OverallProgress tracks overall download progress and implements ProgressTracker.
type OverallProgress struct {
	TotalFiles      int
	FilesCompleted  int
	TotalSize       int64
	BytesDownloaded int64
	StartTime       time.Time
	mu              sync.RWMutex
	lastLogTime     time.Time
}

// OnFileDownloadStarted logs when a file download starts.
func (op *OverallProgress) OnFileDownloadStarted(key string, size int64) {
	log.Info("Starting download", "key", key, "size", formatBytes(size))
}

// OnFileDownloadProgress logs download progress.
func (op *OverallProgress) OnFileDownloadProgress(key string, downloaded int64, total int64) {
	op.mu.Lock()
	defer op.mu.Unlock()

	// Log progress every 5 seconds or every 25MB
	now := time.Now()

	const timeInterval = 5 * time.Second

	shouldLog := now.Sub(op.lastLogTime) >= timeInterval

	if shouldLog && total > 0 {
		percent := float64(downloaded) / float64(total) * 100
		progressBar := createProgressBar(percent, 30)
		elapsed := now.Sub(op.StartTime)

		var speed string

		if elapsed.Seconds() > 0 {
			bytesPerSecond := float64(downloaded) / elapsed.Seconds()
			speed = fmt.Sprintf(" | %s/s", formatBytes(int64(bytesPerSecond)))
		}

		log.Info("Download progress",
			"key", key,
			"progress", fmt.Sprintf("%s %.1f%%", progressBar, percent),
			"downloaded", fmt.Sprintf("%s/%s", formatBytes(downloaded), formatBytes(total)),
			"elapsed", elapsed.Round(time.Second).String()+speed)

		op.lastLogTime = now
	}
}

// OnFileDownloadCompleted logs when a file download completes and updates overall progress.
func (op *OverallProgress) OnFileDownloadCompleted(key string, size int64) {
	op.mu.Lock()
	defer op.mu.Unlock()

	op.FilesCompleted++
	op.BytesDownloaded += size

	filesPercent := float64(op.FilesCompleted) / float64(op.TotalFiles) * 100
	bytesPercent := float64(op.BytesDownloaded) / float64(op.TotalSize) * 100
	elapsed := time.Since(op.StartTime)

	var avgSpeed string

	if elapsed.Seconds() > 0 {
		bytesPerSecond := float64(op.BytesDownloaded) / elapsed.Seconds()
		avgSpeed = fmt.Sprintf(" | %s/s avg", formatBytes(int64(bytesPerSecond)))
	}

	overallBytes := fmt.Sprintf("%s/%s (%.1f%%)", formatBytes(op.BytesDownloaded), formatBytes(op.TotalSize), bytesPercent)

	log.Info("File download completed",
		"key", key,
		"overallFiles", fmt.Sprintf("%d/%d (%.1f%%)", op.FilesCompleted, op.TotalFiles, filesPercent),
		"overallBytes", overallBytes,
		"elapsed", elapsed.Round(time.Second).String()+avgSpeed)
}

// createProgressBar creates a visual progress bar.
func createProgressBar(percent float64, width int) string {
	filled := int(percent / 100.0 * float64(width))
	if filled > width {
		filled = width
	}

	bar := "["

	for i := range width {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}

	bar += "]"

	return bar
}

// formatBytes formats byte count in human readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
