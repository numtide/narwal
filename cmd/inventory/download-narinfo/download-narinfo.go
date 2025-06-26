package downloadnarinfo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/parquet-go/parquet-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/store"
	"github.com/numtide/narwal/pkg/workarea"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download-narinfo",
		Short: "Download all narinfo files from cache bucket based on inventory parquet data",
		Long: `Downloads all narinfo files from the cache bucket based on the inventory parquet data.
This command reads the S3 inventory parquet files, identifies all narinfo files, and downloads
them to the local workarea for offline analysis or subsequent processing.

The parquet files must already be downloaded using 'narwal inventory download'.

This is useful for:
- Offline analysis of narinfo metadata
- Preparing data for bulk import operations
- Creating local mirrors of narinfo data`,
		Example: `  # Download narinfos for a specific report
  narwal inventory download-narinfo --bucket nix-cache-inventory --report 2025-06-07T01-00Z

  # Use custom workarea and parallel downloads
  narwal inventory download-narinfo --bucket nix-cache-inventory --report 2025-06-07T01-00Z \
    --workarea.path /tmp/my-cache --parallel 32`,
		RunE: runE,
	}

	// Add inventory-specific flags
	appconfig.SetInventoryFlags(cmd.Flags())

	// Add parallelism flag
	cmd.Flags().Int("parallel", 20, "Number of parallel downloads")

	// Add profiling flags
	cmd.Flags().String("cpuprofile", "", "Write CPU profile to file")
	cmd.Flags().String("memprofile", "", "Write memory profile to file")

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

	// Setup CPU profiling if requested
	cpuProfile := viper.GetString("cpuprofile")
	if cpuProfile != "" {
		f, err := os.Create(cpuProfile) //nolint:gosec
		if err != nil {
			return fmt.Errorf("failed to create CPU profile file: %w", err)
		}
		defer f.Close() //nolint:errcheck

		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("failed to start CPU profile: %w", err)
		}
		defer pprof.StopCPUProfile()

		log.Info("CPU profiling enabled", "file", cpuProfile)
	}

	// Parse viper into our config object
	var fullCfg appconfig.Config
	if err := appconfig.FromViper(viper.GetViper(), &fullCfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	cfg := &fullCfg.Inventory
	if err := cfg.Validate(ctx, nil, fullCfg.Workarea.Path); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Require report ID for download-narinfo command
	if cfg.ReportID == "" {
		return errors.New("report ID is required for download-narinfo command (use --report flag)")
	}

	parallelism := viper.GetInt("parallel")
	if parallelism <= 0 {
		parallelism = 20
	}

	log.Info("Starting narinfo download from inventory",
		"report_id", cfg.ReportID,
		"bucket", cfg.Bucket,
		"prefix", cfg.Prefix,
		"parallelism", parallelism)

	// Get workarea bucket to check for local manifest
	bucket := cfg.Workarea.Bucket(cfg.Bucket, inventory.BucketConfig())
	manifestKey := fmt.Sprintf("%s%s/manifest.json", cfg.Prefix, cfg.ReportID)
	manifestPath := bucket.GetPath(manifestKey)

	// Check if manifest exists locally
	if !bucket.Exists(manifestKey) {
		return fmt.Errorf("manifest not found locally at %s\n"+
			"Run 'narwal inventory download' first to download the data", manifestPath)
	}

	// Read and parse the manifest from local workarea
	manifestFile, err := os.Open(manifestPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open manifest file: %w", err)
	}
	defer manifestFile.Close() //nolint:errcheck

	manifest, err := inventory.ReadManifest(manifestFile)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	log.Info("Processing inventory manifest", "total_files", len(manifest.Files))

	// Get local paths for all manifest files (all should be parquet files)
	parquetFiles := make([]string, len(manifest.Files))

	for i, file := range manifest.Files {
		localPath := bucket.GetPath(file.Key)
		if _, err := os.Stat(localPath); err != nil {
			return fmt.Errorf("parquet file not found locally: %s\n"+
				"Run 'narwal inventory download' first to download the data", localPath)
		}

		parquetFiles[i] = localPath
	}

	log.Info("Processing parquet files", "count", len(parquetFiles))

	// Create workarea for narinfo downloads
	workArea, err := workarea.New(fullCfg.Workarea.Path)
	if err != nil {
		return fmt.Errorf("failed to create workarea: %w", err)
	}

	narinfoWorkareaBucket := workArea.Bucket(fullCfg.S3.Bucket, workarea.DefaultBucketConfig())

	// Process parquet files and download narinfos directly
	err = processAndDownloadNarinfos(ctx, &fullCfg.S3, narinfoWorkareaBucket, parquetFiles, parallelism)

	// Write memory profile if requested
	memProfile := viper.GetString("memprofile")
	if memProfile != "" {
		writeMemoryProfile(memProfile)
	}

	return err
}

