package inventory

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/workarea"
)

// ManifestNotFoundError represents an error when a manifest file is not found
type ManifestNotFoundError struct {
	Path string
}

func (e *ManifestNotFoundError) Error() string {
	return fmt.Sprintf("manifest not found at %s\nRun 'narwal inventory download' first to download the data", e.Path)
}

// LoadLocalManifest loads and parses a manifest.json file from the local workarea
func LoadLocalManifest(bucket *workarea.Bucket, prefix, reportID string) (*InventoryManifest, error) {
	manifestKey := fmt.Sprintf("%s%s/manifest.json", prefix, reportID)
	manifestPath := bucket.GetPath(manifestKey)

	// Check if manifest exists
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return nil, &ManifestNotFoundError{Path: manifestPath}
	}

	// Read and parse the manifest
	log.Info("Reading inventory manifest", "manifest_path", manifestPath)

	manifestFile, err := os.Open(manifestPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to open manifest file: %w", err)
	}
	defer manifestFile.Close() //nolint:errcheck

	var manifest InventoryManifest

	decoder := json.NewDecoder(manifestFile)

	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	log.Info("Parsed inventory manifest",
		"total_files", len(manifest.Files),
		"source_bucket", manifest.SourceBucket,
		"creation_time", manifest.CreationTime)

	return &manifest, nil
}
