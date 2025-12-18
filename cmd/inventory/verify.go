package inventory

import (
	"fmt"

	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
)

func verifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify what we have downloaded",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			report := args[0]

			return inventory.Verify(cmd.Context(), cfg, report)
		},
	}

	config.SetBadgerFlags(cmd.Flags())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
