package root

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/numtide/narwal/pkg/db"
	"github.com/spf13/cobra"
)

func rootAdd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a GC root",
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

			// check if the object exists
			_, err = queries.GetObjectByHash(ctx, narHash)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("object not found: %s", path)
			} else if err != nil {
				return fmt.Errorf("failed to get object: %w", err)
			}

			// add the gc root
			if err = queries.PutGCRoot(ctx, narHash); err != nil {
				return fmt.Errorf("failed to add gc root: %w", err)
			}

			return nil
		},
	}

	cmd.SilenceUsage = true

	return cmd
}
