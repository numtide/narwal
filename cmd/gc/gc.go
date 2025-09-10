package gc

import (
	"fmt"

	"github.com/numtide/narwal/cmd/gc/plan"
	"github.com/numtide/narwal/cmd/gc/root"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCmd() *cobra.Command {
	// create the command
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Set GC roots and run garbage collection",
	}

	// bind our command's flags to viper
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	cmd.AddCommand(root.Cmd())
	cmd.AddCommand(plan.Cmd())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
