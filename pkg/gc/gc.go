package gc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/charmbracelet/log"
	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/parquet-go/parquet-go"
	"golang.org/x/sync/errgroup"
)

type Simple struct {
	cfg        *config.Config
	log        *log.Logger
	inputFile  string
	outputFile string
	dryRun     bool

	bucketClient *awssdk.BucketClient
}

func NewSimple(cfg *config.Config, inputFile, outputFile string, dryRun bool) *Simple {
	return &Simple{
		cfg:        cfg,
		log:        log.WithPrefix("gc_simple"),
		inputFile:  inputFile,
		outputFile: outputFile,
		dryRun:     dryRun,
	}
}

func (s *Simple) Run(ctx context.Context) (*Stats, error) {
	cfg := s.cfg

	s.log.Info("starting simple garbage collection")

	if s.dryRun {
		s.log.Info("dry-run mode enabled, no changes will be made")
	}

	var err error

	// Create a bucket client
	if s.bucketClient, err = awssdk.NewBucketClientFromConfig(ctx, cfg.AWS, cfg.S3); err != nil {
		return nil, fmt.Errorf("failed to create bucket client: %w", err)
	}

	// Open the input file to start reading GC targets
	r, err := s.targetsReader()
	if err != nil {
		return nil, err
	}

	// Create an errgroup for concurrent processing
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(16)

	// Create some channels for gathering records and stats as things are processed
	statsCh := make(chan *Stats, 1024)
	recordCh := make(chan *RemovalRecord, 1024)

	// Create a global stats object we will merge into
	allStats := &Stats{
		DryRun: s.dryRun,
	}

	// We use a wait group to know when all batches have been processed since we don't know ahead of time how many
	// there will be
	processingWg := &sync.WaitGroup{}

	// log progress periodically
	progressCtx, cancelProgress := context.WithCancel(egCtx)
	defer cancelProgress()

	eg.Go(s.progressLogger(progressCtx, allStats))
	eg.Go(s.statsProcessor(ctx, statsCh, allStats, cancelProgress))
	eg.Go(func() error {
		return s.processRemovalRecords(egCtx, recordCh, statsCh)
	})

	// Process batches from the input file
	if err = s.processBatches(ctx, egCtx, r, eg, processingWg, recordCh, statsCh); err != nil {
		return nil, err
	}

	// Wait for all processing to finish before closing the channels
	processingWg.Wait()

	close(recordCh)

	// Wait for the record processing to complete
	if err = eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to process GC targets: %w", err)
	}

	return allStats, nil
}

func (s *Simple) progressLogger(ctx context.Context, allStats *Stats) func() error {
	return func() error {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case _, ok := <-t.C:
				if !ok {
					return nil
				}

				log.Infof("progress:\n%s", allStats)
			}
		}
	}
}

func (s *Simple) statsProcessor(
	ctx context.Context,
	statsCh chan *Stats,
	allStats *Stats,
	cancelProgress context.CancelFunc,
) func() error {
	return func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case stats, ok := <-statsCh:
				if !ok {
					cancelProgress() // cancel progress logging
					return nil
				}

				allStats.Merge(stats)
			}
		}
	}
}

func (s *Simple) processBatches(
	ctx context.Context,
	egCtx context.Context,
	r *parquet.GenericReader[inventory.NarInfoRecord],
	eg *errgroup.Group,
	processingWg *sync.WaitGroup,
	recordCh chan *RemovalRecord,
	statsCh chan *Stats,
) error {
	for {
		select {
		case <-ctx.Done():
			log.Debug("context cancelled, stopping")
			return nil

		default:
			// Read the next batch of targets
			batch := make([]inventory.NarInfoRecord, 500)

			n, err := r.Read(batch)

			// If we read any targets, submit them for removal
			if n > 0 {
				// Bump wait group
				processingWg.Add(1)

				eg.Go(func() error {
					// Indicate this goroutine has finished when we're done
					defer processingWg.Done()

					// Remove the targets
					stats, removeErr := s.removeTargets(egCtx, batch[:n], recordCh)
					if removeErr == nil {
						// Send the stats to the main goroutine
						statsCh <- stats
					}

					return removeErr
				})
			}

			// Check for EOF
			if errors.Is(err, io.EOF) {
				return nil
			} else if err != nil {
				return fmt.Errorf("failed to read GC targets: %w", err)
			}
		}
	}
}

func (s *Simple) processRemovalRecords(
	ctx context.Context,
	recordCh chan *RemovalRecord,
	statsCh chan *Stats,
) error {
	defer close(statsCh)

	filePath := s.outputFile

	// Open the input file contain narinfos
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open output file %s: %w", filePath, err)
	}

	// Open a new parquet writer for the results
	w := parquet.NewWriter(f,
		parquet.Compression(&parquet.Zstd),
		parquet.MaxRowsPerRowGroup(20_000),
	)

	// Track the unique store paths
	uniqueHashes := make(map[[32]byte]struct{})

