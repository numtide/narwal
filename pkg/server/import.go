package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/pb"
	"github.com/dgraph-io/ristretto/v2/z"
	"github.com/dustin/go-humanize"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/parquet-go/parquet-go"
	"golang.org/x/sync/errgroup"
)

//nolint:gochecknoglobals
var ignoredPrefixes = []string{
	".well-known/",   // contains a single file, .well-known/pki-validation/gsdv.txt
	"index.html",     // landing page for the cache
	"binary-cache/",  // empty directory
	"nix-cache-info", // static binary cache config file
	"error-pages/",   // static error pages
}

func shouldIgnorePath(path string) bool {
	// Paths that will be ignored when importing manifest files
	for _, prefix := range ignoredPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	return false
}

// Import takes a path to a daily manifest and imports all the objects and narinfos it references into Postgres.
// Any existing objects will be overwritten.
// It returns an error if any of the steps fail.
func Import(ctx context.Context, cfg *config.Config, report string) error {
	// open the inventory db and clean up when we're done
	if cfg.Badger == nil {
		return errors.New("badger config is required")
	}

	inventoryDB, err := inventory.OpenDB(cfg.Badger)
	if err != nil {
		return fmt.Errorf("failed to open inventoryDB: %w", err)
	}

	defer func() {
		if closeErr := inventoryDB.Close(); closeErr != nil {
			log.Errorf("failed to close inventoryDB: %s", closeErr)
		}
	}()

	// Connect to Postgres and clean up when we're done
	pgPool, err := cfg.Postgres.Connect(ctx, true)
	if err != nil {
		//nolint:wrapcheck
		return err
	}

	// Clean up when done
	defer pgPool.Close()

	// Import the manifest
	if err = importManifest(ctx, inventoryDB, pgPool, report); err != nil {
		return fmt.Errorf("failed to import manifest %s: %w", report, err)
	}

	return nil
}