func writeMemoryProfile(memProfile string) {
	f, memErr := os.Create(memProfile) //nolint:gosec
	if memErr != nil {
		log.Error("Failed to create memory profile file", "error", memErr)
		return
	}
	defer f.Close() //nolint:errcheck

	if memErr := pprof.WriteHeapProfile(f); memErr != nil {
		log.Error("Failed to write memory profile", "error", memErr)
	} else {
		log.Info("Memory profile written", "file", memProfile)
	}
}


func processAndDownloadNarinfos(
	ctx context.Context,
	s3Config *appconfig.S3,
	narinfoWorkareaBucket *workarea.Bucket,
	parquetFiles []string,
	parallelism int,
) error {
	log.Info("Starting narinfo processing and download", "parquet_files", len(parquetFiles), "parallelism", parallelism)

	// Statistics tracking with atomic counters
	var s stats

	// Setup channels and workers
	rawNarinfoChan := make(chan string, parallelism*1000)
	filteredNarinfoChan := make(chan string, parallelism*1000)
	stopReporter := make(chan struct{})
	reporterDone := make(chan struct{})

	// Start all workers
	var wg sync.WaitGroup

	startFilterWorker(ctx, &wg, &s, rawNarinfoChan, filteredNarinfoChan, narinfoWorkareaBucket)
	startDownloadWorkers(ctx, &wg, &s, s3Config, filteredNarinfoChan, narinfoWorkareaBucket, parallelism)
	startProgressReporter(&s, stopReporter, reporterDone)

	// Process parquet files in parallel and wait for completion
	if err := processParquetFilesParallel(ctx, parquetFiles, rawNarinfoChan); err != nil {
		return err
	}

	// Wait for all workers to complete
	wg.Wait()

	// Stop the progress reporter and display final statistics
	close(stopReporter)
	<-reporterDone

	log.Info("Narinfo processing completed",
		"total_narinfos", s.totalNarinfos.Load(),
		"already_downloaded", s.alreadyDownloaded.Load(),
		"newly_downloaded", s.downloaded.Load(),
		"failed", s.failed.Load())

	return nil
}

// stats tracks narinfo processing statistics with atomic counters.
type stats struct {
	totalNarinfos     atomic.Int64
	alreadyDownloaded atomic.Int64
	downloaded        atomic.Int64
	failed            atomic.Int64
}

func startFilterWorker(
	ctx context.Context,
	wg *sync.WaitGroup,
	s *stats,
	rawNarinfoChan <-chan string,
	filteredNarinfoChan chan<- string,
	narinfoWorkareaBucket *workarea.Bucket,
) {
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer close(filteredNarinfoChan)

		for key := range rawNarinfoChan {
			select {
			case <-ctx.Done():
				log.Info("Filter worker stopping due to context cancellation")
				return
			default:
			}

			s.totalNarinfos.Add(1)

			if narinfoWorkareaBucket.Exists(key) {
				s.alreadyDownloaded.Add(1)
			} else {
				select {
				case filteredNarinfoChan <- key:
				case <-ctx.Done():
					log.Info("Filter worker stopping due to context cancellation")
					return
				}
			}
		}
	}()
}

