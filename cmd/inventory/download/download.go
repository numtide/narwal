package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/numtide/narwal/pkg/awssdk"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/workarea"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download parquet files for a specific report ID into the workarea",
		Long: `Downloads all parquet files for a specific inventory report ID. The manifest
is downloaded automatically if not already cached in the workarea.
Files are downloaded in parallel and cached in the workarea.`,
		Example: `  # Download files for specific report
  narwal inventory download --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

  # Use custom workarea directory
  narwal inventory download --bucket nix-cache-inventory --prefix data/ \
    --report 2025-06-03T01-00Z --workarea.path /tmp/cache

  # Download with custom parallelism
  narwal inventory download --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z --parallel 16`,
		RunE: runE,
	}

	// Add flags
	appconfig.SetInventoryFlags(cmd.Flags())
	cmd.Flags().Int("parallel", 8, "Number of parallel downloads")

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

	if parallelFlag := cmd.Flag("parallel"); parallelFlag != nil && parallelFlag.Changed {
		viper.Set("parallel", parallelFlag.Value.String())
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

	if cfg.ReportID == "" {
		return errors.New("report ID is required for download command")
	}

	parallelism := viper.GetInt("parallel")
	if parallelism <= 0 {
		parallelism = 8
	}

	// Create AWS credentials
	creds, err := awssdk.NewCredentials(ctx, cfg.Credentials)
	if err != nil {
		return fmt.Errorf("failed to create AWS credentials: %w", err)
	}

	// Create bucket client using awssdk
	bucketClient, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
		Bucket:   cfg.Bucket,
		Region:   cfg.BucketRegion,
		Endpoint: cfg.Endpoint,
		UseSSL:   cfg.UseSSL,
	}, creds)
	if err != nil {
		return fmt.Errorf("failed to create bucket client: %w", err)
	}

	// Load or download manifest
	manifest, err := loadOrDownloadManifest(ctx, bucketClient, cfg)
	if err != nil {
		return fmt.Errorf("failed to get manifest: %w", err)
	}

	// Create the UI model
	model := newModel(cfg, bucketClient, manifest, parallelism)

	// Run the Bubble Tea program
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("error running UI: %w", err)
	}

	return model.err
}

// loadOrDownloadManifest loads manifest from cache or downloads it if not present.
func loadOrDownloadManifest(
	ctx context.Context, bucketClient *awssdk.BucketClient, cfg *appconfig.Inventory,
) (*inventory.InventoryManifest, error) {
	// Check if manifest exists in workarea
	manifestPath := filepath.Join(cfg.Workarea.GetBasePath(), "manifests", cfg.Bucket, cfg.ReportID, "manifest.json")

	//nolint:gosec
	if manifestFile, err := os.Open(manifestPath); err == nil {
		//nolint:errcheck
		defer manifestFile.Close()

		var manifest inventory.InventoryManifest

		decoder := json.NewDecoder(manifestFile)
		if err := decoder.Decode(&manifest); err == nil {
			log.Info("Loaded manifest from cache", "path", manifestPath)
			return &manifest, nil
		}

		_ = manifestFile.Close()
	}

	// Download manifest
	log.Info("Downloading manifest", "bucket", cfg.Bucket, "reportID", cfg.ReportID)

	inventoryClient := inventory.NewClient(bucketClient, cfg.Prefix)

	manifest, err := inventoryClient.GetManifest(ctx, cfg.ReportID)
	if err != nil {
		return nil, fmt.Errorf("failed to download manifest: %w", err)
	}

	// Save manifest to workarea
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o750); err != nil {
		return nil, fmt.Errorf("failed to create manifest directory: %w", err)
	}

	//nolint:gosec
	manifestFile, err := os.Create(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifest file: %w", err)
	}
	//nolint:errcheck
	defer manifestFile.Close()

	encoder := json.NewEncoder(manifestFile)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(manifest); err != nil {
		return nil, fmt.Errorf("failed to save manifest: %w", err)
	}

	log.Info("Manifest downloaded and cached", "files", len(manifest.Files), "path", manifestPath)

	return manifest, nil
}

