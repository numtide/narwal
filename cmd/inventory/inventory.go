package inventory

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//nolint:gochecknoglobals
var (
	cfg *config.Config

	s3 *awssdk.BucketClient
)

func NewCmd() *cobra.Command {
	// create the command
	cmd := &cobra.Command{
		Use:               "inventory",
		Short:             "Inventory service related commands such as downloading manifest and list files",
		PersistentPreRunE: preRunE,
	}

	fs := cmd.PersistentFlags()
	config.SetAWSFlags(fs)
	config.SetS3Flags(fs)
	config.SetInventoryFlags(fs)

	// bind our command's flags to viper
	if err := viper.BindPFlags(fs); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	cmd.AddCommand(manifestCmd())
	cmd.AddCommand(downloadCmd())
	cmd.AddCommand(verifyCmd())
	cmd.AddCommand(fuseCmd())
	cmd.AddCommand(exportNarinfoCmd())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}

func preRunE(cmd *cobra.Command, _ []string) error {
	var err error

	// parse viper into our config object
	v := viper.GetViper()
	if err = config.FromViper(v, &cfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err = cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())

	// create s3 client
	if s3, err = awssdk.NewS3Client(cmd.Context(), cfg.AWS, cfg.S3); err != nil {
		//nolint:wrapcheck
		return err
	}

	return nil
}
