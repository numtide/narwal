package gc

import (
	"context"
	"fmt"
	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"golang.org/x/sync/errgroup"
	"strconv"
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

	eg := errgroup.Group{}

	count := 0

	for {

		objects := make([]minio.ObjectInfo, 1024)
		cursorValues := make([]deletionEntry, 1024)

		n, deleteErr := tryDelete(ctx, conn, &eg, int32(planID), objects, cursorValues)
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
	eg *errgroup.Group,
	planID int32,
	objects []minio.ObjectInfo,
	cursorValues []deletionEntry,
) (n int, err error) {
	// start a tx
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer tx.Rollback(ctx)

	if n, err = readDeletions(ctx, tx, planID, objects, cursorValues); err != nil {
		return 0, fmt.Errorf("failed to read deletions: %w", err)
	}

	if n > 0 {

		timestamp := time.Now().UTC()

		failures, err := removeFromS3(ctx, eg, objects[:n])
		if err != nil {
			return 0, fmt.Errorf("failed to remove objects: %w", err)
		}

		_, updateErr := tx.Exec(
			ctx,
			fmt.Sprintf(`update %s set applied_at = $1 where path != all($2)`, deletionsTableName(planID)),
			timestamp, failures,
		)

		if updateErr != nil {
			return 0, fmt.Errorf("failed to update deletions table: %w", err)
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
		`select path from %s where applied_at is null`,
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
	eg *errgroup.Group,
	paths []minio.ObjectInfo,
) ([]string, error) {

	objectsCh := make(chan minio.ObjectInfo, len(paths))

	var failures []string

	eg.Go(func() error {
		errCh := s3.RemoveObjects(ctx, cfg.S3.BucketName, objectsCh, minio.RemoveObjectsOptions{})

		for err := range errCh {
			failures = append(failures, err.ObjectName)
			log.Errorf("failed to remove object '%s': %s", err.ObjectName, err.Err)
		}

		if len(failures) > 0 {
			return fmt.Errorf("failed to remove %d objects", len(failures))
		}

		return nil
	})

	for idx := range paths {
		objectsCh <- paths[idx]
	}

	close(objectsCh)

	return failures, eg.Wait()
}
