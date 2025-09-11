package inventory

import (
	"encoding/json"
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

//nolint:gochecknoglobals
var (
	BucketNameManifest = []byte("manifest")
	BucketNameFile     = []byte("file")

	ErrKeyNotFound = errors.New("key not found")
)

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
	var (
		err    error
		bucket *bolt.Bucket
	)

	if tx.Writable() {
		bucket, err = tx.CreateBucketIfNotExists(BucketNameManifest)
		if err != nil {
			return nil, fmt.Errorf("failed to create manifest bucket: %w", err)
		}
	} else {
		bucket = tx.Bucket(BucketNameManifest)
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
	var (
		err    error
		bucket *bolt.Bucket
	)

	if tx.Writable() {
		bucket, err = tx.CreateBucketIfNotExists(BucketNameFile)
		if err != nil {
			return nil, fmt.Errorf("failed to create file bucket: %w", err)
		}
	} else {
		bucket = tx.Bucket(BucketNameFile)
	}

	return &FileBucket{bucket: bucket}, nil
}
