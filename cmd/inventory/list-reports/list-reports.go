package list_reports

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/numtide/narwal/pkg/awssdk"
	appconfig "github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
)

var latest bool //nolint:gochecknoglobals

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-reports",
		Short: "List available inventory reports",
		Long: `Lists all available inventory reports from the S3 bucket.
Reports are returned in lexicographical order (oldest to newest for ISO 8601 format).
Use --latest flag to get only the most recent report.`,
		Example: `  # List all available reports
  narwal inventory list-reports --bucket nix-cache-inventory --prefix data/

  # Get only the latest report
  narwal inventory list-reports --bucket nix-cache-inventory --prefix data/ --latest

  # List reports with custom bucket region
  narwal inventory list-reports --bucket nix-cache-inventory --prefix data/ --region us-east-1`,
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

	// Explicitly set flag values in viper since flag binding might not work properly
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

	// Get available reports
	reports, err := inventoryClient.ListReports(ctx)
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
