package inventory

import (
	"context"
	"errors"

	//nolint:gosec
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"runtime"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/errgroup"
)

type Downloader struct {
	db     *bolt.DB
	client *Client
}

func NewDownloader(ctx context.Context, cfg *config.Config) (*Downloader, error) {
	// check we have bolt config
	if cfg.Bolt == nil {
		return nil, errors.New("bolt config is required")
	}

	// check we have s3 config
	if cfg.S3 == nil {
		return nil, errors.New("s3 config is required")
	}

	// check we have inventory config
	if cfg.Inventory == nil {
		return nil, errors.New("inventory config is required")
	}

	// open the db
	db, err := cfg.Bolt.Open()
	if err != nil {
		//nolint:wrapcheck
		return nil, err
	}

	// ensure all the buckets are created
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := GetManifestBucket(tx); err != nil {
			return fmt.Errorf("failed to create manifest bucket: %w", err)
		}

		if _, err := GetFileBucket(tx); err != nil {
			return fmt.Errorf("failed to create file bucket: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialise local db: %w", err)
	}

	// create an s3 client
	s3, err := cfg.S3.Connect(ctx)
	if err != nil {
		//nolint:wrapcheck
		return nil, err
	}

	return &Downloader{
		db:     db,
		client: NewClient(s3, cfg.Inventory.BucketPrefix),
	}, nil
}

func (d *Downloader) Download(ctx context.Context, report string) error {
	manifest, err := d.ensureManifest(ctx, report)
	if err != nil {
		return err
	}

	downloadThreads := runtime.NumCPU() / 2
	if downloadThreads > 16 {
		// this is the default number of max idle connections per host in minio client transport
		downloadThreads = 16
	}

	downloadGroup, ctx := errgroup.WithContext(ctx)
	downloadGroup.SetLimit(downloadThreads) // this is the max connections per host in minio client transport

	filesToFetch, err := d.filterFiles(manifest.Files)
	if err != nil {
		return fmt.Errorf("failed to filter files: %w", err)
	}

	log.Debugf("manifest has %d files, we need to fetch %d", len(manifest.Files), len(filesToFetch))

	fetchedCh := make(chan *ManifestFile, 16)

	writeGroup, ctx := errgroup.WithContext(ctx)
	writeGroup.Go(func() error {
		for file := range fetchedCh {
			err = d.db.Update(func(tx *bolt.Tx) error {
				bucket, err := GetFileBucket(tx)
				if err != nil {
					return fmt.Errorf("failed to get file bucket: %w", err)
				}

				if err = bucket.Put(file.Key, file.Data); err != nil {
					return fmt.Errorf("failed to put file %s: %w", file.Key, err)
				}

				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to write file %s: %w", file.Key, err)
			}
		}

		return nil
	})

	for _, file := range filesToFetch {
		downloadGroup.Go(func() error {
			log.Debugf("getting file %s from s3", file.Key)

			// download the file
			r, err := d.client.GetFile(ctx, file)
			if err != nil {
				return fmt.Errorf("failed to download file %s from s3: %w", file.Key, err)
			}

			// read all of it in memory
			file.Data, err = io.ReadAll(r)
			if err != nil {
				return fmt.Errorf("failed to read file %s bytes from s3: %w", file.Key, err)
			}

			// validate the checksum
			//nolint:gosec
			checksum := md5.Sum(file.Data)
			checksumHex := hex.EncodeToString(checksum[:])

			if checksumHex != file.MD5Checksum {
				return fmt.Errorf("checksum failed when downloading %s from s3", file.Key)
			}

			log.Debugf("fetched file %s from s3", file.Key)

			// add to the write queue
			fetchedCh <- &file

			return nil
		})
	}

	if err = downloadGroup.Wait(); err != nil {
		return fmt.Errorf("failed to download files: %w", err)
	}

	// indicate no more files
	close(fetchedCh)

	// wait for writes to complete
	if err = writeGroup.Wait(); err != nil {
		return fmt.Errorf("failed to write files: %w", err)
	}

	return nil
}

func (d *Downloader) Close() error {
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("failed to close db: %w", err)
	}

	return nil
}