// importManifest imports all the files referenced by a manifest into Postgres.
func importManifest(
	ctx context.Context,
	inventoryDB *badger.DB,
	pgPool *pgxpool.Pool,
	report string,
) error {
	var (
		err      error
		manifest *inventory.Manifest
	)

	// Read the manifest from the inventory db
	if err := inventoryDB.View(func(tx *badger.Txn) error {
		manifest, err = inventory.GetManifest(tx, report)
		if err != nil {
			return fmt.Errorf("failed to get manifest: %w", err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to view inventory: %w", err)
	}

	// Create a new type wrapper for postgres queries
	queries := db.New(pgPool)

	// Files referenced by the manifest are stored by their UUID
	// We record the UUID of a manifest file after importing so we can skip it when re-importing
	ids := make([]string, 0, len(manifest.Files))

	for _, file := range manifest.Files {
		ids = append(ids, file.UUID())
	}

	importedIDs, err := queries.ListImportedManifestFiles(ctx, ids)
	if err != nil {
		return fmt.Errorf("failed to list imported manifest files: %w", err)
	}

	importedSet := make(map[string]struct{}, len(importedIDs))
	for _, basename := range importedIDs {
		importedSet[basename] = struct{}{}
	}

	skipped := 0

	// Import each manifest file
LOOP:
	for idx, file := range manifest.Files {
		select {
		// Check if the context has been cancelled and break early
		case <-ctx.Done():
			break LOOP

		default:
			// Otherwise, import the file
			l := log.WithPrefix(fmt.Sprintf("[%d / %d]", idx+1, len(manifest.Files)))

			// Skip files that have already been imported
			if _, imported := importedSet[file.UUID()]; imported {
				l.Infof("skipping already-imported manifest file %s", file.UUID())

				skipped++

				continue
			}

			if err = importManifestFile(ctx, inventoryDB, pgPool, file, l); err != nil {
				return fmt.Errorf("failed to import manifest file %s: %w", file.Key, err)
			}
		}
	}

	// Log the number of skipped files
	if skipped > 0 {
		log.Infof("skipped %d manifest files that were already imported", skipped)
	}

	return nil
}

type objectWithMetadata struct {
	obj         inventory.Object
	hash        string
	compression string
}

// importManifestFile imports a single manifest file into Postgres.
func importManifestFile(
	ctx context.Context,
	inventoryDB *badger.DB,
	pgPool *pgxpool.Pool,
	file inventory.ManifestFile,
	l *log.Logger,
) error {
	var (
		err error
		buf []byte
	)

	// Record start time
	startedAt := time.Now()

	l.Infof("importing manifest file %s", file.UUID())

	// Read the bytes of the parquet file from the inventory db
	if err = inventoryDB.View(func(tx *badger.Txn) error {
		buf, err = inventory.GetManifestFile(tx, &file)
		if err != nil {
			return fmt.Errorf("failed to get manifest file: %w", err)
		}

		return nil
	}); err != nil {
		return err //nolint:wrapcheck
	}

	// Parse the parquet file into memory
	objects, err := parquet.Read[inventory.Object](bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return fmt.Errorf("failed to read parquet file: %w", err)
	}

	// Track the number of objects imported
	var totalImported atomic.Uint64

	// Create an error group for concurrent imports
	eg, egCtx := errgroup.WithContext(ctx)
	// We have up to 128 partitions to play with so there's opportunity for a lot of concurrency
	eg.SetLimit(32)

	// Batch objects by object_type for partition-aware imports in Postgres
	batches := make(map[db.ObjectType][]objectWithMetadata)

	// Import objects in batches of 25000
	const batchSize = 25000

	// A helper function to flush a batch of objects to Postgres once the batch size is reached
	flushBatch := func(objectType db.ObjectType, batch []objectWithMetadata) {
		eg.Go(func() error {
			n, err := importBatch(egCtx, objectType, batch, pgPool)
			if err != nil {
				return fmt.Errorf("failed to import batch for %s: %w", objectType, err)
			}

			// Update the total number of objects imported
			totalImported.Add(uint64(n)) //nolint:gosec

			return nil
		})
	}

	// TODO: Object import is commented out while focusing on narinfo import
	// // Before importing we analyse the path of each object to determine its object_type
	// for _, obj := range objects {
	// 	if shouldIgnorePath(obj.Key) {
	// 		continue
	// 	}
	//
	// 	// Analyse the path
	// 	analysis, err := examinePath(obj.Key)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to analyze path '%s': %w", obj.Key, err)
	// 	}
	//
	// 	hash, err := hashFromPath(obj.Key, analysis.ObjectType)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to get hash from path %s: %w", obj.Key, err)
	// 	}
	//
	// 	// Append to the correct batch
	// 	batches[analysis.ObjectType] = append(batches[analysis.ObjectType], objectWithMetadata{
	// 		obj:         obj,
	// 		hash:        hash,
	// 		compression: string(analysis.Compression),
	// 	})
	//
	// 	// Continue to the next object if the batch isn't full
	// 	if len(batches[analysis.ObjectType]) < batchSize {
	// 		continue
	// 	}
	//
	// 	// Otherwise, flush the batch and clear it
	// 	flushBatch(analysis.ObjectType, batches[analysis.ObjectType])
	// 	delete(batches, analysis.ObjectType)
	// }
	//
	// // Flush any remaining partial batches
	// for objectType, batch := range batches {
	// 	if len(batch) > 0 {
	// 		flushBatch(objectType, batch)
	// 	}
	// }
	//
	// // Wait for all pending import jobs to complete
	// if err = eg.Wait(); err != nil {
	// 	return fmt.Errorf("failed to import objects: %w", err)
	// }

	// TODO: Add narinfo import here
	_ = objects
	_ = batches
	_ = batchSize
	_ = flushBatch
	_ = eg

	// Calculate the import duration and rate
	duration := time.Since(startedAt)
	rate := float64(totalImported.Load()) / duration.Seconds()

	// Log an import summary
	l.Info("importing complete",
		"objects", humanize.Comma(int64(totalImported.Load())), //nolint:gosec
		"duration", duration,
		"objects_per_second", humanize.CommafWithDigits(rate, 0),
	)

	// Mark the manifest file as imported in Postgres
	queries := db.New(pgPool)
	if err = queries.MarkManifestFileAsImported(ctx, db.MarkManifestFileAsImportedParams{
		Basename:    file.UUID(),
		Md5Checksum: file.MD5Checksum,
		Size:        int64(file.Size), //nolint:gosec
	}); err != nil {
		return fmt.Errorf("failed to mark manifest file as imported: %w", err)
	}

	return nil
}

func importBatch(
	ctx context.Context,
	objectType db.ObjectType,
	batch []objectWithMetadata,
	pgPool *pgxpool.Pool,
) (int, error) {
	// skip if the batch is empty
	if len(batch) == 0 {
		return 0, nil
	}

	// Acquire a Postgres connection from the pool and release it when we're done
	conn, err := pgPool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire postgres connection: %w", err)
	}

	defer conn.Release()

	// Begin a transaction
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to begin postgres transaction: %w", err)
	}

	defer func() {
		// Ensures the transaction is rolled back when the method returns
		// If tx.Commit() was called before the function returned, this has no effect
		_ = tx.Rollback(ctx)
	}()

	// Disable synchronous commit for this transaction to reduce WAL pressure
	if _, err = tx.Exec(ctx, "SET LOCAL synchronous_commit = off"); err != nil {
		return 0, fmt.Errorf("failed to set synchronous_commit: %w", err)
	}

	// Collect the object hashes and paths for the DELETE statement
	hashes := make([]string, len(batch))
	paths := make([]string, len(batch))

	for i, p := range batch {
		hashes[i] = p.hash
		paths[i] = p.obj.Key
	}

	// Delete existing objects using partition key for efficient pruning
	_, err = tx.Exec(ctx, `
		DELETE FROM object
		WHERE object_type = $1
		AND (hash, path) IN (SELECT unnest($2::varchar[]), unnest($3::varchar[]))`,
		objectType, hashes, paths,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to delete existing objects: %w", err)
	}

	// Copy the processed objects into Postgres
	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"object"},
		[]string{"hash", "object_type", "compression_type", "path", "size", "created_at"},
		pgx.CopyFromSlice(len(batch), func(idx int) ([]any, error) {
			p := batch[idx]

			return []any{
				p.hash,
				objectType,
				p.compression,
				p.obj.Key,
				p.obj.Size,
				time.UnixMilli(p.obj.LastModifiedDate).UTC(),
			}, nil
		}),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to copy objects into object table: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return len(batch), nil
}

