package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dustin/go-humanize"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/store"
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
	eg.SetLimit(8) // todo how to size this?

	// Batch objects by object_type for partition-aware imports in Postgres
	batches := make(map[db.ObjectType][]objectWithMetadata)

	// Import objects in batches of 10240
	const batchSize = 10240 // todo how to size this?

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

	// Before importing we analyse the path of each object to determine its object_type
	for _, obj := range objects {
		if shouldIgnorePath(obj.Key) {
			continue
		}

		// Analyse the path
		analysis, err := store.AnalyzePath(obj.Key)
		if err != nil {
			return fmt.Errorf("failed to analyze path '%s': %w", obj.Key, err)
		}

		hash, err := store.HashFromPath(obj.Key, analysis.ObjectType)
		if err != nil {
			return fmt.Errorf("failed to get hash from path %s: %w", obj.Key, err)
		}

		// Append to the correct batch
		batches[analysis.ObjectType] = append(batches[analysis.ObjectType], objectWithMetadata{
			obj:         obj,
			hash:        hash,
			compression: string(analysis.Compression),
		})

		// Continue to the next object if the batch isn't full
		if len(batches[analysis.ObjectType]) < batchSize {
			continue
		}

		// Otherwise, flush the batch and clear it
		flushBatch(analysis.ObjectType, batches[analysis.ObjectType])
		delete(batches, analysis.ObjectType)
	}

	// Flush any remaining partial batches
	for objectType, batch := range batches {
		if len(batch) > 0 {
			flushBatch(objectType, batch)
		}
	}

	// Wait for all pending import jobs to complete
	if err = eg.Wait(); err != nil {
		return fmt.Errorf("failed to import objects: %w", err)
	}

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

	// Collect the object hashes and paths for the COPY statement
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
