package inventory

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/numtide/narwal/pkg/config"
	bolt "go.etcd.io/bbolt"
)

//nolint:gochecknoglobals
var (
	BucketNameFile     = []byte("file")
	BucketNameManifest = []byte("manifest")

	ErrKeyNotFound = errors.New("key not found")
)

func OpenDB(cfg *config.Bolt) (*bolt.DB, error) {
	db, err := bolt.Open(cfg.Path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open bolt db: %w", err)
	}

	// ensure buckets are created
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err = tx.CreateBucketIfNotExists(BucketNameFile); err != nil {
			return fmt.Errorf("failed to create file bucket: %w", err)
		}

		if _, err = tx.CreateBucketIfNotExists(BucketNameManifest); err != nil {
			return fmt.Errorf("failed to create manifest bucket: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialise boltdb: %w", err)
	}

	return db, nil
}

type ManifestBucket struct {
	bucket *bolt.Bucket
}

func (b *ManifestBucket) Get(report string) (*Manifest, error) {
	buf := b.bucket.Get([]byte(report))
	if buf == nil {
		return nil, ErrKeyNotFound
	}

	var manifest Manifest
	if err := json.Unmarshal(buf, &manifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	return &manifest, nil
}

func (b *ManifestBucket) Put(report string, manifest *Manifest) error {
	buf, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err = b.bucket.Put([]byte(report), buf); err != nil {
		return fmt.Errorf("failed to put manifest: %w", err)
	}

	return nil
}

func GetManifestBucket(tx *bolt.Tx) (*ManifestBucket, error) {
	bucket := tx.Bucket(BucketNameManifest)
	if bucket == nil {
		return nil, errors.New("failed to get manifest bucket")
	}

	return &ManifestBucket{
		bucket: bucket,
	}, nil
}

type FileBucket struct {
	bucket *bolt.Bucket
}

func (b *FileBucket) Get(file string) ([]byte, error) {
	buf := b.bucket.Get([]byte(file))
	if buf == nil {
		return nil, ErrKeyNotFound
	}

	return buf, nil
}

func (b *FileBucket) Put(file string, buf []byte) error {
	if err := b.bucket.Put([]byte(file), buf); err != nil {
		return fmt.Errorf("failed to put file: %w", err)
	}

	return nil
}

func GetFileBucket(tx *bolt.Tx) (*FileBucket, error) {
	bucket := tx.Bucket(BucketNameFile)
	if bucket == nil {
		return nil, errors.New("failed to get manifest bucket")
	}

	return &FileBucket{
		bucket: bucket,
	}, nil
}