// Narinfo import constants.
const (
	narinfoImportBatchSize     = 5000
	narinfoProgressInterval    = 50_000
	narinfoImportStreamWorkers = 16
	narinfoNumPartitions       = 128 // Must match nar_info table partitioning
)

// normalizeCompression maps narinfo compression names to database enum values.
func normalizeCompression(compression string) string {
	switch compression {
	case "bzip2":
		return "bz2"
	case "":
		return "none"
	default:
		return compression
	}
}

// narinfoImportStats tracks import statistics.
type narinfoImportStats struct {
	processed atomic.Int64
	imported  atomic.Int64
	failed    atomic.Int64
}

// narinfoRecord holds a parsed narinfo ready for insertion.
type narinfoRecord struct {
	hash        []byte
	url         string
	storePath   string
	compression string
	fileHash    string
	fileSize    int64
	narHash     string
	narSize     int64
	deriver     string
	references  [][]byte
	signatures  []string
}

// narinfoImportHandler handles batch processing for narinfo import.
// Records are batched by partition to improve write locality.
// Database writes are asynchronous using errgroup for concurrency.
type narinfoImportHandler struct {
	ctx    context.Context
	pgPool *pgxpool.Pool
	stats  *narinfoImportStats
	// batches holds records grouped by partition number (0-127)
	batches [narinfoNumPartitions][]narinfoRecord
	mu      sync.Mutex
	// eg handles async database writes
	eg    *errgroup.Group
	egCtx context.Context
}

// ImportNarinfos imports all narinfos from the Badger inventory database into PostgreSQL.
// It uses Badger's Stream API for efficient reading and PostgreSQL COPY for fast insertion.
func ImportNarinfos(ctx context.Context, cfg *config.Config) error {
	start := time.Now()

	log.Info("starting narinfo import from badger to postgres")

	if cfg.Badger == nil {
		return errors.New("badger config is required")
	}

	// Open the inventory database
	inventoryDB, err := inventory.OpenDB(cfg.Badger)
	if err != nil {
		return fmt.Errorf("failed to open inventory db: %w", err)
	}

	defer func() {
		if closeErr := inventoryDB.Close(); closeErr != nil {
			log.Errorf("failed to close inventory db: %s", closeErr)
		}
	}()

	// Connect to PostgreSQL
	pgPool, err := cfg.Postgres.Connect(ctx, true)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres: %w", err)
	}

	defer pgPool.Close()

	// Truncate nar_info table before import
	log.Info("truncating nar_info table")

	if _, err = pgPool.Exec(ctx, "TRUNCATE nar_info CASCADE"); err != nil {
		return fmt.Errorf("failed to truncate nar_info: %w", err)
	}

	// Run the import
	stats := &narinfoImportStats{}

	if err = importNarinfosFromBadger(ctx, inventoryDB, pgPool, stats); err != nil {
		return fmt.Errorf("failed to import narinfos: %w", err)
	}

	// Log final statistics
	duration := time.Since(start)
	rate := float64(stats.imported.Load()) / duration.Seconds()

	log.Info("narinfo import complete",
		"processed", humanize.Comma(stats.processed.Load()),
		"imported", humanize.Comma(stats.imported.Load()),
		"failed", humanize.Comma(stats.failed.Load()),
		"duration", duration,
		"rate", humanize.CommafWithDigits(rate, 0)+"/s",
	)

	return nil
}

