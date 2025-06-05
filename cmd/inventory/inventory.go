package inventory

import (
	"github.com/numtide/narwal/cmd/inventory/download"
	getmanifest "github.com/numtide/narwal/cmd/inventory/get-manifest"
	listreports "github.com/numtide/narwal/cmd/inventory/list-reports"
	"github.com/numtide/narwal/cmd/inventory/manifest"
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

  # Get the latest available inventory report
  narwal inventory list-reports --bucket nix-cache-inventory --prefix data/ --latest

  # Get manifest information for a specific report
  narwal inventory manifest --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z`,
	}

	// Add sub-commands
	cmd.AddCommand(listreports.NewCmd())
	cmd.AddCommand(manifest.NewCmd())
	cmd.AddCommand(getmanifest.NewCmd())
	cmd.AddCommand(download.NewCmd())

	// Note: Sub-commands handle their own flag binding

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
