package inventory

import (
	"context"
	"errors"

	//nolint:gosec
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/numtide/narwal/pkg/config"
	"golang.org/x/sync/errgroup"
)

type Downloader struct {
	db     *badger.DB
	client *Client
}

func NewDownloader(ctx context.Context, cfg *config.Config) (*Downloader, error) {
	// check we have badger config
	if cfg.Badger == nil {
		return nil, errors.New("badger config is required")
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
	db, err := OpenDB(cfg.Badger)
	if err != nil {
		return nil, err
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
	// fetch the manifest, either from local db or remotely from s3
	manifest, err := d.ensureManifest(ctx, report)
	if err != nil {
		return err
	}

	// create a group to download files concurrently
	downloadGroup, ctx := errgroup.WithContext(ctx)

	// we limit the pool to 16 threads, which is the max connections per host that minio client is configured to use
	downloadGroup.SetLimit(16)

	// determine which files we already have and which we need to download
	missingFiles, err := d.missingFiles(manifest.Files)
	if err != nil {
		return fmt.Errorf("failed to determine missing files: %w", err)
	}

	log.Infof("manifest %s has %d files, we need to fetch %d", report, len(manifest.Files), len(missingFiles))

	// create a channel for files we have fetched
	fetchedCh := make(chan *ManifestFile, 16)

	// create a separate group for writing files into boltdb
	// we use a single write routine to avoid contention
	writeGroup, ctx := errgroup.WithContext(ctx)
	writeGroup.Go(func() error {
		for file := range fetchedCh {
			log.Infof("writing file %s to local db", file.Key)

			if err = d.db.Update(func(tx *badger.Txn) error {
				return PutFile(tx, file.Key, file.Data)
			}); err != nil {
				return fmt.Errorf("failed to write file %s: %w", file.Key, err)
			}

			log.Infof("file %s has been written to local db", file.Key)
		}

		return nil
	})

LOOP:
	// iterate the list of missing files and start downloading them concurrently
	for _, file := range missingFiles {
		select {
		// check if the context has been cancelled
		case <-ctx.Done():
			// stop processing files
			break LOOP

		default:
			// otherwise, start a new download
			downloadGroup.Go(func() error {
				log.Infof("getting file %s from s3", file.Key)

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
	}

	// wait for downloads to complete
	if err = downloadGroup.Wait(); err != nil {
		return fmt.Errorf("failed to download files: %w", err)
	}

	// indicate no more files
	close(fetchedCh)

	// wait for writing to complete
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

func (d *Downloader) missingFiles(files []ManifestFile) ([]ManifestFile, error) {
	// start a read transaction
	tx := d.db.NewTransaction(false)

	// ensure a discard is called if a commit is never made

	defer tx.Discard()

	missing := make([]ManifestFile, 0, len(files))

	// iterate the list of files and check if they are already in the db
	for _, file := range files {
		exists, err := HasFile(tx, file.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to check if file %s is in local db: %w", file.Key, err)
		}

		if !exists {
			missing = append(missing, file)
		}
	}

	return missing, nil
}

func (d *Downloader) ensureManifest(ctx context.Context, report string) (*Manifest, error) {
	tx := d.db.NewTransaction(true)

	// ensure discard is called if a commit is never made

	defer tx.Discard()

	// try to get the manifest from local db
	log.Debugf("looking for manifest %s in local db", report)

	manifest, err := GetManifest(tx, report)
	if !errors.Is(err, badger.ErrKeyNotFound) && err != nil {
		return nil, fmt.Errorf("failed to get manifest %s from local db: %w", report, err)
	}

	// if we have a manifest, return it
	if manifest != nil {
		log.Debugf("found manifest %s in local db", report)
		return manifest, nil
	}

	// otherwise, download the manifest from s3
	log.Debugf("manifest %s not found in local db, downloading from s3", report)

	manifest, err = d.client.GetManifest(ctx, report)
	if err != nil {
		return nil, fmt.Errorf("failed to get manifest %s from s3: %w", report, err)
	}

	// put the manifest in the local db
	log.Debugf("putting manifest %s in local db", report)

	if err = PutManifest(tx, report, manifest); err != nil {
		return nil, fmt.Errorf("failed to put manifest %s in local db: %w", report, err)
	}

	// commit the transaction and return the manifest
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return manifest, nil
}
