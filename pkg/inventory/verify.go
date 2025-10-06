package inventory

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dustin/go-humanize"
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

	// check we have bolt config
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
	statz, err := readManifestFiles(ctx, db, manifest)
	if err != nil {
		return fmt.Errorf("failed to read manifest files: %w", err)
	}

	// verify that all narinfos in the db match the checksums in the manifest
	if err = verifyObjects(ctx, db, statz); err != nil {
		return fmt.Errorf("failed to verify objects: %w", err)
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

func readManifestFiles(ctx context.Context, db *badger.DB, manifest *Manifest) (*Stats, error) {
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

			fileStats, err := readManifestFile(ctx, db, &file)
			if err != nil {
				return nil, fmt.Errorf("failed to read manifest file %s: %w", file.Key, err)
			}

			log.Infof("finished reading manifest file %s", file.Key)
			log.Debugf("file stats:\n%s", fileStats)

			// merge the stats from this file with the overall stats
			allStats.Merge(fileStats)

			log.Infof("summary:\n%s", allStats)
		}
	}

	log.Infof("finished reading manifest files\nfinal summary:\n%s", allStats)

	return allStats, nil
}

func readManifestFile(ctx context.Context, db *badger.DB, file *ManifestFile) (*Stats, error) {
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

	prefix := []byte(BadgerPrefixObject)

	// create an iterator for iterating over the objects in the db
	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix: prefix,
	})
	defer iter.Close()

	// seek to the first object in the db
	iter.Seek(prefix)

	// create a stats object for tracking what we find
	statz := Stats{}
	statz.Objects.Count += Count(len(objects))

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
				continue LOOP
			}

			// record some narinfo stats
			statz.Narinfo.Size += SizeBytes(uint64(obj.Size)) //nolint:gosec
			statz.Narinfo.Count++

			// check if we have an entry for this narinfo in the db
			key := []byte(BadgerPrefixObject + obj.Key)

			if iter.Seek(key); iter.ValidForPrefix(key) {
				// do nothing
			} else {
				// record that we have a missing narinfo
				log.Debugf("missing narinfo: manifest file = %s, key = %s", file.Key, obj.Key)
				statz.Narinfo.Missing++
			}
		}
	}

	return &statz, nil
}

// verifyObjects checks that all narinfos in the db match the checksums in the manifest.
func verifyObjects(ctx context.Context, db *badger.DB, statz *Stats) error {
	log.Infof("verifying objects")

	start := time.Now()

	tx := db.NewTransaction(false)
	defer tx.Discard()

	prefix := []byte(BadgerPrefixObject)

	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		Reverse:        false,
		AllVersions:    false,
		PrefetchSize:   100,
		PrefetchValues: true,
	})
	defer iter.Close()

	iter.Seek(prefix)

	processed := int64(0)

LOOP:
	for iter.ValidForPrefix(prefix) {
		select {
		case <-ctx.Done():
			break LOOP

		default:

			if processed%100000 == 0 {
				log.Infof("processed %s objects\n%s", humanize.Comma(processed), statz)
			}

			processed++

			item := iter.Item()

			key := string(item.Key()[len(BadgerPrefixObject):])

			err := item.Value(func(val []byte) error {
				checksum := val[:md5.Size]

				buf := val[md5.Size:]
				if !strings.HasPrefix(string(buf), "StorePath:") {
					log.Errorf("object %s has no checksum corrupt", key)
					statz.Narinfo.NoChecksum++

					return nil
				}

				hash := md5.Sum(buf) //nolint:gosec

				if !bytes.Equal(checksum, hash[:]) {
					statz.Narinfo.BadChecksum++
				}

				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to read object %s: %w", key, err)
			}

			iter.Next()
		}
	}

	log.Infof("finished veryfing objects in %v", time.Since(start))

	return nil
}
