package gc

import (
	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	// create the command
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage collection related commands",
	}

	cmd.AddCommand(simpleCmd())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