// importNarinfosFromBadger streams narinfos from Badger and inserts them into PostgreSQL.
func importNarinfosFromBadger(
	ctx context.Context,
	inventoryDB *badger.DB,
	pgPool *pgxpool.Pool,
	stats *narinfoImportStats,
) error {
	// Create errgroup for async database writes
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(32) // Limit concurrent DB writes to match connection pool

	handler := &narinfoImportHandler{
		ctx:    ctx,
		pgPool: pgPool,
		stats:  stats,
		eg:     eg,
		egCtx:  egCtx,
	}

	stream := inventoryDB.NewStream()
	stream.NumGo = narinfoImportStreamWorkers
	stream.Prefix = []byte(inventory.BadgerPrefixObject)
	stream.LogPrefix = "narinfo-import"

	// Only process .narinfo files
	stream.ChooseKey = func(item *badger.Item) bool {
		return bytes.HasSuffix(item.Key(), []byte(".narinfo"))
	}

	// Process batches of KV pairs
	stream.Send = func(buf *z.Buffer) error {
		return handler.handleBatch(buf)
	}

	// Run the stream
	if err := stream.Orchestrate(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("stream orchestration failed: %w", err)
		}

		log.Info("import cancelled, flushing remaining batch...")
	}

	// Wait for all async writes to complete
	if err := handler.eg.Wait(); err != nil {
		return fmt.Errorf("async batch write failed: %w", err)
	}

	// Flush any remaining records in all partitions
	if err := handler.flushAllBatches(); err != nil {
		return fmt.Errorf("failed to flush final batches: %w", err)
	}

	return nil
}

// hashPartition computes the PostgreSQL hash partition number for a hash string.
// This must match PostgreSQL's hash partitioning: hash(hash) % 128
func hashPartition(hash []byte) int {
	// PostgreSQL uses a specific hash function for hash partitioning.
	// We use a simple hash that approximates partition distribution.
	// The actual partition is determined by PostgreSQL, but grouping by
	// any consistent hash of the key improves locality.
	var h uint32
	for i := 0; i < len(hash); i++ {
		h = h*31 + uint32(hash[i])
	}

	return int(h % narinfoNumPartitions)
}

