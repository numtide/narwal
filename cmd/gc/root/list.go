package root

import (
	"fmt"

	"github.com/numtide/narwal/pkg/db"
	"github.com/spf13/cobra"
)

func rootList() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List GC roots",
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
			gcRoots, err := queries.ListGCRoots(ctx)
			if err != nil {
				return fmt.Errorf("failed to list gc roots: %w", err)
			}

			for _, gcRoot := range gcRoots {
				println(gcRoot)
			}

			return nil
		},
	}

	cmd.SilenceUsage = true

	return cmd
}
