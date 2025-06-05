package getmanifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
		Use:   "get-manifest",
		Short: "Get and cache inventory manifest for a specific report ID",
		Long: `Downloads the manifest.json file for a specific inventory report ID and stores it
in the workarea for later use by other commands.`,
		Example: `  # Get manifest for specific report
  narwal inventory get-manifest --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

  # Use custom workarea directory
  narwal inventory get-manifest --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z --workdir /tmp/cache`,
		RunE: runE,
	}

	// Add flags
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
	if workdirFlag := cmd.Flag("workdir"); workdirFlag != nil && workdirFlag.Changed {
		viper.Set("workdir", workdirFlag.Value.String())
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
		return fmt.Errorf("report ID is required for get-manifest command")
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())
	log.Info("Accessing S3 bucket", "bucket", cfg.Bucket, "prefix", cfg.Prefix, "region", cfg.BucketRegion)
	log.Info("Using work directory", "workdir", cfg.Workarea.GetBasePath())

	// Create a new S3 client with the correct region
	regionCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.BucketRegion),
		awsconfig.WithRetryMaxAttempts(5),
	)
	if err != nil {
		return fmt.Errorf("error loading AWS config with region: %w", err)
	}

	s3Client := s3.NewFromConfig(regionCfg)

	// Create inventory client
	inventoryClient := inventory.NewClient(s3Client, cfg.Bucket, cfg.Prefix)

	log.Info("Getting manifest for report", "report", cfg.ReportID)

	// Get manifest info
	manifest, err := inventoryClient.GetManifest(ctx, cfg.ReportID)
	if err != nil {
		return fmt.Errorf("error getting manifest: %w", err)
	}

	log.Info("Found parquet files in manifest", "count", len(manifest.Files))
	totalSize := manifest.TotalSize()
	log.Info("Total size", "size", formatBytes(totalSize))

	// Store manifest in workarea
	manifestPath := filepath.Join(cfg.Workarea.GetBasePath(), "manifests", cfg.Bucket, cfg.ReportID, "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o750); err != nil {
		return fmt.Errorf("failed to create manifest directory: %w", err)
	}

	manifestFile, err := os.Create(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to create manifest file: %w", err)
	}
	defer manifestFile.Close()

	encoder := json.NewEncoder(manifestFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	log.Info("Manifest cached successfully", "path", manifestPath)

	return nil
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