// handleBatch processes a batch of KV pairs from the Badger stream.
func (h *narinfoImportHandler) handleBatch(buf *z.Buffer) error {
	// Check for context cancellation or errgroup failure
	if err := h.egCtx.Err(); err != nil {
		return err
	}

	list, err := badger.BufferToKVList(buf)
	if err != nil {
		return fmt.Errorf("buffer to KV list: %w", err)
	}

	records := h.parseRecords(list.GetKv())
	if len(records) == 0 {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Add records to their respective partition batches
	for _, record := range records {
		partition := hashPartition(record.hash)
		h.batches[partition] = append(h.batches[partition], record)

		// Submit async flush if this partition's batch is full
		if len(h.batches[partition]) >= narinfoImportBatchSize {
			// Copy batch for async processing to avoid data race
			batchCopy := make([]narinfoRecord, len(h.batches[partition]))
			copy(batchCopy, h.batches[partition])
			h.batches[partition] = h.batches[partition][:0]

			// Submit async flush
			h.eg.Go(func() error {
				return h.flushBatch(batchCopy)
			})
		}
	}

	return nil
}

// parseRecords converts KV pairs to narinfoRecords.
func (h *narinfoImportHandler) parseRecords(kvList []*pb.KV) []narinfoRecord {
	records := make([]narinfoRecord, 0, len(kvList))

	for _, kv := range kvList {
		if kv.GetStreamDone() {
			continue
		}

		h.stats.processed.Add(1)

		// Parse the narinfo
		info, err := narinfo.Parse(bytes.NewReader(kv.GetValue()))
		if err != nil {
			log.Debugf("failed to parse narinfo %s: %s", kv.GetKey(), err)
			h.stats.failed.Add(1)

			continue
		}

		// Extract the hash from the key (format: "o:<hash>.narinfo")
		key := string(kv.GetKey())
		hashStr := key[len(inventory.BadgerPrefixObject) : len(key)-len(".narinfo")]

		// Decode hash from nixbase32 string to bytes
		hash, err := nixbase32.DecodeString(hashStr)
		if err != nil {
			log.Debugf("failed to decode hash %s: %s", hashStr, err)
			h.stats.failed.Add(1)

			continue
		}

		// Build references array (first 32 chars of each reference, decoded to bytes)
		references := make([][]byte, 0, len(info.References))
		for _, ref := range info.References {
			if len(ref) >= 32 {
				refBytes, err := nixbase32.DecodeString(ref[:32])
				if err != nil {
					log.Debugf("failed to decode reference hash %s: %s", ref[:32], err)
					continue
				}

				references = append(references, refBytes)
			}
		}

		// Build signatures array as "name:base64data" strings
		signatures := make([]string, 0, len(info.Signatures))
		for _, sig := range info.Signatures {
			signatures = append(signatures, sig.Name+":"+base64.StdEncoding.EncodeToString(sig.Data))
		}

		record := narinfoRecord{
			hash:        hash,
			url:         info.URL,
			storePath:   info.StorePath,
			compression: normalizeCompression(info.Compression),
			fileSize:    int64(info.FileSize),
			narSize:     int64(info.NarSize),
			deriver:     info.Deriver,
			references:  references,
			signatures:  signatures,
		}

		if info.FileHash != nil {
			record.fileHash = info.FileHash.String()
		}

		if info.NarHash != nil {
			record.narHash = info.NarHash.String()
		}

		records = append(records, record)
	}

	return records
}

// flushAllBatches flushes all remaining partition batches to PostgreSQL synchronously.
// Called after errgroup.Wait() to flush any partial batches.
func (h *narinfoImportHandler) flushAllBatches() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for partition := 0; partition < narinfoNumPartitions; partition++ {
		if len(h.batches[partition]) > 0 {
			// Copy and flush synchronously for final batches
			batchCopy := make([]narinfoRecord, len(h.batches[partition]))
			copy(batchCopy, h.batches[partition])
			h.batches[partition] = h.batches[partition][:0]

			if err := h.flushBatch(batchCopy); err != nil {
				return err
			}
		}
	}

	return nil
}

// flushBatch writes a batch of records to PostgreSQL using COPY protocol.
// This function is safe to call concurrently from multiple goroutines.
func (h *narinfoImportHandler) flushBatch(batch []narinfoRecord) error {
	if len(batch) == 0 {
		return nil
	}

	// Use egCtx to respect errgroup cancellation
	ctx := h.egCtx

	// Acquire a connection from the pool
	conn, err := h.pgPool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire postgres connection: %w", err)
	}

	defer conn.Release()

	// Begin a transaction
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Disable synchronous commit for better throughput
	if _, err = tx.Exec(ctx, "SET LOCAL synchronous_commit = off"); err != nil {
		return fmt.Errorf("failed to set synchronous_commit: %w", err)
	}

	// Use COPY for bulk insert
	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"nar_info"},
		[]string{
			"hash", "url", "store_path", "compression", "file_hash",
			"file_size", "nar_hash", "nar_size", "deriver", "references", "signatures",
		},
		pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
			r := batch[i]
			return []any{
				r.hash,
				r.url,
				r.storePath,
				r.compression,
				r.fileHash,
				r.fileSize,
				r.narHash,
				r.narSize,
				r.deriver,
				r.references,
				r.signatures,
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to copy narinfos: %w", err)
	}

	// Commit the transaction
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Update stats and log progress
	imported := h.stats.imported.Add(int64(len(batch)))
	prev := imported - int64(len(batch))

	if imported/narinfoProgressInterval != prev/narinfoProgressInterval {
		log.Infof("imported %s narinfos", humanize.Comma(imported))
	}

	return nil
}
