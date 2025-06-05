package gc

import (
	"github.com/spf13/cobra"
)

func plan() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Plan and execute GC plans",
	}

	cmd.AddCommand(planCreate())
	cmd.AddCommand(planList())

	return cmd
}
