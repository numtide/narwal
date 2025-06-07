package gc

import (
	"fmt"

	"github.com/numtide/narwal/pkg/db"
	"github.com/spf13/cobra"
)

func planList() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List GC plans",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			defer pg.Close()

			ctx := cmd.Context()

			conn, err := pg.Acquire(ctx)
			if err != nil {
				return fmt.Errorf("failed to acquire db connection: %w", err)
			}

			defer conn.Release()

			queries := db.New(conn)

			// todo paging/cursor?
			plans, err := queries.ListGCPlans(ctx)
			if err != nil {
				return fmt.Errorf("failed to list gc plans: %w", err)
			}

			for _, plan := range plans {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d\t%v\t%v\n", plan.ID, plan.CreatedAt.Time, plan.CompletedAt.Time)
			}

			return nil
		},
	}

	cmd.SilenceUsage = true

	return cmd
}
