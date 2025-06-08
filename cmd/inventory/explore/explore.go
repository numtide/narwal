package explore

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explore",
		Short: "Explore inventory data interactively with ClickHouse Local",
		Long: `Loads inventory parquet files into ClickHouse Local with pre-configured views
for interactive data exploration. The parquet files must already be downloaded.

This command creates several useful views:
- inventory: All inventory data from the parquet files

The command will check if ClickHouse Local is available and if the parquet files
exist in the workarea before launching the interactive shell.`,
		Example: `  # Explore a specific report
  narwal inventory explore --bucket nix-cache-inventory --report 2025-06-07T01-00Z

  # Use custom workarea
  narwal inventory explore --bucket nix-cache-inventory --report 2025-06-07T01-00Z \
    --workarea.path /tmp/my-cache`,
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

	// Require report ID for explore command
	if cfg.ReportID == "" {
		return errors.New("report ID is required for explore command (use --report flag)")
	}

	// Check if clickhouse-local is available
	if err := checkClickHouseLocal(); err != nil {
		return err
	}

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

	// Check if parquet files exist
	parquetPattern := filepath.Join(reportDir, "*.parquet")

	matches, err := filepath.Glob(parquetPattern)
	if err != nil {
		return fmt.Errorf("failed to check for parquet files: %w", err)
	}

	if len(matches) == 0 {
		return fmt.Errorf("no parquet files found in %s\n"+
			"Run 'narwal inventory download' first to download the data", reportDir)
	}

	log.Info("Found inventory data", "directory", reportDir, "parquet_files", len(matches))

	// Run ClickHouse Local with initialization
	return runClickHouseLocal(reportDir)
}

func checkClickHouseLocal() error {
	cmd := exec.Command("clickhouse-local", "--version")
	if err := cmd.Run(); err != nil {
		return errors.New("clickhouse-local not found in PATH\n" +
			"Please install ClickHouse: https://clickhouse.com/docs/en/getting-started/install")
	}

	return nil
}

func runClickHouseLocal(reportDir string) error {
	// Create initialization SQL
	initSQL := `
-- Create main inventory view from all parquet files
CREATE VIEW inventory AS 
SELECT * FROM file('*.parquet', 'Parquet');
`

	// Change to the report directory
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	if err := os.Chdir(reportDir); err != nil {
		return fmt.Errorf("failed to change to report directory: %w", err)
	}

	// Ensure we change back
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			log.Error("Failed to change back to original directory", "error", err)
		}
	}()

	// Create temporary init file
	tmpFile, err := os.CreateTemp("", "narwal-inventory-init-*.sql")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			log.Warn("Failed to remove temporary file", "file", tmpFile.Name(), "error", err)
		}
	}()

	if _, err := tmpFile.WriteString(initSQL); err != nil {
		return fmt.Errorf("failed to write init SQL: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	log.Info("Starting ClickHouse Local interactive session", "directory", reportDir)

	// Run clickhouse-local with init file
	//nolint:gosec // tmpFile.Name() is safe - we created the temp file ourselves
	cmd := exec.Command("clickhouse-local", "--queries-file", tmpFile.Name(), "--interactive")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clickhouse-local failed: %w", err)
	}

	return nil
}
