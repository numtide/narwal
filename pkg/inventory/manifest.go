package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/minio/minio-go/v7"
)

// Manifest represents the structure of an S3 inventory manifest.json file.
type Manifest struct {
	Files        []ManifestFile `json:"files"`
	SourceBucket string         `json:"sourceBucket"`
	DestBucket   string         `json:"destinationBucket"`
	Version      string         `json:"version"`
	CreationTime string         `json:"creationTimestamp"`
	FileFormat   string         `json:"fileFormat"`
	FileSchema   string         `json:"fileSchema"`
}

// ManifestFile represents information about a single inventory file in the manifest.
type ManifestFile struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	MD5Checksum string `json:"MD5checksum"`

	Data []byte `json:"-"`
}

// GetManifest retrieves and parses the inventory manifest for a given report ID.
func (c *Client) GetManifest(ctx context.Context, reportID string) (*Manifest, error) {
	manifestKey := c.prefix + reportID + "/manifest.json"
	log.Debug("fetching inventory manifest", "bucket", c.bucketClient.BucketName(), "key", manifestKey)

	// Get the manifest.json file from S3
	reader, err := c.bucketClient.GetObject(ctx, manifestKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get manifest file: %w", err)
	}
	defer reader.Close() //nolint:errcheck

	// Parse the JSON manifest directly from the stream
	var manifest Manifest

	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	err = manifest.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate manifest: %w", err)
	}

	log.Debug("parsed inventory manifest", "files", len(manifest.Files))

	return &manifest, nil
}

// Validate performs basic validation on a manifest.
func (m *Manifest) Validate() error {
	if len(m.Files) == 0 {
		return errors.New("manifest contains no files")
	}

	if m.FileFormat == "" {
		return errors.New("manifest missing file format")
	}

	for i, file := range m.Files {
		if file.Key == "" {
			return fmt.Errorf("file %d missing key", i)
		}

		if file.Size <= 0 {
			return fmt.Errorf("file %d has invalid size: %d", i, file.Size)
		}
	}

	return nil
}

// TotalSize returns the total size of all files in the manifest.
func (m *Manifest) TotalSize() int64 {
	var total int64
	for _, file := range m.Files {
		total += file.Size
	}

	return total
}

func (d *Downloader) ensureManifest(ctx context.Context, report string) (*Manifest, error) {
	tx, err := d.db.Begin(true)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// ensure a rollback is called if a commit is never made
	//nolint:errcheck
	defer tx.Rollback()

	// try to get the manifest from local db
	log.Debugf("looking for manifest %s in local db", report)

	manifests, err := GetManifestBucket(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to get manifest bucket: %w", err)
	}

	manifest, err := manifests.Get(report)
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

	if err = manifests.Put(report, manifest); err != nil {
		return nil, fmt.Errorf("failed to put manifest %s in local db: %w", report, err)
	}

	// commit the transaction and return the manifest
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return manifest, nil
}
