package gc

import (
	"errors"
	"fmt"

	"github.com/numtide/narwal/pkg/db"
	"github.com/spf13/cobra"
)

func rootRemove() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Remove a GC root",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			defer pg.Close()

			ctx := cmd.Context()

			path := args[0]

			narHash, err := extractNarHash(path)
			if err != nil {
				return err
			}

			conn, err := pg.Acquire(ctx)
			if err != nil {
				return fmt.Errorf("failed to acquire db connection: %w", err)
			}

			defer conn.Release()

			queries := db.New(conn)

			count, err := queries.DeleteGCRoot(ctx, narHash)
			if err != nil {
				return fmt.Errorf("failed to delete gc root: %w", err)
			}

			if count == 0 {
				return fmt.Errorf("gc root not found: %s", path)
			}

			if count > 1 {
				return errors.New("multiple gc roots deleted, this should not happen")
			}

			return nil
		},
	}

	cmd.SilenceUsage = true

	return cmd
}
