package plan

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/db"

	ci "github.com/Eun/go-pgx-cursor-iterator/v2"
	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

type deletionEntry struct {
	Path string `db:"path"`
}

func planApply() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a GC plan e.g. DELETE THINGS FROM THE CACHE!",
		Args:  cobra.ExactArgs(1),
		RunE:  applyPlan,
	}

	cmd.SilenceUsage = true

	return cmd
}

func applyPlan(cmd *cobra.Command, args []string) error {
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

	queries := db.New(conn)

	// check the plan hasn't already been applied successfully
	plan, err := queries.GetGCPlan(ctx, int32(planID))
	if err != nil {
		return fmt.Errorf("gc plan %d not found: %w", planID, err)
	}

	if plan.CompletedAt.Valid {
		return fmt.Errorf("gc plan %d has already been applied successfully at %v", planID, plan.CompletedAt.Time)
	}

	// summarize
	summary, err := summarizePlan(ctx, conn, int32(planID))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(
		cmd.OutOrStdout(),
		"plan %d has %d objects in total, %d to delete\n",
		planID, summary.total, summary.total-summary.completed,
	)

	deleted := 0

	keys := make([]string, 1024)
	cursorValues := make([]deletionEntry, 1024)

	for {
		n, deleteErr := tryDelete(ctx, conn, int32(planID), keys, cursorValues)
		if deleteErr != nil {
			return fmt.Errorf("failed to apply plan: %w", deleteErr)
		}

		log.Infof("deleted %d objects", n)

		deleted += n

		if n == 0 {
			break
		}
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted %d objects\n", deleted)

	if summary, err = summarizePlan(ctx, conn, int32(planID)); err != nil {
		return err
	}

	if summary.total == summary.completed {
		if err = queries.SetGCPlanAsCompleted(ctx, int32(planID)); err != nil {
			return fmt.Errorf("failed to set gc plan as completed: %w", err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "gc plan %d applied successfully\n", planID)

		return nil
	}

	return fmt.Errorf("failed to apply gc plan, %d/%d objects were not deleted", summary.incomplete, summary.total)
}

func tryDelete(
	ctx context.Context,
	conn *pgxpool.Conn,
	planID int32,
	keys []string,
	cursorValues []deletionEntry,
) (int, error) {
	// start a tx
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	//nolint:errcheck
	defer tx.Rollback(ctx)

	n, err := readDeletions(ctx, tx, planID, keys, cursorValues)
	if err != nil {
		return 0, fmt.Errorf("failed to read deletions: %w", err)
	}

	if n > 0 { //nolint:nestif
		timestamp := time.Now().UTC()

		removedPaths, failedPaths := removeFromS3(ctx, keys[:n])

		// record the successes
		deletionsTable := deletionsTableName(planID)

		updateResult, updateErr := tx.Exec(
			ctx,
			fmt.Sprintf(`update %s set applied_at = $1, error = null where path = any($2)`, deletionsTable),
			timestamp, removedPaths,
		)
		if updateErr != nil {
			return 0, fmt.Errorf("failed to update deletions table: %w", updateErr)
		}

		if updateResult.RowsAffected() != int64(len(removedPaths)) {
			return 0, fmt.Errorf(
				"failed to update deletions table: %d rows affected, expected %d",
				updateResult.RowsAffected(), len(removedPaths),
			)
		}

		// record the failures
		batch := pgx.Batch{}

		for path, removeErr := range failedPaths {
			batch.Queue(
				fmt.Sprintf("update %s set error = $1 where path = $2", deletionsTable),
				removeErr.Error(), path,
			)
		}

		results := tx.SendBatch(ctx, &batch)
		if updateErr = results.Close(); updateErr != nil {
			return 0, fmt.Errorf("failed to update deletions table: %w", updateErr)
		}

		// delete the associated objects from the object table
		updateResult, updateErr = tx.Exec(ctx, `delete from object where path = any($1)`, removedPaths)
		if updateErr != nil {
			return 0, fmt.Errorf("failed to delete objects from object table: %w", updateErr)
		}

		if updateResult.RowsAffected() != int64(len(removedPaths)) {
			return 0, fmt.Errorf(
				"failed to delete objects from object table: %d rows affected, expected %d",
				updateResult.RowsAffected(), len(removedPaths),
			)
		}

		// delete nar info entries
		var narInfoHashes []string

		for _, path := range removedPaths {
			if strings.HasSuffix(path, ".narinfo") {
				narInfoHashes = append(narInfoHashes, path[0:32])
			}
		}

		updateResult, updateErr = tx.Exec(ctx, `delete from nar_info where hash = any($1)`, narInfoHashes)
		if updateErr != nil {
			return 0, fmt.Errorf("failed to delete nar info entries: %w", updateErr)
		}

		if updateResult.RowsAffected() != int64(len(narInfoHashes)) {
			return 0, fmt.Errorf(
				"failed to delete nar info entries: %d rows affected, expected %d",
				updateResult.RowsAffected(), len(narInfoHashes),
			)
		}
	}

	// commit changes to the db
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return n, nil
}

func readDeletions(
	ctx context.Context,
	tx pgx.Tx,
	planID int32,
	keys []string,
	cursorValues []deletionEntry,
) (int, error) {
	query := fmt.Sprintf(
		`select path from %s where applied_at is null and error is null`,
		deletionsTableName(planID),
	)

	iter, err := ci.NewCursorIterator(tx, cursorValues, query)
	if err != nil {
		return 0, fmt.Errorf("failed to create deletions iterator: %w", err)
	}

	idx := 0

	// only consume up until we fill the batch
	for idx < len(keys) && iter.Next(ctx) {
		keys[idx] = cursorValues[iter.ValueIndex()].Path
		idx += 1
	}

	if iter.Error() != nil {
		return 0, fmt.Errorf("failure whilst iterating the deletions table: %w", iter.Error())
	}

	return idx, nil
}

//nolint:nonamedreturns
func removeFromS3(
	ctx context.Context,
	keys []string,
) (successes []string, failures map[string]error) {
	failures, err := s3.RemoveObjects(ctx, keys)
	if err != nil {
		// If we get an error, treat all keys as failed
		failures = make(map[string]error)
		for _, key := range keys {
			failures[key] = err
		}
	}

	if failures == nil {
		failures = make(map[string]error)
	}

	// Log individual failures
	for key, removeErr := range failures {
		log.Errorf("failed to remove object '%s': %s", key, removeErr)
	}

	// construct success list
	for _, key := range keys {
		if _, ok := failures[key]; !ok {
			successes = append(successes, key)
		}
	}

	return successes, failures
}
