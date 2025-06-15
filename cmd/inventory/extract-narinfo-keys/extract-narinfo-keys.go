package extractnarinfokeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/parquet-go/parquet-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/store"
)

// progressMsg represents a progress update
type progressMsg struct {
	filesProcessed int
	totalFiles     int
	keysExtracted  int
}

// progressModel represents the progress bar model
type progressModel struct {
	progress       progress.Model
	filesProcessed int
	totalFiles     int
	keysExtracted  int
	done           bool
}

func (m progressModel) Init() tea.Cmd {
	return nil
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressMsg:
		m.filesProcessed = msg.filesProcessed
		m.totalFiles = msg.totalFiles
		m.keysExtracted = msg.keysExtracted
		if m.filesProcessed >= m.totalFiles {
			m.done = true
		}
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	progressModel, progressCmd := m.progress.Update(msg)
	if p, ok := progressModel.(progress.Model); ok {
		m.progress = p
	}
	cmd = tea.Batch(cmd, progressCmd)
	return m, cmd
}

func (m progressModel) View() string {
	if m.done {
		return fmt.Sprintf("✅ Completed! Processed %d files, extracted %d keys\n", m.totalFiles, m.keysExtracted)
	}

	percent := 0.0
	if m.totalFiles > 0 {
		percent = float64(m.filesProcessed) / float64(m.totalFiles)
	}

	return fmt.Sprintf(
		"Processing files... %s %d/%d (%d keys extracted)\n",
		m.progress.ViewAs(percent),
		m.filesProcessed,
		m.totalFiles,
		m.keysExtracted,
	)
}

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract-narinfo-keys",
		Short: "Extract and sort all narinfo keys from parquet files",
		Long: `Reads all parquet files in parallel, extracts narinfos, sorts their hash keys 
in memory, and writes them as text (one key per line) to a file.

The parquet files must already be downloaded using 'narwal inventory download'.

This command is optimized for parallel processing and efficient memory usage.`,
		Example: `  # Extract narinfo keys for a specific report
  narwal inventory extract-narinfo-keys --bucket nix-cache-inventory --report 2025-06-07T01-00Z

  # Use custom output file and parallel workers
  narwal inventory extract-narinfo-keys --bucket nix-cache-inventory --report 2025-06-07T01-00Z \
    --output keys.txt --parallel 32`,
		RunE: runE,
	}

	// Add inventory-specific flags
	appconfig.SetInventoryFlags(cmd.Flags())

	// Add command-specific flags
	cmd.Flags().String("output", "narinfo-keys.txt", "Output file for sorted keys")
	cmd.Flags().Int("parallel", runtime.NumCPU(), "Number of parallel workers")

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

	// Explicitly set flag values in viper
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

	// Require report ID
	if cfg.ReportID == "" {
		return errors.New("report ID is required (use --report flag)")
	}

	outputFile := viper.GetString("output")
	parallelism := viper.GetInt("parallel")

	log.Info("Starting narinfo key extraction",
		"report_id", cfg.ReportID,
		"bucket", cfg.Bucket,
		"prefix", cfg.Prefix,
		"output_file", outputFile,
		"parallel_workers", parallelism)

	// Get the directory where parquet files should be
	bucket := cfg.Workarea.Bucket(cfg.Bucket, inventory.BucketConfig())
	manifestKey := fmt.Sprintf("%s%s/manifest.json", cfg.Prefix, cfg.ReportID)
	manifestPath := bucket.GetPath(manifestKey)

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

	// Get parquet files from manifest
	parquetFiles := make([]string, 0, len(manifest.Files))

	for _, file := range manifest.Files {
		if !strings.HasSuffix(file.Key, ".parquet") {
			continue
		}

		localPath := bucket.GetPath(file.Key)
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			log.Warn("Parquet file not found locally", "key", file.Key)
			continue
		}

		parquetFiles = append(parquetFiles, localPath)
	}

	if len(parquetFiles) == 0 {
		return errors.New("no parquet files found locally")
	}

	log.Info("Found parquet files", "count", len(parquetFiles))

	// Extract keys in parallel with progress bar
	keys, err := extractKeysParallelWithProgress(ctx, parquetFiles, parallelism)
	if err != nil {
		return fmt.Errorf("failed to extract keys: %w", err)
	}

	log.Info("Extracted narinfo keys", "count", len(keys))

	// Sort keys
	log.Info("Sorting keys...")

	startSort := time.Now()

	sort.Strings(keys)

	log.Info("Sorted keys", "duration", time.Since(startSort).Round(time.Millisecond))

	// Write to output file
	log.Info("Writing keys to file", "output", outputFile)

	if err := writeKeysToFile(outputFile, keys); err != nil {
		return fmt.Errorf("failed to write keys: %w", err)
	}

	log.Info("Successfully wrote keys", "count", len(keys), "file", outputFile)

	return nil
}

// S3InventoryRecord represents a record in the S3 inventory parquet file.
type S3InventoryRecord struct {
	Bucket           string `parquet:"bucket"`
	Key              string `parquet:"key"`
	Size             *int64 `parquet:"size"`
	LastModifiedDate *int64 `parquet:"last_modified_date"`
	ETag             string `parquet:"e_tag"`
	StorageClass     string `parquet:"storage_class"`
}