// Bubble Tea Model.
type model struct {
	cfg          *appconfig.Inventory
	bucketClient *awssdk.BucketClient
	manifest     *inventory.InventoryManifest
	parallelism  int

	// Download state
	downloads      []downloadState
	completed      int
	totalFiles     int
	totalSize      int64
	downloadedSize int64
	startTime      time.Time
	err            error
	done           bool

	// UI state
	width  int
	height int

	// Channels for communication
	progressChan chan progressUpdate
	errorChan    chan error
	doneChan     chan struct{}
}

type downloadState struct {
	key       string
	size      int64
	progress  int64
	completed bool
	error     error
}

type progressUpdate struct {
	index     int
	key       string
	progress  int64
	total     int64
	completed bool
	err       error
}

func newModel(
	cfg *appconfig.Inventory, bucketClient *awssdk.BucketClient, manifest *inventory.InventoryManifest, parallelism int,
) *model {
	downloads := make([]downloadState, len(manifest.Files))
	for i, file := range manifest.Files {
		downloads[i] = downloadState{
			key:  file.Key,
			size: file.Size,
		}
	}

	return &model{
		cfg:          cfg,
		bucketClient: bucketClient,
		manifest:     manifest,
		parallelism:  parallelism,
		downloads:    downloads,
		totalFiles:   len(manifest.Files),
		totalSize:    manifest.TotalSize(),
		startTime:    time.Now(),
		progressChan: make(chan progressUpdate, 100),
		errorChan:    make(chan error, 1),
		doneChan:     make(chan struct{}, 1),
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(
		m.startDownloads(),
		m.listenForProgress(),
	)
}

//nolint:ireturn
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if !m.done {
				return m, tea.Quit
			}
		case "enter":
			if m.done {
				return m, tea.Quit
			}
		}

		return m, nil

	case progressUpdate:
		if msg.index < len(m.downloads) {
			old := m.downloads[msg.index]
			m.downloads[msg.index].progress = msg.progress

			if msg.completed && !old.completed {
				m.downloads[msg.index].completed = true
				m.completed++
				m.downloadedSize += m.downloads[msg.index].size
			}

			if msg.err != nil {
				m.downloads[msg.index].error = msg.err
			}
		}

		return m, m.listenForProgress()

	case errMsg:
		m.err = msg.err
		m.done = true

		return m, nil

	case doneMsg:
		m.done = true
		return m, nil
	}

	return m, nil
}

