package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/numtide/narwal/pkg/config"
	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/reader"
	"golang.org/x/sync/errgroup"
)

//nolint:gocognit
func OutputStats(ctx context.Context, cfg *config.Config, report string) error {
	// check we have bolt config
	if cfg.Badger == nil {
		return errors.New("badger config is required")
	}

	// open the db
	db, err := OpenDB(cfg.Badger)
	if err != nil {
		return err
	}

	var eg *errgroup.Group

	eg, ctx = errgroup.WithContext(ctx)
	eg.SetLimit(8)

	var manifest *Manifest

	if err = db.Update(func(tx *badger.Txn) error {
		manifest, err = GetManifest(tx, report)
		if err != nil {
			return fmt.Errorf("failed to get manifest: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	tx := db.NewTransaction(false)

	defer tx.Discard()

	narinfoCount := atomic.Int64{}

	for idx, file := range manifest.Files {
		eg.Go(func() error {
			select {
			case <-ctx.Done():
				return nil

			default:
				buf, err := GetFile(tx, file.Key)
				if errors.Is(err, badger.ErrKeyNotFound) {
					return fmt.Errorf("file %s not found in db", file.Key)
				} else if err != nil {
					return fmt.Errorf("failed to get file %s from local db: %w", file.Key, err)
				}

				bufferFile := buffer.NewBufferFileFromBytes(buf)

				pr, err := reader.NewParquetReader(bufferFile, new(ObjectEssential), 4)
				if err != nil {
					return fmt.Errorf("failed to create parquet reader: %w", err)
				}

				log.Infof("[%d] file %s has %d rows", idx, file.Key, pr.GetNumRows())

				var count int64

				batchSize := 1024 * 100
				numRows := int(pr.GetNumRows())

				rows := make([]ObjectEssential, batchSize)

				for i := 0; i < numRows; i += batchSize {
					err := pr.Read(&rows)
					if err != nil {
						return fmt.Errorf("failed to read row: %w", err)
					}

					for _, obj := range rows {
						if strings.HasSuffix(obj.Key, ".narinfo") {
							count += 1
						}
					}
				}

				log.Infof("[%d] file %s has %d narinfos", idx, file.Key, count)

				narinfoCount.Add(count)

				pr.ReadStop()

				return nil
			}
		})
	}

	if err = eg.Wait(); err != nil {
		return fmt.Errorf("failed to wait for eg: %w", err)
	}

	log.Infof("total narinfos: %d", narinfoCount.Load())

	return nil
}
