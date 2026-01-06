package inventory

import (
	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	// create the command
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Inventory service related commands such as downloading manifest and list files",
	}

	cmd.AddCommand(manifestCmd())
	cmd.AddCommand(downloadCmd())
	cmd.AddCommand(verifyCmd())
	cmd.AddCommand(fuseCmd())
	cmd.AddCommand(exportNarinfoCmd())
	cmd.AddCommand(pruneCmd())
	cmd.AddCommand(mergeCmd())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
