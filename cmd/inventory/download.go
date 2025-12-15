package inventory

import (
	"fmt"

	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
)

func downloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download all parquet files from an inventory report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report := args[0]

			cfg, err := loadConfig(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			dl, err := inventory.NewDownloader(cfg)
			if err != nil {
				return fmt.Errorf("failed to create downloader: %w", err)
			}

			if err = dl.Download(cmd.Context(), report); err != nil {
				return fmt.Errorf("failed to download: %w", err)
			}

			return nil
		},
	}

	fs := cmd.Flags()

	config.SetAWSFlags(fs)
	config.SetS3Flags(fs)
	config.SetInventoryFlags(fs)
	config.SetBadgerFlags(fs)

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
