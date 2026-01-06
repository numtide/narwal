package gc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/queries"
	"github.com/parquet-go/parquet-go"
	"golang.org/x/sync/errgroup"
)

type Simple struct {
	cfg        *config.Config
	log        *log.Logger
	inputFile  string
	outputFile string
	dryRun     bool

	pool         *pgxpool.Pool
	bucketClient *awssdk.BucketClient

	missingCount atomic.Int32
}

func NewSimple(cfg *config.Config, inputFile, outputFile string, dryRun bool) *Simple {
	return &Simple{
		cfg:        cfg,
		log:        log.WithPrefix("gc"),
		inputFile:  inputFile,
		outputFile: outputFile,
		dryRun:     dryRun,
	}
}

func (s *Simple) Run(ctx context.Context) (*Stats, error) {
	cfg := s.cfg

	if s.dryRun {
		s.log.Info("dry-run mode enabled, no changes will be made")
	}

	var err error

	// Connect to the postgres database
	if s.pool, err = cfg.Postgres.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}
	defer s.pool.Close()

	// Create a bucket client
	if s.bucketClient, err = awssdk.NewBucketClientFromConfig(ctx, cfg.AWS, cfg.S3); err != nil {
		return nil, fmt.Errorf("failed to create bucket client: %w", err)
	}

	// Open the input file to start reading GC targets
	r, err := s.targetReader()
	if err != nil {
		return nil, err
	}

	// Create an errgroup for concurrent processing
	eg, egCtx := errgroup.WithContext(ctx)

	// Create a channel for GC records and start processing them first
	recordCh := make(chan *RemovalRecord, 1024)

	// Create a channel for stats collection
	statsCh := make(chan *Stats, 32)

	// Create an overall object for gathering all stats
	allStats := &Stats{}

	eg.Go(func() error {
		stats, processErr := s.processRecords(egCtx, recordCh, statsCh)
		if processErr != nil {
			return processErr
		}

		// Merge stats with overall stats
		allStats.Merge(stats)

		return nil
	})

	// Start processing batches
	processingWg := &sync.WaitGroup{}

LOOP:
	for {
		select {
		case <-ctx.Done():
			log.Debug("context cancelled, stopping reading GC targets")
			break LOOP

		default:
			batch := make([]inventory.NarInfoRecord, 128)

			n, err := r.Read(batch)

			if n > 0 {
				processingWg.Add(1)

				eg.Go(func() error {
					defer processingWg.Done()

					stats, removeErr := s.removeTargets(egCtx, batch[:n], recordCh)
					if removeErr == nil {
						// Send the stats to the main goroutine
						statsCh <- stats
					}

					return removeErr
				})
			}

			if errors.Is(err, io.EOF) {
				// end of the file
				break LOOP
			} else if err != nil {
				return nil, fmt.Errorf("failed to read GC targets: %w", err)
			}
		}
	}

	// Wait for all processing to finish before closing the channel
	processingWg.Wait()
	close(recordCh)

	// wait for the channel processing to complete
	if err = eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to process GC targets: %w", err)
	}

	return allStats, nil
}

func (s *Simple) processRecords(
	ctx context.Context,
	recordCh chan *RemovalRecord,
	statsCh chan *Stats,
) (*Stats, error) {
	filePath := s.outputFile

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // filePath from args
	if err != nil {
		return nil, fmt.Errorf("failed to open output file %s: %w", filePath, err)
	}

	w := parquet.NewWriter(f,
		parquet.Compression(&parquet.Zstd),
		parquet.MaxRowsPerRowGroup(20_000),
	)

	// track the unique store paths
	uniqueHashes := make(map[[32]byte]struct{})

	stats := Stats{}

	closedChannels := 0

LOOP:
	for closedChannels < 2 {
		select {
		case <-ctx.Done():
			break LOOP

		case s, ok := <-statsCh:
			if !ok {
				closedChannels++
				continue LOOP
			}

			stats.Merge(s)

		case record, ok := <-recordCh:
			if !ok {
				closedChannels++
				continue LOOP
			}

			s.log.Debugf("GC record: %s %s (%s)", record.StorePath, record.Key, record.Error)

			if writeErr := w.Write(record); writeErr != nil {
				return nil, fmt.Errorf("failed to write GC record: %w", writeErr)
			}

			switch {
			case strings.HasPrefix(record.Key, "nar/"):
				stats.Removals.Nars++

			case strings.HasSuffix(record.Key, ".narinfo"):
				stats.Removals.NarInfos++

			default:
				return nil, fmt.Errorf("unexpected GC record key: %s", record.Key)
			}

			// record the store path hash
			var hash [32]byte
			copy(hash[:], record.StorePath[11:43])
			uniqueHashes[hash] = struct{}{}
		}
	}

	if closeErr := w.Close(); closeErr != nil {
		return nil, fmt.Errorf("failed to close GC output file: %w", closeErr)
	}

	stats.StorePaths.Targets = len(uniqueHashes)

	s.log.Infof("processing complete: \n%v", stats)

	return &stats, nil
}

