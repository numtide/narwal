package inventory

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/numtide/narwal/pkg/config"
	"github.com/parquet-go/parquet-go"
)

var (
	ErrMissingManifest      = errors.New("manifest is missing")
	ErrMissingManifestFiles = errors.New("one or more manifest files are missing")
)

// Verify verifies the integrity of the inventory database.
// For the given report, it checks that all parquet files listed in the manifest have been downloaded, and that all
// narinfos in the manifest have been downloaded. It then checks that all narinfos in the database match the checksums
// in the manifest.
func Verify(ctx context.Context, cfg *config.Config, report string) error {
	var err error

	start := time.Now()

	log.Infof("verifying inventory for report %s", report)

	// check we have badger config
	if cfg.Badger == nil {
		return errors.New("badger config is required")
	}

	// open the db
	db, err := OpenDB(cfg.Badger)
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}

	// ensure we close the db cleanly
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Errorf("failed to close db: %s", closeErr)
		}
	}()

	// ensure we have downloaded the manifest and its associated parquet files
	manifest, err := checkForManifest(db, report)
	if err != nil {
		return err
	}

	// read the manifest files and check that all narinfos are present in the db
	if err = readManifestFiles(ctx, cfg, db, manifest); err != nil {
		return fmt.Errorf("failed to read manifest files: %w", err)
	}

	log.Infof("finished verifying inventory for report %s in %v", report, time.Since(start))

	return nil
}

func checkForManifest(db *badger.DB, id string) (*Manifest, error) {
	log.Infof("checking inventory for manifest %s", id)

	tx := db.NewTransaction(false)
	defer tx.Discard()

	manifest, err := GetManifest(tx, id)
	if errors.Is(err, ErrKeyNotFound) {
		return nil, ErrMissingManifest
	} else if err != nil {
		return nil, fmt.Errorf("failed to read manifest from db: %w", err)
	}

	log.Infof("manifest %s found, checking for manifest files", id)

	hasMissing := false

	for _, file := range manifest.Files {
		exists, err := HasManifestFile(tx, &file)
		if err != nil {
			return nil, fmt.Errorf("failed to check if manifest file %s exists: %w", file.Key, err)
		}

		log.Debugf("manifest file %s exists = %t", file.Key, exists)

		if !exists {
			log.Warnf("manifest file %s is missing", file.Key)

			hasMissing = true
		}
	}

	if hasMissing {
		return nil, ErrMissingManifestFiles
	}

	log.Infof("all %d manifest files for %s have been downloaded", len(manifest.Files), id)

	return manifest, nil
}

func readManifestFiles(ctx context.Context, cfg *config.Config, db *badger.DB, manifest *Manifest) error {
	allStats := &Stats{}

	log.Infof("reading manifest files")

LOOP:
	for idx, file := range manifest.Files {
		select {
		case <-ctx.Done():
			log.Warnf("context cancelled, stopping reading manifest files")
			break LOOP

		default:
			ml := log.WithPrefix(fmt.Sprintf("[%d / %d]", idx+1, len(manifest.Files)))

			ml.Infof("reading manifest file: %s", file.Key)

			start := time.Now()
			fileStats, err := readManifestFile(ctx, cfg, db, &file)
			if err != nil {
				return fmt.Errorf("failed to read manifest file %s: %w", file.Key, err)
			}

			log.Infof("finished reading manifest file %s in %v", file.Key, time.Since(start))
			log.Debugf("file stats:\n%s", fileStats)

			// merge the stats from this file with the overall stats
			allStats.Merge(fileStats)

			log.Infof("summary:\n%s", allStats)
		}
	}

	log.Infof("finished reading manifest files\nfinal summary:\n%s", allStats)

	return nil
}

func readManifestFile(ctx context.Context, cfg *config.Config, db *badger.DB, file *ManifestFile) (*Stats, error) {
	// retrieve the file contents from the db
	var (
		err error
		buf []byte
	)

	if err = db.View(func(tx *badger.Txn) error {
		buf, err = GetManifestFile(tx, file)
		if errors.Is(err, ErrKeyNotFound) {
			return fmt.Errorf("file %s is missing from db", file.Key)
		} else if err != nil {
			return fmt.Errorf("failed to get file %s from db: %w", file.Key, err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	// read the entire parquet file into memory
	objects, err := parquet.Read[Object](bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("failed to read parquet file: %w", err)
	}

	// create a read-only transaction for reading objects from the db
	tx := db.NewTransaction(false)
	defer tx.Discard()

	// create a stats object for tracking what we find
	statz := Stats{}
	statz.Objects.Count += Count(len(objects))

	wb := db.NewWriteBatch()

LOOP:
	for _, obj := range objects {
		select {
		case <-ctx.Done():
			log.Warnf("context cancelled, stopping reading manifest file")
			break LOOP

		default:
			// record total size of all objects
			statz.Objects.Size += SizeBytes(uint64(obj.Size)) //nolint:gosec

			// we only download narinfos, so skip anything else for further processing
			if path.Ext(obj.Key) != ".narinfo" {
				continue
			}

			// record some narinfo stats
			statz.Narinfo.Size += SizeBytes(uint64(obj.Size)) //nolint:gosec
			statz.Narinfo.Count++

			if err = verifyNarInfo(cfg, &obj, tx, wb, &statz); err != nil {
				return nil, fmt.Errorf("failed to verify narinfo %s: %w", obj.Key, err)
			}
		}
	}

	if err = wb.Flush(); err != nil {
		return nil, fmt.Errorf("failed to flush write batch: %w", err)
	}

	return &statz, nil
}

func verifyNarInfo(cfg *config.Config, obj *Object, tx *badger.Txn, wb *badger.WriteBatch, statz *Stats) error {
	// check if we have an entry for this narinfo in the db
	key := ObjectKey(obj)

	item, err := tx.Get(key)
	if errors.Is(err, badger.ErrKeyNotFound) {
		// record that we have a missing narinfo
		statz.Narinfo.Missing++

		log.Debugf("missing narinfo: %s", obj.Key)

		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get object %s from db: %w", obj.Key, err)
	}

	checksum, err := hex.DecodeString(obj.ETag)
	if err != nil {
		return fmt.Errorf("failed to decode ETag %s: %w", obj.ETag, err)
	}

	err = item.Value(func(val []byte) error {
		hash := md5.Sum(val) //nolint:gosec

		if bytes.Equal(checksum, hash[:]) {
			statz.Narinfo.Verified++

			return nil
		}

		statz.Narinfo.BadChecksum++

		if !cfg.Inventory.DeleteInvalidNarInfos {
			return nil
		}

		if err = wb.Delete(key); err != nil {
			return fmt.Errorf("failed to delete object %s from db: %w", obj.Key, err)
		}

		statz.Narinfo.Deleted++

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to read object %v, %s: %w", obj.LastModifiedDate, obj.Key, err)
	}

	return nil
}
