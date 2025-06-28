package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/numtide/narwal/pkg/awssdk"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/util"
)

var (
	outputFormat string //nolint:gochecknoglobals
	showStats    bool   //nolint:gochecknoglobals
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

	if endpointFlag := cmd.Flag("endpoint"); endpointFlag != nil && endpointFlag.Changed {
		viper.Set("endpoint", endpointFlag.Value.String())
	}

	if useSSLFlag := cmd.Flag("use_ssl"); useSSLFlag != nil && useSSLFlag.Changed {
		viper.Set("use_ssl", useSSLFlag.Value.String())
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

	// Require report ID for manifest command
	if cfg.ReportID == "" {
		return errors.New("report ID is required for manifest command (use --report flag)")
	}

	log.Info("Accessing S3 bucket", "bucket", cfg.Bucket, "prefix", cfg.Prefix, "region", cfg.BucketRegion)

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

	// Create inventory client
	inventoryClient := inventory.NewClient(bucketClient, cfg.Prefix)

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
	fmt.Printf("  Total Size: %s\n", util.FormatBytes(totalSize))

	return nil
}

func displayJSON(manifest *inventory.InventoryManifest) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(manifest.Files); err != nil {
		return fmt.Errorf("error encoding manifest files: %w", err)
	}

	return nil
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
	fmt.Printf("  Total Size: %s\n", util.FormatBytes(totalSize))
	fmt.Printf("\nFiles:\n")

	for i, file := range manifest.Files {
		fmt.Printf("  %d. %s\n", i+1, file.Key)
		fmt.Printf("     Size: %s\n", util.FormatBytes(file.Size))
		fmt.Printf("     MD5: %s\n", file.MD5Checksum)

		if i < len(manifest.Files)-1 {
			fmt.Println()
		}
	}

	return nil
}
