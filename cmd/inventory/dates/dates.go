package dates

import (
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dates",
		Short: "List all available inventory dates",
		Long: `Lists all available inventory dates from the S3 bucket.
Dates are returned in lexicographical order (oldest to newest for ISO 8601 format).`,
		Example: `  # List all available dates
  narwal inventory dates --bucket nix-cache-inventory --prefix data/

  # List dates with custom bucket region
  narwal inventory dates --bucket nix-cache-inventory --prefix data/ --region us-east-1`,
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

	// Get available dates
	dates, err := inventoryClient.GetDates(ctx)
	if err != nil {
		return fmt.Errorf("error getting available dates: %w", err)
	}

	if len(dates) == 0 {
		fmt.Println("No inventory dates found")
		return nil
	}

	log.Info("Found inventory dates", "count", len(dates))

	// Print all dates
	for _, date := range dates {
		fmt.Println(date)
	}

	return nil
}