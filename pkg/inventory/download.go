package inventory

import (
	"bytes"
	"context"
	"errors"
	"path"
	"strings"
	"sync/atomic"
	"time"

	//nolint:gosec
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dustin/go-humanize"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
	"github.com/parquet-go/parquet-go"
	"golang.org/x/sync/errgroup"
)

type Downloader struct {
	cfg       *config.Config
	batchSize int

	db     *badger.DB
	client *Client

	fetchCount     atomic.Uint64
	fetchSizeBytes atomic.Uint64
}

func NewDownloader(cfg *config.Config) (*Downloader, error) {
	// check we have badger config
	if cfg.Badger == nil {
		return nil, errors.New("badger config is required")
	}

	// check we have aws config
	if cfg.AWS == nil {
		return nil, errors.New("aws config is required")
	}

	// check we have s3 config
	if cfg.S3 == nil {
		return nil, errors.New("s3 config is required")
	}

	// check we have inventory config
	if cfg.Inventory == nil {
		return nil, errors.New("inventory config is required")
	}

	return &Downloader{cfg: cfg, batchSize: 1024}, nil
}

func (d *Downloader) Download(ctx context.Context, report string) error {
	var err error

	// open the db
	if d.db, err = OpenDB(d.cfg.Badger, false); err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}

	defer func() {
		if closeErr := d.db.Close(); closeErr != nil {
			log.Errorf("failed to close db: %s", closeErr)
		}
	}()

	// create a prefixed s3 client
	s3, err := awssdk.NewS3ClientFromConfig(ctx, d.cfg.AWS, d.cfg.S3)
	if err != nil {
		//nolint:wrapcheck
		return err
	}

	d.client = NewClient(s3, d.cfg.Inventory.BucketPrefix)

	// start download
	log.Infof("downloading inventory report %s", report)

	// fetch the manifest, either from local db or remotely from s3
	manifest, err := d.ensureManifest(ctx, report)
	if err != nil {
		return err
	}

	// init some counters
	d.fetchCount = atomic.Uint64{}
	d.fetchSizeBytes = atomic.Uint64{}

	log.Info("downloading inventory files")

	// download all inventory files from s3
	if err = d.downloadFiles(ctx, manifest); err != nil {
		return err
	}

	log.Info("downloading nar infos")

	// download nar infos based on the information in the inventory files
	if err = d.downloadNarInfos(ctx, *manifest); err != nil {
		return err
	}

	return nil
}

func (d *Downloader) downloadNarInfos(ctx context.Context, manifest Manifest) error {
	var err error

LOOP:
	for idx, file := range manifest.Files {
		select {
		case <-ctx.Done():
			break LOOP

		default:
			// check if it has already been downloaded
			var downloaded bool

			if err = d.db.View(func(tx *badger.Txn) error {
				// each parquet file has a unique hash in its file name
				// we store them into the db using this basename to keep things flat
				downloaded, err = HasFileNarInfosBeenDownloaded(tx, file)
				if err != nil {
					return fmt.Errorf("failed to check if file %s has been downloaded: %w", file.Key, err)
				}

				return nil
			}); err != nil {
				return err
			}

			if !d.cfg.Inventory.ForceNarInfoDownload && downloaded {
				log.Infof("[%d / %d] file %s has already been downloaded", idx+1, len(manifest.Files), file.Key)
				continue
			}

			log.Infof("[%d / %d] downloading nar infos for file %s", idx+1, len(manifest.Files), file.Key)

			if err := d.downloadNarInfosForFile(ctx, &file); err != nil {
				return fmt.Errorf("failed to download nar infos for file %s: %w", file.Key, err)
			}
		}
	}

	return nil
}

func (d *Downloader) downloadNarInfosForFile(ctx context.Context, file *ManifestFile) error {
	writeGroup := errgroup.Group{}

	entriesCh := make(chan *badger.Entry, d.batchSize*10)

	// start write processing
	writeGroup.Go(d.writeNarInfos(entriesCh))

	// create a group to download files concurrently
	// we limit the pool to 16 threads, which is the max connections per host that minio client is configured to use
	downloadGroup, downloadCtx := errgroup.WithContext(ctx)
	downloadGroup.SetLimit(2048)

	var (
		buf []byte
		err error
	)

	if err = d.db.View(func(tx *badger.Txn) error {
		buf, err = GetManifestFile(tx, file)
		if err != nil {
			return fmt.Errorf("failed to get file %s from local db: %w", file.Key, err)
		}

		return nil
	}); err != nil {
		return err
	}

	// read all the objects from the parquet file into memory
	objs, err := parquet.Read[Object](bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return errors.New("failed to read parquet file")
	}

	numRows := len(objs)

LOOP:
	for i := 0; i < numRows; i += d.batchSize {
		select {
		case <-downloadCtx.Done():
			break LOOP

		default:
			// work out the new batch slice within objs
			end := min(i+d.batchSize, len(objs))

			batch := objs[i:end]
			if len(batch) == 0 {
				// nothing more to process
				break LOOP
			}

			if err = d.downloadNarInfoBatch(downloadCtx, batch, downloadGroup, entriesCh); err != nil {
				return fmt.Errorf("failed to download nar info batch: %w", err)
			}
		}
	}

	if err = downloadGroup.Wait(); err != nil {
		return fmt.Errorf("failed to download nar infos: %w", err)
	}

	// no more entries to write
	close(entriesCh)

	if err = writeGroup.Wait(); err != nil {
		return fmt.Errorf("failed to finish writing nar infos to local db: %w", err)
	}

	// mark this file and all its narinfo files as downloaded
	if err = d.db.Update(func(tx *badger.Txn) error {
		return MarkManifestFileAsDownloaded(tx, file)
	}); err != nil {
		return fmt.Errorf("failed to mark file %s as downloaded: %w", file.Key, err)
	}

	return nil
}

