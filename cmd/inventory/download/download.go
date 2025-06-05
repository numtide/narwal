package download

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download parquet files for a specific report ID",
		Long: `Downloads all parquet files for a specific inventory report ID using the manifest
stored in the workarea. The manifest must be downloaded first using the get-manifest command.
Files are downloaded in parallel and cached in the workarea.`,
		Example: `  # Download files for specific report
  narwal inventory download --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

  # Use custom workarea directory
  narwal inventory download --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z --workdir /tmp/cache

  # Download with custom parallelism
  narwal inventory download --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z --parallel 10`,
		RunE: runE,
	}

	// Add flags
	appconfig.SetInventoryFlags(cmd.Flags())
	cmd.Flags().Int("parallel", 5, "Number of parallel downloads")

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
	if workdirFlag := cmd.Flag("workdir"); workdirFlag != nil && workdirFlag.Changed {
		viper.Set("workdir", workdirFlag.Value.String())
	}
	if parallelFlag := cmd.Flag("parallel"); parallelFlag != nil && parallelFlag.Changed {
		viper.Set("parallel", parallelFlag.Value.String())
	}

	// Load default AWS configuration
	awscfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("error loading AWS config: %w", err)
	}

	// parse viper into our config object
	var cfg *appconfig.Inventory

	if err := appconfig.FromViper(viper.GetViper(), &cfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err := cfg.Validate(ctx, awscfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	if cfg.ReportID == "" {
		return fmt.Errorf("report ID is required for download command")
	}

	parallelism := viper.GetInt("parallel")
	if parallelism <= 0 {
		parallelism = 5
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())
	log.Info("Accessing S3 bucket", "bucket", cfg.Bucket, "prefix", cfg.Prefix, "region", cfg.BucketRegion)
	log.Info("Using work directory", "workdir", cfg.Workarea.GetBasePath())

	// Load manifest from workarea
	manifestPath := filepath.Join(cfg.Workarea.GetBasePath(), "manifests", cfg.Bucket, cfg.ReportID, "manifest.json")
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to open manifest file (run get-manifest first): %w", err)
	}
	defer manifestFile.Close()

	var manifest inventory.InventoryManifest
	decoder := json.NewDecoder(manifestFile)
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	log.Info("Loaded manifest", "files", len(manifest.Files), "totalSize", formatBytes(manifest.TotalSize()))

	// Create a new S3 client with the correct region
	regionCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.BucketRegion),
		awsconfig.WithRetryMaxAttempts(5),
	)
	if err != nil {
		return fmt.Errorf("error loading AWS config with region: %w", err)
	}

	s3Client := s3.NewFromConfig(regionCfg)
	bucket := cfg.Workarea.Bucket(cfg.Bucket)

	// Create progress tracker
	progressTracker := &OverallProgress{
		StartTime:  time.Now(),
		TotalFiles: len(manifest.Files),
		TotalSize:  manifest.TotalSize(),
	}

	log.Info("Starting parallel download", "files", len(manifest.Files), "parallel", parallelism)

	// Create worker pool for parallel downloads
	fileChan := make(chan inventory.InventoryManifestInfo, len(manifest.Files))
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for file := range fileChan {
				if ctx.Err() != nil {
					log.Warn("Download cancelled", "worker", workerID)
					return
				}

				log.Debug("Downloading file", "worker", workerID, "key", file.Key)

				err := bucket.Download(ctx, s3Client, file.Key, func(bucket, key string, downloaded, total int64) {
					progressTracker.OnFileDownloadProgress(key, downloaded, total)
				})
				if err != nil {
					log.Error("Failed to download file", "worker", workerID, "key", file.Key, "error", err)
					continue
				}

				progressTracker.OnFileDownloadCompleted(file.Key, file.Size)
			}
		}(i)
	}

	// Send files to workers
	go func() {
		defer close(fileChan)
		for _, file := range manifest.Files {
			select {
			case fileChan <- file:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for all downloads to complete
	wg.Wait()

	if ctx.Err() != nil {
		return fmt.Errorf("download cancelled: %w", ctx.Err())
	}

	elapsed := time.Since(progressTracker.StartTime)
	log.Info("Download completed", "elapsed", elapsed.Round(time.Second))

	return nil
}

// OverallProgress tracks overall download progress.
type OverallProgress struct {
	TotalFiles      int
	FilesCompleted  int
	TotalSize       int64
	BytesDownloaded int64
	StartTime       time.Time
	mu              sync.RWMutex
	lastLogTime     time.Time
}

// OnFileDownloadProgress logs download progress.
func (op *OverallProgress) OnFileDownloadProgress(key string, downloaded int64, total int64) {
	op.mu.Lock()
	defer op.mu.Unlock()

	// Log progress every 5 seconds
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

// OnFileDownloadCompleted logs when a file download completes.
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
	for i := 0; i < width; i++ {
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
