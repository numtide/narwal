package manifest

import (
	"encoding/json"
	"fmt"
	"os"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	outputFormat string
	showStats    bool
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Get manifest information for a specific inventory report",
		Long: `Gets and displays manifest information for a specific inventory report.
The manifest contains metadata about all parquet files in the inventory, including
file paths, sizes, and checksums.`,
		Example: `  # Get manifest for a specific report
  narwal inventory manifest --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

  # Get manifest in JSON format
  narwal inventory manifest --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z --format json

  # Show only statistics
  narwal inventory manifest --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z --stats`,
		RunE: runE,
	}

	// Add inventory-specific flags
	appconfig.SetInventoryFlags(cmd.Flags())

	// Add command-specific flags
	cmd.Flags().StringVar(&outputFormat, "format", "table", "Output format: table, json")
	cmd.Flags().BoolVar(&showStats, "stats", false, "Show only statistics summary")

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
	var cfg *appconfig.Inventory
	if err := appconfig.FromViper(viper.GetViper(), &cfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err := cfg.Validate(ctx, awscfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Require report ID for manifest command
	if cfg.ReportID == "" {
		return fmt.Errorf("report ID is required for manifest command (use --report flag)")
	}

	log.Info("Accessing S3 bucket", "bucket", cfg.Bucket, "prefix", cfg.Prefix, "region", cfg.BucketRegion)

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
	inventoryClient, err := inventory.NewClient(s3Client, cfg.Bucket, cfg.Prefix, "")
	if err != nil {
		return fmt.Errorf("error creating inventory client: %w", err)
	}

	// Get manifest
	manifest, err := inventoryClient.GetManifest(ctx, cfg.ReportID)
	if err != nil {
		return fmt.Errorf("error getting manifest for report %s: %w", cfg.ReportID, err)
	}

	log.Info("Retrieved manifest", "report", cfg.ReportID, "files", len(manifest.Files))

	// Display results based on format
	switch {
	case showStats:
		return displayStats(manifest)
	case outputFormat == "json":
		return displayJSON(manifest)
	default:
		return displayTable(manifest)
	}
}

func displayStats(manifest *inventory.InventoryManifest) error {
	totalSize := manifest.TotalSize()

	fmt.Printf("Manifest Statistics for %s:\n", manifest.CreationTime)
	fmt.Printf("  Source Bucket: %s\n", manifest.SourceBucket)
	fmt.Printf("  Destination Bucket: %s\n", manifest.DestBucket)
	fmt.Printf("  File Format: %s\n", manifest.FileFormat)
	fmt.Printf("  File Schema: %s\n", manifest.FileSchema)
	fmt.Printf("  Version: %s\n", manifest.Version)
	fmt.Printf("  Total Files: %d\n", len(manifest.Files))
	fmt.Printf("  Total Size: %s\n", formatBytes(totalSize))

	return nil
}

func displayJSON(manifest *inventory.InventoryManifest) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(manifest)
}

func displayTable(manifest *inventory.InventoryManifest) error {
	totalSize := manifest.TotalSize()

	fmt.Printf("Manifest Information:\n")
	fmt.Printf("  Creation Time: %s\n", manifest.CreationTime)
	fmt.Printf("  Source Bucket: %s\n", manifest.SourceBucket)
	fmt.Printf("  Destination Bucket: %s\n", manifest.DestBucket)
	fmt.Printf("  File Format: %s\n", manifest.FileFormat)
	fmt.Printf("  File Schema: %s\n", manifest.FileSchema)
	fmt.Printf("  Version: %s\n", manifest.Version)
	fmt.Printf("  Total Files: %d\n", len(manifest.Files))
	fmt.Printf("  Total Size: %s\n", formatBytes(totalSize))
	fmt.Printf("\nFiles:\n")

	for i, file := range manifest.Files {
		fmt.Printf("  %d. %s\n", i+1, file.Key)
		fmt.Printf("     Size: %s\n", formatBytes(file.Size))
		fmt.Printf("     MD5: %s\n", file.MD5Checksum)
		if i < len(manifest.Files)-1 {
			fmt.Println()
		}
	}

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
