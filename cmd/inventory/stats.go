package inventory

import (
	"fmt"

	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func statsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Output stats about a manifest report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report := args[0]

			return inventory.OutputStats(cmd.Context(), cfg, report)
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
