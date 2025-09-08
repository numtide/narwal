package plan

import (
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/numtide/narwal/pkg/db"

	ci "github.com/Eun/go-pgx-cursor-iterator/v2"
	"github.com/bits-and-blooms/bloom/v3"
	"github.com/spf13/cobra"
)

type closureEntry struct {
	Hash       string  `db:"hash"`
	ObjectType string  `db:"object_type"`
	Path       string  `db:"path"`
	NarUrl     *string `db:"nar_url"`
}

func planCreate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a GC plan",
		Args:  cobra.NoArgs,
		RunE:  createPlan,
	}

	cmd.SilenceUsage = true

	return cmd
}

//nolint:gocognit
func createPlan(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

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

	// insert a new plan entry
	planID, err := queries.InsertGCPlan(ctx)
	if err != nil {
		return fmt.Errorf("failed to insert gc plan: %w", err)
	}

	// create a table to store the deletions we need to make
	if err := addDeletionsTable(ctx, tx, planID); err != nil {
		return err
	}

	// generate a new root closure table with the list of all nar hashes we need to retain
	// this is done via a function within the db to avoid round-trip latency
	if _, err = tx.Exec(ctx, `select generate_gc_root_closure($1)`, planID); err != nil {
		return fmt.Errorf("failed to generate gc root closure: %w", err)
	}

	// create a bloom filter to filter out all nar hashes we don't need to retain'
	// todo make parameters configurable
	// current value is based on cache.nixos.org total nars
	filter := bloom.NewWithEstimates(275000000, 0.01)

	batchSize := 1024 // todo make configurable

	// stream each entry in the closure table and add it to the filter
	cursorValues := make([]closureEntry, batchSize)
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
	objectIter, err := ci.NewCursorIterator(
		tx, cursorValues,
		`
			select 
			    o.hash, o.object_type, o.path, ni.url as nar_url 
			from object o 
			left join nar_info ni on o.hash = ni.hash 
			where o.object_type != 'nar'`,
	)
	if err != nil {
		return fmt.Errorf("failed to create object iterator: %w", err)
	}

	rows := make([][]any, 0, batchSize)
	narPaths := make([]string, 0, batchSize)

	objectCount := 0
	deletionCount := 0

	deletionsTable := deletionsTableName(planID)

	columnNames := []string{"path"}
	tableIdentifier := pgx.Identifier{deletionsTable}

	// copy paths into the deletion table in batches
	flushRows := func() error {
		_, copyErr := tx.CopyFrom(ctx, tableIdentifier, columnNames, pgx.CopyFromRows(rows))
		if copyErr != nil {
			return fmt.Errorf("failed to copy rows: %w", copyErr)
		}

		deletionCount += len(rows)

		// reset
		rows = rows[:0]

		return nil
	}

	flushNarPaths := func() error {
		// I've observed multiple narinfo's pointing to the same nar, so we insert them differently
		batch := pgx.Batch{}
		for _, path := range narPaths {
			batch.Queue(
				fmt.Sprintf(`insert into %s (path) values ($1) on conflict (path) do nothing`, deletionsTable),
				path,
			)
		}

		results := tx.SendBatch(ctx, &batch)
		if batchErr := results.Close(); batchErr != nil {
			return fmt.Errorf("failed to send batch: %w", batchErr)
		}

		deletionCount += len(narPaths)

		// reset
		narPaths = narPaths[:0]

		return nil
	}

	// iterate the closure and compare with the filter
	// if the hash is in the filter, then we need to keep it
	// if not, we can delete it
	for objectIter.Next(ctx) {
		entry := cursorValues[objectIter.ValueIndex()]

		objectCount++

		if filter.Test([]byte(entry.Hash)) {
			// we need to keep this one
			continue
		}

		rows = append(rows, []any{entry.Path})

		// we get the nar url from the nar_info table if it exists
		if entry.ObjectType == "narinfo" && entry.NarUrl != nil {
			narPaths = append(narPaths, *entry.NarUrl)
			objectCount++
		}

		if len(rows) == batchSize {
			if err = flushRows(); err != nil {
				return fmt.Errorf("failed to flush rows: %w", err)
			}
		}

		if len(narPaths) == batchSize {
			if err = flushNarPaths(); err != nil {
				return fmt.Errorf("failed to flush nar paths: %w", err)
			}
		}
	}

	// flush any remaining paths
	if err = flushRows(); err != nil {
		return fmt.Errorf("failed to flush rows: %w", err)
	}

	if err = flushNarPaths(); err != nil {
		return fmt.Errorf("failed to flush nar paths: %w", err)
	}

	if objectIter.Error() != nil {
		return fmt.Errorf("failed to iterate objects: %w", objectIter.Error())
	}

	if err = objectIter.Close(ctx); err != nil {
		return fmt.Errorf("failed to close object iterator: %w", err)
	}

	// commit changes to the db
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	_, _ = fmt.Fprintf(
		cmd.OutOrStdout(),
		"plan %d created\n%d items processed\n%d items scheduled for deletion\n",
		planID, objectCount, deletionCount,
	)

	return nil
}
