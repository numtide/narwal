package inventory

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

func (c *Client) GetFile(ctx context.Context, file ManifestFile) (io.ReadCloser, error) {
	obj, err := c.bucketClient.GetObject(ctx, file.Key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get file %s: %w", file.Key, err)
	}

	return obj, nil
}

func (d *Downloader) filterFiles(files []ManifestFile) ([]ManifestFile, error) {
	tx, err := d.db.Begin(false)
	if err != nil {
		return nil, fmt.Errorf("failed to begin read transaction: %w", err)
	}

	// ensure a rollback is called if a commit is never made
	//nolint:errcheck
	defer tx.Rollback()

	bucket, err := GetFileBucket(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to get file bucket: %w", err)
	}

	filtered := make([]ManifestFile, 0, len(files))

	for _, file := range files {
		_, err := bucket.Get(file.Key)
		if errors.Is(err, ErrKeyNotFound) {
			filtered = append(filtered, file)
			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to get file %s from local db: %w", file.Key, err)
		}
	}

	return filtered, nil
}