func startDownloadWorkers(
	ctx context.Context,
	wg *sync.WaitGroup,
	s *stats,
	s3Config *appconfig.S3,
	filteredNarinfoChan <-chan string,
	narinfoWorkareaBucket *workarea.Bucket,
	parallelism int,
) {
	for i := range parallelism {
		wg.Add(1)

		go func(workerID int) {
			defer wg.Done()

			// Create a dedicated S3 client for this worker
			// This ensures each worker has its own HTTP client/transport for optimal HTTP/2 multiplexing
			workerBucketClient, err := s3Config.Connect(ctx)
			if err != nil {
				log.Error("Failed to create S3 client for worker", "worker", workerID, "error", err)
				return
			}

			workerS3Client := workarea.NewBucketClientAdapter(workerBucketClient)

			for key := range filteredNarinfoChan {
				select {
				case <-ctx.Done():
					log.Info("Worker stopping due to context cancellation", "worker", workerID)
					return
				default:
				}

				if err := narinfoWorkareaBucket.Download(ctx, workerS3Client, key, nil); err != nil {
					if ctx.Err() != nil {
						log.Info("Download cancelled", "worker", workerID, "key", key)
						return
					}
					log.Error("Failed to download narinfo", "worker", workerID, "key", key, "error", err)
					s.failed.Add(1)
				} else {
					s.downloaded.Add(1)
				}
			}
		}(i)
	}
}

func startProgressReporter(
	s *stats,
	stopReporter chan struct{},
	reporterDone chan struct{},
) {
	go func() {
		defer close(reporterDone)

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		startTime := time.Now()

		for {
			select {
			case <-ticker.C:
				total := s.totalNarinfos.Load()
				alreadyDL := s.alreadyDownloaded.Load()
				newDL := s.downloaded.Load()
				failed := s.failed.Load()

				if total > 0 {
					elapsed := time.Since(startTime)
					downloadAttempted := newDL + failed
					rate := float64(downloadAttempted) / elapsed.Seconds()

					log.Info("Progress update",
						"elapsed", elapsed.Round(time.Second),
						"total_processed", total,
						"download_rate", fmt.Sprintf("%.0f/s", rate),
						"already_downloaded", alreadyDL,
						"newly_downloaded", newDL,
						"failed", failed,
						"pending", total-alreadyDL-newDL-failed)
				}
			case <-stopReporter:
				return
			}
		}
	}()
}

func processParquetFilesParallel(ctx context.Context, parquetFiles []string, rawNarinfoChan chan<- string) error {
	log.Info("Processing parquet files sequentially", "count", len(parquetFiles))

	defer close(rawNarinfoChan)

	for i, parquetFile := range parquetFiles {
		if err := processParquetFile(ctx, parquetFile, rawNarinfoChan); err != nil {
			return fmt.Errorf("failed to process parquet file %s: %w", filepath.Base(parquetFile), err)
		}
		log.Info("Completed parquet file", "file", filepath.Base(parquetFile), "progress", fmt.Sprintf("%d/%d", i+1, len(parquetFiles)))
	}

	return nil
}

func processParquetFile(ctx context.Context, parquetFile string, narinfoChan chan<- string) error {
	file, err := os.Open(parquetFile) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open parquet file: %w", err)
	}
	defer file.Close() //nolint:errcheck

	reader := parquet.NewGenericReader[inventory.S3InventoryRecord](file)
	defer reader.Close() //nolint:errcheck

	const batchSize = 1000
	records := make([]inventory.S3InventoryRecord, batchSize)

	for {
		n, err := reader.Read(records)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("failed to read parquet records: %w", err)
		}

		if n == 0 {
			break
		}

		for i := range n {
			select {
			case <-ctx.Done():
				log.Info("Parquet file processing cancelled", "file", filepath.Base(parquetFile))
				return ctx.Err()
			default:
			}

			record := records[i]
			if record.Key == "" || record.Size == nil {
				continue
			}

			if analysis, err := store.AnalyzePath(record.Key); err == nil && analysis.ObjectType == db.ObjectTypeNarinfo {
				select {
				case narinfoChan <- record.Key:
				case <-ctx.Done():
					log.Info("Parquet file processing cancelled", "file", filepath.Base(parquetFile))
					return ctx.Err()
				}
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return nil
}
