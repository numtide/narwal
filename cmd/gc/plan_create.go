package gc

import (
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/numtide/narwal/pkg/db"

	ci "github.com/Eun/go-pgx-cursor-iterator/v2"
	"github.com/bits-and-blooms/bloom/v3"
	"github.com/spf13/cobra"
)

type closureEntry struct {
	Hash   string `db:"hash"`
	Bucket string `db:"bucket"`
	Path   string `db:"path"`
}

func planCreate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a GC plan",
		Args:  cobra.ExactArgs(1),
		RunE:  createPlan,
	}

	cmd.SilenceUsage = true

	return cmd
}

func createPlan(cmd *cobra.Command, args []string) (err error) {

	defer pg.Close()

	ctx := cmd.Context()
	name := args[0]

	conn, err := pg.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire db connection: %w", err)
	}

	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer tx.Rollback(ctx)

	queries := db.New(tx)

	// create a new plan
	planID, err := queries.InsertGCPlan(ctx, pgtype.Text{String: name, Valid: true})
	if err != nil {
		return fmt.Errorf("failed to insert gc plan: %w", err)
	}

	// create a bloom filter to filter out all nar hashes we don't need to retain'
	// todo make parameters configurable
	// current value is based on cache.nixos.org total nars
	filter := bloom.NewWithEstimates(275000000, 0.01)

	// create a table to store the deletions we need to make
	deletionsTableName := fmt.Sprintf("gc_plan_%d_deletions", planID)

	deletionPlanQuery := fmt.Sprintf(
		`create table if not exists %s (
		    bucket varchar(128), 
		    path varchar(128), 
		    applied bool, 
		    primary key (bucket, path)
		)`,
		deletionsTableName,
	)

	_, err = tx.Exec(ctx, deletionPlanQuery)
	if err != nil {
		return fmt.Errorf("failed to create %s table: %w", deletionsTableName, err)
	}

	// truncate if the table already exists and we are re-running the plan
	if _, err = tx.Exec(ctx, fmt.Sprintf("truncate table %s", deletionsTableName)); err != nil {
		return fmt.Errorf("failed to truncate %s table: %w", deletionsTableName, err)
	}

	// generate a new root closure table with the list of all nar hashes we need to retain
	if _, err = tx.Exec(ctx, `select generate_gc_root_closure($1)`, planID); err != nil {
		return fmt.Errorf("failed to generate gc root closure: %w", err)
	}

	// stream each entry in the closure table and add it to the filter
	cursorValues := make([]closureEntry, 1024)
	closureQuery := fmt.Sprintf(`select distinct(hash) as hash from gc_plan_%d_closure`, planID)

	closureIter, err := ci.NewCursorIterator(tx, cursorValues, closureQuery)
	if err != nil {
		return fmt.Errorf("failed to create closure iterator: %w", err)
	}

	for closureIter.Next(ctx) {
		entry := cursorValues[closureIter.ValueIndex()]
		filter.Add([]byte(entry.Hash))
	}

	if closureIter.Error() != nil {
		return fmt.Errorf("failed to iterate gc_root_closure: %w", closureIter.Error())
	}

	if err = closureIter.Close(ctx); err != nil {
		return fmt.Errorf("failed to close closure iterator: %w", err)
	}

	// stream every object (except nars) and add their hashes to the filter
	// everything except nars is keyed by the same hash
	objectIter, err := ci.NewCursorIterator(tx, cursorValues, `select hash, bucket, path from object where object_type != 'nar'`)
	if err != nil {
		return fmt.Errorf("failed to create object iterator: %w", err)
	}

	batchSize := 1024 // todo make configurable
	rows := make([][]any, 0, batchSize)

	objectCount := 0
	deletionCount := 0

	flush := func() error {

		_, err := tx.CopyFrom(
			ctx,
			pgx.Identifier{deletionsTableName},
			[]string{"bucket", "path"},
			pgx.CopyFromRows(rows),
		)

		if err != nil {
			return fmt.Errorf("failed to copy rows: %w", err)
		}

		deletionCount += len(rows)

		// reset the batch
		rows = rows[:0]

		return nil
	}

	for objectIter.Next(ctx) {
		entry := cursorValues[objectIter.ValueIndex()]

		objectCount++

		if filter.Test([]byte(entry.Hash)) {
			// we need to keep this one
			continue
		}

		rows = append(rows, []any{entry.Bucket, entry.Path})

		if len(rows) == batchSize {
			if err = flush(); err != nil {
				return fmt.Errorf("failed to flush rows: %w", err)
			}
		}
	}

	// flush any remaining rows
	if err = flush(); err != nil {
		return fmt.Errorf("failed to flush rows: %w", err)
	}

	if objectIter.Error() != nil {
		return fmt.Errorf("failed to iterate objects: %w", objectIter.Error())
	}

	if err = objectIter.Close(ctx); err != nil {
		return fmt.Errorf("failed to close object iterator: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan %d created\n%d items processed\n%d items scheduled for deletion\n", planID, objectCount, deletionCount)

	return nil

}
