package gc

import (
	"context"
	"fmt"
	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"strconv"
	"strings"
	"time"

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

	count := 0

	objects := make([]minio.ObjectInfo, 1024)
	cursorValues := make([]deletionEntry, 1024)

	for {

		n, deleteErr := tryDelete(ctx, conn, int32(planID), objects, cursorValues)
		if deleteErr != nil {
			return fmt.Errorf("failed to apply plan: %w", deleteErr)
		}

		log.Infof("deleted %d objects", n)

		count += n

		if n == 0 {
			break
		}
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted %d objects\n", count)

	return nil
}

func tryDelete(
	ctx context.Context,
	conn *pgxpool.Conn,
	planID int32,
	objects []minio.ObjectInfo,
	cursorValues []deletionEntry,
) (n int, err error) {
	// start a tx
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer tx.Rollback(ctx)

	if n, err = readDeletions(ctx, tx, planID, objects, cursorValues); err != nil {
		return 0, fmt.Errorf("failed to read deletions: %w", err)
	}

	if n > 0 {
		timestamp := time.Now().UTC()

		removedPaths, failedPaths := removeFromS3(ctx, objects[:n])

		// record the successes
		deletionsTable := deletionsTableName(planID)

		updateResult, updateErr := tx.Exec(
			ctx,
			fmt.Sprintf(`update %s set applied_at = $1 where path = any($2)`, deletionsTable),
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
	objects []minio.ObjectInfo,
	cursorValues []deletionEntry,
) (n int, err error) {
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
	for idx < len(objects) && iter.Next(ctx) {
		objects[idx].Key = cursorValues[iter.ValueIndex()].Path
		idx += 1
	}

	if iter.Error() != nil {
		return 0, fmt.Errorf("failure whilst iterating the deletions table: %w", iter.Error())
	}

	return idx, nil
}

func removeFromS3(
	ctx context.Context,
	objects []minio.ObjectInfo,
) (successes []string, failures map[string]error) {

	objectsCh := make(chan minio.ObjectInfo, len(objects))

	go func() {
		for idx := range objects {
			objectsCh <- objects[idx]
		}

		close(objectsCh)
	}()

	errCh := s3.RemoveObjects(ctx, cfg.S3.BucketName, objectsCh, minio.RemoveObjectsOptions{})

	failures = make(map[string]error)

	for removeErr := range errCh {
		failures[removeErr.ObjectName] = removeErr.Err
		log.Errorf("failed to remove object '%s': %s", removeErr.ObjectName, removeErr.Err)
	}

	// construct success list
	for _, object := range objects {
		if _, ok := failures[object.Key]; !ok {
			successes = append(successes, object.Key)
		}
	}

	return successes, failures
}
