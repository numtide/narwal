package inventory

import (
	"fmt"

	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func exportNarinfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export-narinfos <output-dir>",
		Short: "Export narinfo entries from badger database to parquet files",
		Long: `Export all narinfo entries from the badger database to parquet files.
A new parquet file is created when the current file reaches 512MB.
Progress is logged every 10,000 records.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputDir := args[0]
			return inventory.ExportNarinfos(cmd.Context(), cfg, outputDir)
		},
	}

	config.SetBadgerFlags(cmd.Flags())

	// bind our command's flags to viper
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
