package gc

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/numtide/narwal/pkg/db"

	"github.com/spf13/cobra"
)

func planRemove() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Remove a GC plan",
		Args:  cobra.ExactArgs(1),
		RunE:  removePlan,
	}

	cmd.SilenceUsage = true

	return cmd
}

func removePlan(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	planID, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil {
		return fmt.Errorf("failed to parse plan id: %w", err)
	}

	// acquire a db connection
	conn, err := pg.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire db connection: %w", err)
	}

	defer conn.Release()

	// start a transaction
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	//nolint:errcheck
	defer tx.Rollback(ctx)

	queries := db.New(tx)

	// check the plan exists

	_, err = queries.GetGCPlan(ctx, int32(planID))
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("plan not found: %d", planID)
	} else if err != nil {
		return fmt.Errorf("failed to get gc plan: %w", err)
	}

	// remove all associated tables

	count, err := queries.DeleteGCPlan(ctx, int32(planID))
	if err != nil || count == 0 {
		return fmt.Errorf("failed to delete gc plan: %w", err)
	}

	if err = removePlanTables(ctx, tx, int32(planID)); err != nil {
		return errors.New("failed to remove plan tables")
	}

	// commit changes to the db
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
