package inventory

import (
	"github.com/numtide/narwal/cmd/inventory/download"
	"github.com/numtide/narwal/cmd/inventory/explore"
	listreports "github.com/numtide/narwal/cmd/inventory/list-reports"
	"github.com/numtide/narwal/cmd/inventory/manifest"
	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Interact with S3 inventory data",
		Long: `The inventory command provides sub-commands to interact with S3 inventory data
without importing all the data. Use these commands to explore available reports,
get the latest inventory report, or examine manifest information.`,
		Example: `  # List all available inventory reports
  narwal inventory list-reports --bucket nix-cache-inventory --prefix data/

  # Get manifest information for a specific report
  narwal inventory manifest --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

  # Download parquet files for interactive analysis
  narwal inventory download --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

  # Explore downloaded data interactively with ClickHouse
  narwal inventory explore --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z`,
	}

	// set flags
	config.SetS3Flags(cmd.PersistentFlags())

	// Add sub-commands
	cmd.AddCommand(listreports.NewCmd())
	cmd.AddCommand(manifest.NewCmd())
	cmd.AddCommand(download.NewCmd())
	cmd.AddCommand(explore.NewCmd())

	// Note: Sub-commands handle their own flag binding

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