func extractKeysParallelWithProgress(ctx context.Context, parquetFiles []string, parallelism int) ([]string, error) {
	// Initialize progress bar
	prog := progress.New(progress.WithDefaultGradient())
	model := progressModel{
		progress:   prog,
		totalFiles: len(parquetFiles),
	}

	// Start the progress UI in a goroutine
	progressChan := make(chan progressMsg, 100)
	done := make(chan struct{})

	go func() {
		defer close(done)
		p := tea.NewProgram(model)

		// Listen for progress updates
		go func() {
			for msg := range progressChan {
				p.Send(msg)
			}
			// Send final completion message
			p.Send(progressMsg{
				filesProcessed: len(parquetFiles),
				totalFiles:     len(parquetFiles),
				keysExtracted:  model.keysExtracted,
			})
			time.Sleep(500 * time.Millisecond) // Give time to see completion
			p.Quit()
		}()

		if _, err := p.Run(); err != nil {
			log.Error("Error running progress UI", "error", err)
		}
	}()

	// Channel to send files to workers
	fileChan := make(chan string, len(parquetFiles))
	for _, file := range parquetFiles {
		fileChan <- file
	}
	close(fileChan)

	// Mutex to protect the shared data
	var mu sync.Mutex
	var allKeys []string
	var filesProcessed int64
	var totalKeysExtracted int64

	// Error group for parallel processing
	eg, ctx := errgroup.WithContext(ctx)

	// Start workers
	for i := range parallelism {
		workerID := i

		eg.Go(func() error {
			for file := range fileChan {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				keys, err := extractKeysFromFile(file)
				if err != nil {
					return fmt.Errorf("worker %d failed on %s: %w", workerID, file, err)
				}

				mu.Lock()
				allKeys = append(allKeys, keys...)
				mu.Unlock()

				// Update progress atomically
				processed := atomic.AddInt64(&filesProcessed, 1)
				totalKeys := atomic.AddInt64(&totalKeysExtracted, int64(len(keys)))

				// Send progress update
				select {
				case progressChan <- progressMsg{
					filesProcessed: int(processed),
					totalFiles:     len(parquetFiles),
					keysExtracted:  int(totalKeys),
				}:
				default:
				}
			}

			return nil
		})
	}

	// Wait for all workers to complete
	err := eg.Wait()
	close(progressChan)

	// Wait for progress UI to finish
	<-done

	if err != nil {
		return nil, fmt.Errorf("worker error: %w", err)
	}

	return allKeys, nil
}

func extractKeysParallel(ctx context.Context, parquetFiles []string, parallelism int) ([]string, error) {
	// Channel to send files to workers
	fileChan := make(chan string, len(parquetFiles))
	for _, file := range parquetFiles {
		fileChan <- file
	}

	close(fileChan)

	// Mutex to protect the shared keys slice
	var mu sync.Mutex

	var allKeys []string

	// Error group for parallel processing
	eg, ctx := errgroup.WithContext(ctx)

	// Start workers
	for i := range parallelism {
		workerID := i

		eg.Go(func() error {
			for file := range fileChan {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				log.Debug("Worker processing file", "worker", workerID, "file", filepath.Base(file))

				keys, err := extractKeysFromFile(file)
				if err != nil {
					return fmt.Errorf("worker %d failed on %s: %w", workerID, file, err)
				}

				mu.Lock()
				allKeys = append(allKeys, keys...)
				mu.Unlock()

				log.Debug("Worker completed file", "worker", workerID, "file", filepath.Base(file), "keys", len(keys))
			}

			return nil
		})
	}

	// Wait for all workers to complete
	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("worker error: %w", err)
	}

	return allKeys, nil
}

func extractKeysFromFile(parquetFile string) ([]string, error) {
	file, err := os.Open(parquetFile) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to open parquet file: %w", err)
	}
	defer file.Close() //nolint:errcheck

	reader := parquet.NewGenericReader[S3InventoryRecord](file)
	defer reader.Close() //nolint:errcheck

	const batchSize = 1000
	records := make([]S3InventoryRecord, batchSize)

	var keys []string

	for {
		n, err := reader.Read(records)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("failed to read parquet records: %w", err)
		}

		if n == 0 {
			break
		}

		// Process the batch
		for i := range n {
			record := records[i]

			// Skip non-narinfo files
			if !strings.HasSuffix(record.Key, ".narinfo") {
				continue
			}

			// Analyze path to confirm it's a narinfo
			analysis, err := store.AnalyzePath(record.Key)
			if err != nil || analysis.ObjectType != db.ObjectTypeNarinfo {
				continue
			}

			// Extract the hash from the narinfo path
			// narinfo paths start with the hash (32 chars)
			if len(record.Key) < 32 {
				continue
			}

			hashStr := record.Key[:32]
			keys = append(keys, hashStr)
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return keys, nil
}

func writeKeysToFile(outputFile string, keys []string) error {
	file, err := os.Create(outputFile) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close() //nolint:errcheck

	// Write all keys, one per line
	for _, key := range keys {
		if len(key) != 32 {
			return fmt.Errorf("invalid key length: expected 32, got %d", len(key))
		}

		// Write key followed by newline
		if _, err := file.WriteString(key + "\n"); err != nil {
			return fmt.Errorf("failed to write key: %w", err)
		}
	}

	return nil
}