LOOP:
	for {
		select {
		case <-ctx.Done():
			break LOOP

		case record, ok := <-recordCh:
			if !ok {
				break LOOP
			}

			s.log.Debugf("GC record: %s %s (%s)", record.StorePath, record.Key, record.Error)

			if writeErr := w.Write(record); writeErr != nil {
				return fmt.Errorf("failed to write GC record: %w", writeErr)
			}

			// record the store path hash
			var hash [32]byte
			copy(hash[:], record.StorePath[11:43])
			uniqueHashes[hash] = struct{}{}

			// update stats (count even in dry-run to track what would be removed)
			stats := &Stats{}

			switch {
			case strings.HasPrefix(record.Key, "nar/"):
				stats.Removed.Nars++

			case strings.HasSuffix(record.Key, ".narinfo"):
				stats.Removed.NarInfos++

			default:
				return fmt.Errorf("unexpected GC record key: %s", record.Key)
			}

			statsCh <- stats
		}
	}

	// Close the writer and output file
	if closeErr := w.Close(); closeErr != nil {
		return fmt.Errorf("failed to close GC output file: %w", closeErr)
	}

	return nil
}

func (s *Simple) removeTargets(
	ctx context.Context,
	batch []inventory.NarInfoRecord,
	recordCh chan *RemovalRecord,
) (*Stats, error) {
	stats := &Stats{}

	s.log.Debugf("attempting to remove %d targets", len(batch))

	// The AWS bulk delete API supports up to 1000 objects at a time.
	// For each narinfo entry we will delete 0 or 1 nar files (multiple narinfos can point to the same nar file).
	// So we can safely batch up to 500 targets.
	if len(batch) > 500 {
		return nil, fmt.Errorf("batch size must be <= 500, got %d", len(batch))
	}

	// A list of s3 object keys to delete
	keysToDelete := make([]string, 0, len(batch)*2)

	// A record of what happened keyed by S3 key
	removalsByKey := make(map[string]*RemovalRecord, len(batch)*2)

	// Build S3 keys directly from the batch items
	for _, target := range batch {
		// Convert hash from binary into nixbase32
		hash := make([]byte, 32)
		nixbase32.Encode(hash, target.Hash[:])
		hashStr := string(hash)

		// Construct the store path
		storePath := fmt.Sprintf("/nix/store/%s-%s", hashStr, target.Pname)

		// First the narinfo file
		narInfoKey := hashStr + ".narinfo"

		keysToDelete = append(keysToDelete, narInfoKey)

		removalsByKey[narInfoKey] = &RemovalRecord{
			Key:       narInfoKey,
			StorePath: storePath,
		}

		stats.Targets.NarInfos++

		// Followed by the nar file
		fileHash := nixbase32.EncodeToString(target.FileHash[:])

		narKey := "nar/" + fileHash + ".nar"
		if target.Compression != "" && target.Compression != "none" {
			narKey += "." + target.Compression
		}

		keysToDelete = append(keysToDelete, narKey)

		removalsByKey[narKey] = &RemovalRecord{
			Key:       narKey,
			StorePath: storePath,
		}
	}

	// Remove the objects from S3 (unless dry-run)
	var (
		bucketErrors map[string]types.Error
		err          error
	)

	if s.dryRun {
		// For a dry run we just check if the keys exist
		bucketErrors, err = s.bucketClient.StatObjects(ctx, keysToDelete)
		if err != nil {
			return nil, fmt.Errorf("failed to stat objects: %w", err)
		}
	} else {
		// Otherwise, we remove the objects from S3
		bucketErrors, err = s.bucketClient.RemoveObjects(ctx, keysToDelete)
		if err != nil {
			return nil, fmt.Errorf("failed to remove objects: %w", err)
		}
	}

	// Process the S3 errors
	if err = s.processBucketErrors(bucketErrors, removalsByKey, stats); err != nil {
		return nil, err
	}

	s.log.Debug(
		"processed targets",
		"dry_run", s.dryRun,
		"batch_size", len(batch),
		"keys_to_delete", len(keysToDelete),
		"errors", len(bucketErrors),
	)

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
) error {
	for key, bucketErr := range bucketErrors {
		record, ok := removalsByKey[key]
		if !ok {
			return fmt.Errorf(
				"unexpected error, could not find gc target for key %s: %s",
				key, aws.ToString(bucketErr.Message),
			)
		}

		if aws.ToString(bucketErr.Code) == "NoSuchKey" {
			// Likely we deleted this in a previous run, we do not consider this an error
			record.NotFound = true

			switch {
			case strings.HasPrefix(key, "nar/"):
				stats.MissingInS3.Nars++
			case strings.HasSuffix(key, ".narinfo"):
				stats.MissingInS3.NarInfos++
			default:
				return fmt.Errorf("unexpected record key: %s", key)
			}
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
	}

	return nil
}

func (s *Simple) targetsReader() (*parquet.GenericReader[inventory.NarInfoRecord], error) {
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
