package reports

import (
	"errors"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var latest bool //nolint:gochecknoglobals

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reports",
		Short: "List available inventory reports",
		Long: `Lists all available inventory reports from the S3 bucket.
Reports are returned in lexicographical order (oldest to newest for ISO 8601 format).
Use --latest flag to get only the most recent report.`,
		Example: `  # List all available reports
  narwal inventory reports --bucket nix-cache-inventory --prefix data/

  # Get only the latest report
  narwal inventory reports --bucket nix-cache-inventory --prefix data/ --latest

  # List reports with custom bucket region
  narwal inventory reports --bucket nix-cache-inventory --prefix data/ --region us-east-1`,
		RunE: runE,
	}

	// Add inventory-specific flags
	appconfig.SetInventoryFlags(cmd.Flags())

	// Add command-specific flags
	cmd.Flags().BoolVar(&latest, "latest", false, "Show only the latest report")

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

	// Get available reports
	reports, err := inventoryClient.GetReports(ctx)
	if err != nil {
		return fmt.Errorf("error getting available reports: %w", err)
	}

	if len(reports) == 0 {
		if latest {
			return errors.New("no inventory reports found")
		}

		fmt.Println("No inventory reports found")

		return nil
	}

	if latest {
		// Get the latest report (lexicographically last)
		latestReport := reports[len(reports)-1]
		log.Info("Latest inventory report found", "report", latestReport)
		fmt.Println(latestReport)
	} else {
		log.Info("Found inventory reports", "count", len(reports))
		// Print all reports
		for _, report := range reports {
			fmt.Println(report)
		}
	}

	return nil
}
