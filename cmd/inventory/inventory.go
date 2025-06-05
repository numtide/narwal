package inventory

import (
	"fmt"

	"github.com/numtide/narwal/cmd/inventory/dates"
	"github.com/numtide/narwal/cmd/inventory/latest"
	"github.com/numtide/narwal/cmd/inventory/manifest"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Interact with S3 inventory data",
		Long: `The inventory command provides sub-commands to interact with S3 inventory data
without importing all the data. Use these commands to explore available dates,
get the latest inventory date, or examine manifest information.`,
		Example: `  # List all available inventory dates
  narwal inventory dates --bucket nix-cache-inventory --prefix data/

  # Get the latest available inventory date
  narwal inventory latest --bucket nix-cache-inventory --prefix data/

  # Get manifest information for a specific date
  narwal inventory manifest --bucket nix-cache-inventory --prefix data/ --date 2025-06-03T01-00Z`,
	}

	// Add sub-commands
	cmd.AddCommand(dates.NewCmd())
	cmd.AddCommand(latest.NewCmd())
	cmd.AddCommand(manifest.NewCmd())

	// bind our command's flags to viper
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}