func (s *Simple) removeTargets(
	ctx context.Context,
	batch []inventory.NarInfoRecord,
	recordCh chan *RemovalRecord,
) (*Stats, error) {
	stats := &Stats{}

	s.log.Debugf("removing %d targets", len(batch))

	// The AWS bulk delete API supports up to 1000 objects at a time.
	// For each narinfo entry we will delete 0 or 1 nar files (multiple narinfos can point to the same nar file).
	// So we can safely batch up to 500 targets.
	if len(batch) > 500 {
		return nil, fmt.Errorf("batch size must be <= 500, got %d", len(batch))
	}

	// NarInfoRecord stores only the hash from the store path in binary format.
	// We want to reconstruct these store paths and index them by hash to make it easier to interoperate with the
	// `buildstepoutputs` table.
	storePaths := make([]string, len(batch))
	targetsByHash := make(map[string]inventory.NarInfoRecord, len(batch))

	for idx, target := range batch {
		// Convert hash from binary into nixbase32
		hash := make([]byte, 32)
		nixbase32.Encode(hash, target.Hash[:])

		// Index the target by hash to make it easier to look up later
		targetsByHash[string(hash)] = target

		// Construct the store path to match what will be in `buildstepoutputs` and add it to the list
		storePaths[idx] = fmt.Sprintf("/nix/store/%s-%s", string(hash), target.Pname)
	}

	// Get a DB connection from the pool
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire DB connection from pool: %w", err)
	}
	defer conn.Release()

	// Start a transaction
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to begin DB transaction: %w", err)
	}

	defer func() { _ = tx.Rollback(ctx) }()

	// Create a query helper object
	qry := queries.New(tx)

	// Check the db for the target paths
	storePathsInDB, err := qry.FindBuildStepOutputs(ctx, storePaths)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup buildstepoutputs: %w", err)
	}

	// Compare store paths in the db with targets provided by the targets file
	if err = s.logDiffBetweenTargetsAndDB(targetsByHash, storePathsInDB); err != nil {
		return nil, err
	}

	// A list of s3 object keys to delete
	keysToDelete := make([]string, 0, len(storePathsInDB)*2)

	// A record of what happened keyed by store path
	removalsByKey := make(map[string]*RemovalRecord, len(storePathsInDB)*2)

	// Iterate over the store paths found in the DB and generate some S3 object keys for deletion
	for _, storePath := range storePathsInDB {
		if !storePath.Valid {
			// This should not happen
			return nil, fmt.Errorf("invalid store path: %s", storePath.String)
		}

		// Extract the nar hash from the store path
		hash := storePath.String[11:43]

		// Lookup the target by hash
		target, ok := targetsByHash[hash]
		if !ok {
			// This should not be able to happen
			return nil, fmt.Errorf("target not found in batch: %s", storePath.String)
		}

		// Construct the S3 object keys to delete

		// First the narinfo file
		narInfoKey := hash + ".narinfo"

		keysToDelete = append(keysToDelete, narInfoKey)

		removalsByKey[narInfoKey] = &RemovalRecord{
			Key:       narInfoKey,
			StorePath: storePath.String,
		}

		// Followed by the nar file
		fileHash := nixbase32.EncodeToString(target.FileHash[:])

		narKey := "nar/" + fileHash + ".nar"
		if target.Compression != "" && target.Compression != "none" {
			narKey += target.Compression
		}

		keysToDelete = append(keysToDelete, narKey)

		removalsByKey[narKey] = &RemovalRecord{
			Key:       narKey,
			StorePath: storePath.String,
		}
	}

	// Remove the objects from S3 (unless dry-run)
	var bucketErrors map[string]types.Error
	if !s.dryRun {
		bucketErrors, err = s.bucketClient.RemoveObjects(ctx, keysToDelete)
		if err != nil {
			return nil, fmt.Errorf("failed to remove objects: %w", err)
		}
	}

	// Process the S3 errors
	failedStorePaths, err := s.processBucketErrors(bucketErrors, removalsByKey, stats)
	if err != nil {
		return nil, err
	}

	// Remove entries from the DB, filtering for store paths that were not successfully removed from S3
	storePathsToDelete := slices.DeleteFunc(slices.Clone(storePaths), func(s string) bool {
		return slices.Contains(failedStorePaths, s)
	})

	removed, err := qry.DeleteBuildStepOutputs(ctx, storePathsToDelete)
	if err != nil {
		return nil, fmt.Errorf("failed to delete build step outputs: %w", err)
	}

	if removed != int64(len(storePathsToDelete)) {
		return nil, fmt.Errorf("expected to remove %d entries, removed %d", len(storePaths), removed)
	}

	// Commit the TX (unless dry-run, in which case the deferred rollback will run)
	if !s.dryRun {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("failed to commit DB transaction: %w", err)
		}
	} else {
		s.log.Debug("dry-run: skipping commit, transaction will be rolled back")
	}

	s.log.Debugf("removed %d store paths comprised of %d bucket objects", len(storePaths), len(keysToDelete))

	// Feed the GC records into the channel
	for _, record := range removalsByKey {
		recordCh <- record
	}

	return stats, nil
}