func (m *model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7D56F4")).
		PaddingBottom(1)

	b.WriteString(headerStyle.Render("Narwal Inventory Download"))
	b.WriteString("\n\n")

	// Overall progress
	overallPercent := float64(m.completed) / float64(m.totalFiles) * 100
	bytesPercent := float64(m.downloadedSize) / float64(m.totalSize) * 100

	elapsed := time.Since(m.startTime)

	var speed string

	if elapsed.Seconds() > 0 && m.downloadedSize > 0 {
		bytesPerSecond := float64(m.downloadedSize) / elapsed.Seconds()
		speed = fmt.Sprintf(" (%s/s)", formatBytes(int64(bytesPerSecond)))
	}

	b.WriteString(fmt.Sprintf("Overall Progress: %d/%d files (%.1f%%) | %s/%s (%.1f%%)%s\n",
		m.completed, m.totalFiles, overallPercent,
		formatBytes(m.downloadedSize), formatBytes(m.totalSize), bytesPercent,
		speed))

	overallBar := createProgressBar(overallPercent, m.width-20)
	b.WriteString(overallBar)
	b.WriteString("\n\n")

	// Individual file progress (show only active downloads)
	maxVisible := m.height - 10 // Reserve space for header and footer
	if maxVisible < 1 {
		maxVisible = 1
	}

	activeDownloads := 0

	for _, download := range m.downloads {
		if download.completed {
			continue
		}

		if activeDownloads >= maxVisible {
			break
		}

		percent := float64(0)
		if download.size > 0 {
			percent = float64(download.progress) / float64(download.size) * 100
		}

		fileName := filepath.Base(download.key)
		if len(fileName) > 40 {
			fileName = fileName[:37] + "..."
		}

		status := "downloading"
		if download.error != nil {
			status = "error"
		}

		b.WriteString(fmt.Sprintf("%-40s %6.1f%% [%s] %s\n",
			fileName,
			percent,
			createProgressBar(percent, 20),
			status))

		activeDownloads++
	}

	// Show completed count if there are more files
	if len(m.downloads) > activeDownloads+m.completed {
		remaining := len(m.downloads) - activeDownloads - m.completed
		b.WriteString(fmt.Sprintf("\n... %d more files queued for download\n", remaining))
	}

	// Footer
	b.WriteString("\n")

	if m.done {
		if m.err != nil {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Render(
				fmt.Sprintf("Error: %v", m.err)))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Render(
				fmt.Sprintf("✓ Download completed in %v", elapsed.Round(time.Second))))
		}

		b.WriteString("\nPress Enter to exit or Ctrl+C to quit")
	} else {
		b.WriteString("Press Ctrl+C to cancel")
	}

	return b.String()
}

//nolint:gocognit
func (m *model) startDownloads() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		bucket := m.cfg.Workarea.Bucket(m.cfg.Bucket)

		// Create worker pool
		fileChan := make(chan int, len(m.downloads))

		var wg sync.WaitGroup

		// Start workers
		for range m.parallelism {
			wg.Add(1)

			go func() {
				defer wg.Done()

				for fileIndex := range fileChan {
					if fileIndex >= len(m.downloads) {
						continue
					}

					download := m.downloads[fileIndex]

					// Create adapter for workarea compatibility
					s3Client := workarea.NewBucketClientAdapter(m.bucketClient)
					err := bucket.Download(ctx, s3Client, download.key, func(bucket, key string, written, total int64) {
						select {
						case m.progressChan <- progressUpdate{
							index:    fileIndex,
							key:      key,
							progress: written,
							total:    total,
						}:
						default:
						}
					})

					// Send completion update
					select {
					case m.progressChan <- progressUpdate{
						index:     fileIndex,
						key:       download.key,
						progress:  download.size,
						total:     download.size,
						completed: err == nil,
						err:       err,
					}:
					default:
					}

					if err != nil {
						select {
						case m.errorChan <- fmt.Errorf("failed to download %s: %w", download.key, err):
						default:
						}

						return
					}
				}
			}()
		}

		// Send file indices to workers
		go func() {
			defer close(fileChan)

			for i := range m.downloads {
				select {
				case fileChan <- i:
				case <-ctx.Done():
					return
				}
			}
		}()

		// Wait for completion
		go func() {
			wg.Wait()
			select {
			case m.doneChan <- struct{}{}:
			default:
			}
		}()

		return nil
	}
}

func (m *model) listenForProgress() tea.Cmd {
	return func() tea.Msg {
		select {
		case update := <-m.progressChan:
			return update
		case err := <-m.errorChan:
			return errMsg{err}
		case <-m.doneChan:
			return doneMsg{}
		}
	}
}

// Custom message types.
type (
	errMsg  struct{ err error }
	doneMsg struct{}
)

// Helper functions.
func createProgressBar(percent float64, width int) string {
	if width < 2 {
		return ""
	}

	filled := int(percent / 100.0 * float64(width-2))
	if filled > width-2 {
		filled = width - 2
	}

	if filled < 0 {
		filled = 0
	}

	bar := "["

	for i := range width - 2 {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}

	bar += "]"

	return bar
}

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
