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
		Use:   "export-narinfos <output-file>",
		Short: "Export narinfo entries from badger database to a parquet file",
		Long: `Export all narinfo entries from the badger database to a single parquet file.
Uses ZSTD compression and includes a bloom filter on the hash column for fast lookups.
Progress is logged every 100,000 records.
Supports graceful cancellation with Ctrl+C.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputPath := args[0]
			return inventory.ExportNarinfos(cmd.Context(), cfg, outputPath)
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
