package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

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
	Size        uint64 `json:"size"`
	MD5Checksum string `json:"MD5checksum"`

	Data []byte `json:"-"`
}

func (m *ManifestFile) Basename() string {
	return path.Base(m.Key)
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
func (m *Manifest) TotalSize() uint64 {
	var total uint64
	for _, file := range m.Files {
		total += file.Size
	}

	return total
}
