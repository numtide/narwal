package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dustin/go-humanize"
	"github.com/numtide/narwal/pkg/config"
	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/reader"
	"golang.org/x/sync/errgroup"
)

type countRecord struct {
	key string
	val int64
}

type Verifier struct {
	db *badger.DB
}

func NewVerifier(cfg *config.Config) (*Verifier, error) {
	// check we have bolt config
	if cfg.Badger == nil {
		return nil, errors.New("badger config is required")
	}

	// open the db
	db, err := OpenDB(cfg.Badger)
	if err != nil {
		return nil, err
	}

	return &Verifier{
		db: db,
	}, nil
}

func (v *Verifier) Verify(ctx context.Context, report string) error {
	var err error

	// lookup the manifest
	var manifest *Manifest

	if err := v.db.Update(func(tx *badger.Txn) error {
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

	// create a new errgroup for concurrent processing
	verifyGroup, verifyCtx := errgroup.WithContext(ctx)
	verifyGroup.SetLimit(8)

	log.Infof("verifying %d files", len(manifest.Files))

LOOP:
	// verify each file concurrently
	for _, file := range manifest.Files {
		select {
		case <-ctx.Done():
			break LOOP
		default:
			verifyGroup.Go(func() error {
				if err := v.verifyFile(verifyCtx, file, counterCh); err != nil {
					return fmt.Errorf("failed to verify file %s: %w", file.Key, err)
				}

				return nil
			})
		}
	}

	// wait for all files to be verified
	if err = verifyGroup.Wait(); err != nil {
		return fmt.Errorf("failed to verify files: %w", err)
	}

	// indicate no more counter records
	close(counterCh)

	// wait for the counter group to finish
	if err = counterGroup.Wait(); err != nil {
		return fmt.Errorf("failed to count: %w", err)
	}

	return nil
}

func (v *Verifier) Close() error {
	if err := v.db.Close(); err != nil {
		return fmt.Errorf("failed to close db: %w", err)
	}

	return nil
}

func (v *Verifier) verifyFile(ctx context.Context, file ManifestFile, counter chan countRecord) error {
	// read the file from db
	var (
		err error
		buf []byte
	)

	if err = v.db.View(func(tx *badger.Txn) error {
		exists, err := HasFile(tx, file.Key)
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

		buf, err = GetFile(tx, file.Key)
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

	log.Infof("verifying file %s", file.Key)

	start := time.Now()

	// create a parquet reader for the file
	pr, err := reader.NewParquetReader(buffer.NewBufferFileFromBytes(buf), new(Object), 4)
	if err != nil {
		return fmt.Errorf("failed to create parquet reader: %w", err)
	}

	defer pr.ReadStop()

	// process the contents of the file
	numRows := int(pr.GetNumRows())
	batchSize := 1024

	rows := make([]Object, batchSize)

	for i := 0; i < numRows; i += batchSize {
		select {
		case <-ctx.Done():
			return nil
		default:
			err := pr.Read(&rows)
			if err != nil {
				return fmt.Errorf("failed to read row: %w", err)
			}

			err = v.db.View(func(tx *badger.Txn) error {
				for _, row := range rows {
					ext := path.Ext(row.Key)

					if len(ext) > 0 {
						// drop the leading `.`
						ext = ext[1:]
					}

					counter <- countRecord{
						key: "object_count",
						val: 1,
					}

					counter <- countRecord{
						key: "object_count_with_ext_" + ext,
						val: 1,
					}

					counter <- countRecord{
						key: "object_size",
						val: row.Size,
					}

					if ext != "narinfo" {
						continue
					}

					counter <- countRecord{
						key: "nar_info_count",
						val: 1,
					}

					counter <- countRecord{
						key: "nar_info_size",
						val: row.Size,
					}

					// check whether the narinfo is valid
					err = VerifyNarInfo(tx, row.Key, int(row.Size), row.ETag)
					if errors.Is(err, ErrKeyNotFound) {
						counter <- countRecord{
							key: "nar_info_missing",
							val: 1,
						}

						continue
					} else if err != nil {
						log.Errorf("failed to verify narinfo %s: %s", row.Key, err)

						counter <- countRecord{
							key: "nar_info_invalid",
							val: 1,
						}

						continue
					}

					counter <- countRecord{
						key: "nar_info_valid",
						val: 1,
					}
				}

				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to verify narinfo batch: %w", err)
			}
		}
	}

	log.Infof("verified file %s with %s rows in %v", file.Key, humanize.Comma(pr.GetNumRows()), time.Since(start))

	return nil
}