func (s *Simple) processBucketErrors(
	bucketErrors map[string]types.Error,
	removalsByKey map[string]*RemovalRecord,
	stats *Stats,
) ([]string, error) {
	failedStorePaths := make([]string, 0, len(bucketErrors))

	for key, bucketErr := range bucketErrors {
		record, ok := removalsByKey[key]
		if !ok {
			return nil, fmt.Errorf(
				"unexpected error, could not find gc target for key %s: %s",
				key, aws.ToString(bucketErr.Message),
			)
		}

		if aws.ToString(bucketErr.Code) == "NoSuchKey" {
			// Likely we deleted this in a previous run, we do not consider this an error
			record.NotFound = true
			stats.StorePaths.MissingInS3++
		} else {
			// Otherwise record the error in the record and log it out
			record.Error = fmt.Sprintf(
				"s3 error: code = %s, message = %s",
				aws.ToString(bucketErr.Code), aws.ToString(bucketErr.Message),
			)

			s.log.Debugf(
				"failed to remove object from S3: key = %s, code = %s, message = %s",
				key, aws.ToString(bucketErr.Code), aws.ToString(bucketErr.Message),
			)
		}

		failedStorePaths = append(failedStorePaths, record.StorePath)
	}

	return failedStorePaths, nil
}

func (s *Simple) logDiffBetweenTargetsAndDB(
	targets map[string]inventory.NarInfoRecord,
	storePathsInDB []pgtype.Text,
) error {
	if len(targets) == len(storePathsInDB) {
		// nothing to do
		return nil
	}

	// Index storePathsInDB by hash for comparison
	actualHashes := make(map[string]string)

	for _, entry := range storePathsInDB {
		if !entry.Valid {
			return fmt.Errorf("invalid store path returned from DB: %s", entry.String)
		}

		// Hash -> Store Path
		actualHashes[entry.String[11:43]] = entry.String
	}

	// Compare and identify what was missing in the DB
	for hash, record := range targets {
		if _, ok := actualHashes[hash]; !ok {
			storePath := fmt.Sprintf("/nix/store/%s-%s", hash, record.Pname)
			s.log.Warnf("missing store path in buildstepoutputs table: %s", storePath)

			s.missingCount.Add(1)
		}
	}

	return nil
}

func (s *Simple) targetReader() (*parquet.GenericReader[inventory.NarInfoRecord], error) {
	filePath := s.inputFile

	s.log.Infof("reading GC targets from %s", filePath)

	f, err := os.Open(filePath) //nolint:gosec // filePath from args
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	// We expect a NarInfoRecord schema
	schema := parquet.SchemaOf(inventory.NarInfoRecord{})

	return parquet.NewGenericReader[inventory.NarInfoRecord](f, schema), nil
}
