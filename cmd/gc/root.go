package gc

import (
	"github.com/spf13/cobra"
)

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "root",
		Short: "GC root related commands",
	}

	cmd.AddCommand(rootAdd())
	cmd.AddCommand(rootRemove())
	cmd.AddCommand(rootList())

	return cmd
}