func (d *Downloader) downloadNarInfoBatch(
	ctx context.Context,
	objects []Object,
	downloadGroup *errgroup.Group,
	entriesCh chan *badger.Entry,
) error {
	maxDownloadAttempts := 2

	tx := d.db.NewTransaction(false)
	defer tx.Discard()

	for _, obj := range objects {
		// we're only interested in narinfos
		if path.Ext(obj.Key) != ".narinfo" {
			continue
		}

		exists, err := HasNarInfo(tx, &obj)
		if err != nil {
			return fmt.Errorf("failed to check if narinfo %s is in local db: %w", obj.Key, err)
		}

		if exists {
			// verify the contents
			log.Debugf("narinfo %s already exists in local db", obj.Key)
			continue
		}

		downloadGroup.Go(d.downloadNarInfo(ctx, obj, maxDownloadAttempts, entriesCh))
	}

	return nil
}

func (d *Downloader) downloadNarInfo(
	ctx context.Context,
	obj Object,
	maxAttempts int,
	entriesCh chan *badger.Entry,
) func() error {
	return func() error {
		success := false

		for range maxAttempts {
			log.Debugf("downloading narinfo %s from s3", obj.Key)

			r, err := d.client.GetObject(ctx, obj.Bucket, obj.Key)

			if err != nil && strings.Contains(err.Error(), "connection reset by peer") {
				time.Sleep(1 * time.Second)
				continue
			} else if err != nil {
				return fmt.Errorf("failed to get object %s from s3: %w", obj.Key, err)
			}

			//nolint:errcheck
			defer r.Close()

			value, err := io.ReadAll(r)
			if err != nil {
				if strings.Contains(err.Error(), "connection reset by peer") {
					log.Warnf("connection reset by peer when reading object %s from s3, retrying in 1 second...", obj.Key)
					time.Sleep(1 * time.Second)

					continue
				}

				return fmt.Errorf("failed to read object %s bytes from s3: %w", obj.Key, err)
			}

			// decode the checksum from the parquet file and enforce that it's md5
			checksum, err := hex.DecodeString(obj.ETag)
			if err != nil {
				return fmt.Errorf("failed to decode ETag %s: %w", obj.ETag, err)
			}

			if len(checksum) != md5.Size {
				return fmt.Errorf("ETag %s is not a valid md5 checksum: length %d != %d", obj.ETag, len(checksum), md5.Size)
			}

			// compare what was downloaded with the checksum
			sum := md5.Sum(value) //nolint:gosec

			if !bytes.Equal(sum[:], checksum) {
				return fmt.Errorf(
					"checksum failed when downloading %s from s3: expected %s, found %s",
					obj.Key, obj.ETag, hex.EncodeToString(sum[:]),
				)
			}

			// append to the entries channel
			entriesCh <- &badger.Entry{
				Key:   ObjectKey(&obj),
				Value: value,
			}

			success = true

			break
		}

		if !success {
			return fmt.Errorf("failed to download narinfo %s from s3, max retries exceeded", obj.Key)
		}

		return nil
	}
}

func (d *Downloader) writeNarInfos(entriesCh <-chan *badger.Entry) func() error {
	return func() error {
		batch := make([]*badger.Entry, 0, d.batchSize)

		flush := func() error {
			if err := d.writeNarInfoBatch(batch); err != nil {
				return fmt.Errorf("failed to write nar infos: %w", err)
			}

			// reset the batch
			batch = batch[:0]

			return nil
		}

		for entry := range entriesCh {
			batch = append(batch, entry)
			if len(batch) == d.batchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}

		// flush any remaining entries
		return flush()
	}
}

func (d *Downloader) writeNarInfoBatch(batch []*badger.Entry) error {
	tx := d.db.NewTransaction(true)
	defer tx.Discard()

	for _, entry := range batch {
		if err := tx.SetEntry(entry); err != nil {
			return fmt.Errorf("failed to set entry: %w", err)
		}

		d.fetchSizeBytes.Add(uint64(len(entry.Value)))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	d.fetchCount.Add(uint64(len(batch)))

	//nolint:gosec
	log.Infof(
		"wrote %d nar infos to local db, total fetched = %s, total size = %s",
		len(batch), humanize.Comma(int64(d.fetchCount.Load())), humanize.Bytes(d.fetchSizeBytes.Load()),
	)

	return nil
}

func (d *Downloader) downloadFiles(ctx context.Context, manifest *Manifest) error {
	// create a group to download files concurrently
	downloadGroup, ctx := errgroup.WithContext(ctx)

	// we limit the pool to 16 threads, which is the max connections per host that minio client is configured to use
	downloadGroup.SetLimit(16)

	// determine which files we already have and which we need to download
	missingFiles, err := d.missingFiles(manifest.Files)
	if err != nil {
		return fmt.Errorf("failed to determine missing files: %w", err)
	}

	log.Infof("manifest has %d files, we need to fetch %d", len(manifest.Files), len(missingFiles))

	// create a channel for files we have fetched
	fetchedCh := make(chan *ManifestFile, 16)

	// create a separate group for writing files into boltdb
	// we use a single write routine to avoid contention
	writeGroup, ctx := errgroup.WithContext(ctx)
	writeGroup.Go(func() error {
		for file := range fetchedCh {
			log.Infof("writing file %s to local db", file.Key)

			if err = d.db.Update(func(tx *badger.Txn) error {
				return PutManifestFile(tx, file)
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

				//nolint:errcheck
				defer r.Close()

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

	log.Info("finished downloading manifest files")

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
		exists, err := HasManifestFile(tx, &file)
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
	if !errors.Is(err, ErrKeyNotFound) && err != nil {
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
