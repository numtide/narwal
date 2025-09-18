package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dustin/go-humanize"
	"github.com/numtide/narwal/pkg/config"
	"github.com/parquet-go/parquet-go"
	"golang.org/x/sync/errgroup"
)

type countRecord struct {
	key string
	val int64
}

func Verify(ctx context.Context, cfg *config.Config, report string) error {
	var err error

	// check we have bolt config
	if cfg.Badger == nil {
		return errors.New("badger config is required")
	}

	// open the db
	db, err := OpenDB(cfg.Badger)
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}

	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Errorf("failed to close db: %s", closeErr)
		}
	}()

	// lookup the manifest
	var manifest *Manifest

	if err := db.Update(func(tx *badger.Txn) error {
		manifest, err = GetManifest(tx, report)
		if err != nil {
			return fmt.Errorf("failed to get manifest: %w", err)
		}

		return nil
	}); err != nil {
		//nolint:wrapcheck
		return err
	}

	// create a channel for counting
	counterCh := make(chan countRecord, 1024*10)
	counterGroup := errgroup.Group{}

	counterGroup.Go(func() error {
		counts := make(map[string]int64)

		var (
			ok    bool
			count int64
		)

		for record := range counterCh {
			count, ok = counts[record.key]
			if !ok {
				count = 0
			}

			counts[record.key] = count + record.val
		}

		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, key := range keys {
			var formatted string
			if strings.Contains(key, "size") {
				//nolint:gosec
				formatted = humanize.Bytes(uint64(counts[key]))
			} else {
				formatted = humanize.Comma(counts[key])
			}

			_, _ = fmt.Fprintf(os.Stdout, "%s: %s\n", key, formatted)
		}

		return nil
	})

LOOP:
	for idx, file := range manifest.Files {
		select {
		case <-ctx.Done():
			break LOOP

		default:
			log.Infof("[%d / %d] verifying manifest file %s", idx+1, len(manifest.Files), file.Key)

			if err = verifyManifestFile(ctx, db, &file, counterCh); err != nil {
				return fmt.Errorf("failed to verify manifest file %s: %w", file.Key, err)
			}
		}
	}

	// indicate no more counter records
	close(counterCh)

	// wait for the counter group to finish
	if err = counterGroup.Wait(); err != nil {
		return fmt.Errorf("failed to count: %w", err)
	}

	return nil
}

func verifyManifestFile(ctx context.Context, db *badger.DB, file *ManifestFile, counter chan countRecord) error {
	// read the file from db
	var (
		err error
		buf []byte
	)

	if err = db.View(func(tx *badger.Txn) error {
		exists, err := HasManifestFile(tx, file)
		if err != nil {
			return fmt.Errorf("failed to check if file %s is in local db: %w", file.Key, err)
		}

		if !exists {
			counter <- countRecord{
				key: "file_missing",
				val: 1,
			}
			return nil
		}

		buf, err = GetManifestFile(tx, file)
		if err != nil {
			return fmt.Errorf("failed to get file %s from db: %w", file.Key, err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to get file %s from db: %w", file.Key, err)
	}

	if buf == nil {
		log.Warnf("file %s is missing from db", file.Key)
		return nil
	}

	start := time.Now()

	// read everything from the parquet file into memory
	objs, err := parquet.Read[Object](bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return errors.New("failed to read parquet file")
	}

	// process the contents of the file
	numRows := len(objs)

	if err = verifyNarInfoBatch(ctx, db, objs, counter); err != nil {
		return fmt.Errorf("failed to verify narinfo batch: %w", err)
	}

	log.Infof("verified file %s with %s rows in %v", file.Key, humanize.Comma(int64(numRows)), time.Since(start))

	return nil
}

func verifyNarInfoBatch(ctx context.Context, db *badger.DB, rows []Object, counterCh chan countRecord) error {
	eg := errgroup.Group{}
	eg.SetLimit(runtime.NumCPU())

	tx := db.NewTransaction(false)
	defer tx.Discard()

LOOP:
	for _, row := range rows {
		select {
		case <-ctx.Done():
			break LOOP

		default:
			ext := path.Ext(row.Key)

			if len(ext) > 0 {
				// drop the leading `.`
				ext = ext[1:]
			}

			counterCh <- countRecord{
				key: "object_count",
				val: 1,
			}

			counterCh <- countRecord{
				key: "object_count_with_ext_" + ext,
				val: 1,
			}

			counterCh <- countRecord{
				key: "object_size",
				val: row.Size,
			}

			if ext != "narinfo" {
				continue
			}

			counterCh <- countRecord{
				key: "nar_info_count",
				val: 1,
			}

			counterCh <- countRecord{
				key: "nar_info_size",
				val: row.Size,
			}

			eg.Go(func() error {
				// check whether the narinfo is valid
				err := VerifyNarInfo(tx, row.Key, int(row.Size), row.ETag)
				if errors.Is(err, ErrKeyNotFound) {
					counterCh <- countRecord{
						key: "nar_info_missing",
						val: 1,
					}

					return nil
				} else if err != nil {
					log.Errorf("failed to verify narinfo %s: %s", row.Key, err)

					counterCh <- countRecord{
						key: "nar_info_invalid",
						val: 1,
					}

					return nil
				}

				counterCh <- countRecord{
					key: "nar_info_valid",
					val: 1,
				}

				return nil
			})
		}
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to verify narinfos: %w", err)
	}

	return nil
}
