package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/minio/minio-go/v7"
)

// InventoryManifest represents the structure of an S3 inventory manifest.json file.
type InventoryManifest struct {
	Files        []InventoryManifestInfo `json:"files"`
	SourceBucket string                  `json:"sourceBucket"`
	DestBucket   string                  `json:"destinationBucket"`
	Version      string                  `json:"version"`
	CreationTime string                  `json:"creationTimestamp"`
	FileFormat   string                  `json:"fileFormat"`
	FileSchema   string                  `json:"fileSchema"`
}

// InventoryManifestInfo represents information about a single inventory file in the manifest.
type InventoryManifestInfo struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	MD5Checksum string `json:"MD5checksum"`
}

// GetManifest retrieves and parses the inventory manifest for a given report ID.
func (c *Client) GetManifest(ctx context.Context, reportID string) (*InventoryManifest, error) {
	manifestKey := c.prefix + reportID + "/manifest.json"
	log.Debug("Fetching inventory manifest", "bucket", c.bucketClient.BucketName(), "key", manifestKey)

	// Get the manifest.json file from S3
	reader, err := c.bucketClient.GetObject(ctx, manifestKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get manifest file: %w", err)
	}
	defer reader.Close() //nolint:errcheck

	// Parse the JSON manifest directly from the stream
	var manifest InventoryManifest

	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	err = manifest.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate manifest: %w", err)
	}

	log.Debug("Parsed inventory manifest", "files", len(manifest.Files))

	return &manifest, nil
}

// ValidateManifest performs basic validation on a manifest.
func (m *InventoryManifest) Validate() error {
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
func (m *InventoryManifest) TotalSize() int64 {
	var total int64
	for _, file := range m.Files {
		total += file.Size
	}

	return total
}
