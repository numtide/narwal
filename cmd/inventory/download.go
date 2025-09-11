package inventory

import (
	"fmt"

	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func downloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download all parquet files from an inventory report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report := args[0]

			dl, err := inventory.NewDownloader(cmd.Context(), cfg)
			if err != nil {
				return fmt.Errorf("failed to create downloader: %w", err)
			}

			return dl.Download(cmd.Context(), report)
		},
	}

	config.SetBoltFlags(cmd.Flags())

	// bind our command's flags to viper
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
