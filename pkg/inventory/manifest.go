package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
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

// GetManifest retrieves and parses the inventory manifest for a given date.
func (c *Client) GetManifest(ctx context.Context, date string) (*InventoryManifest, error) {
	manifestKey := c.prefix + date + "/manifest.json"
	log.Debug("Fetching inventory manifest", "bucket", c.bucket, "key", manifestKey)

	// Get the manifest.json file from S3
	result, err := c.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(manifestKey),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get manifest file: %w", err)
	}
	defer result.Body.Close()

	// Parse the JSON manifest directly from the stream
	var manifest InventoryManifest

	decoder := json.NewDecoder(result.Body)
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
