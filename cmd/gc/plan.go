package gc

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

func plan() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Plan and execute GC plans",
	}

	cmd.AddCommand(planCreate())
	cmd.AddCommand(planList())
	cmd.AddCommand(planRemove())
	cmd.AddCommand(planApply())

	return cmd
}

func tableName(planID int32, name string) string {
	return fmt.Sprintf("gc_plan_%d_%s", planID, name)
}

func closureTableName(planID int32) string {
	return tableName(planID, "closure")
}

func deletionsTableName(planID int32) string {
	return tableName(planID, "deletions")
}

func addDeletionsTable(ctx context.Context, tx pgx.Tx, planID int32) error {
	name := deletionsTableName(planID)

	// we might be re-running a plan so we only create the table if it doesn't exist
	_, err := tx.Exec(ctx, fmt.Sprintf(
		`
		create table if not exists %s (		     
			path varchar(128) primary key,		
			applied_at timestamp,
		    error varchar(256)
		)`,
		name,
	))
	if err != nil {
		return fmt.Errorf("failed to add deletions table: %w", err)
	}

	// truncate any entries from a previous run
	if _, err = tx.Exec(ctx, "truncate table "+name); err != nil {
		return fmt.Errorf("failed to truncate deletions table: %w", err)
	}

	return nil
}

func removePlanTables(ctx context.Context, tx pgx.Tx, planID int32) error {
	if _, err := tx.Exec(ctx, `drop table `+closureTableName(planID)); err != nil {
		return fmt.Errorf("failed to remove closure table: %w", err)
	}

	if _, err := tx.Exec(ctx, `drop table `+deletionsTableName(planID)); err != nil {
		return fmt.Errorf("failed to remove deletions table: %w", err)
	}

	return nil
}
