package inventory

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/workarea"
)

// ResolvedParquetFiles contains the results of parquet file resolution
type ResolvedParquetFiles struct {
	Files             []string // Local paths to parquet files
	SkippedFiles      int      // Number of files skipped (non-parquet or missing)
	TotalManifestSize int64    // Total size of all files in manifest
}

// ParquetFileResolver handles resolving parquet files from manifest to local paths
type ParquetFileResolver struct {
	bucket   *workarea.Bucket
	manifest *InventoryManifest
}

// NewParquetFileResolver creates a new parquet file resolver
func NewParquetFileResolver(bucket *workarea.Bucket, manifest *InventoryManifest) *ParquetFileResolver {
	return &ParquetFileResolver{
		bucket:   bucket,
		manifest: manifest,
	}
}

// ResolveLocalFiles resolves parquet files from manifest to local paths
func (r *ParquetFileResolver) ResolveLocalFiles() (*ResolvedParquetFiles, error) {
	log.Info("Resolving parquet files from manifest...")

	parquetFiles := make([]string, 0, len(r.manifest.Files))
	var skippedFiles int
	var totalManifestSize int64

	for i, file := range r.manifest.Files {
		if !strings.HasSuffix(file.Key, ".parquet") {
			skippedFiles++
			continue // Skip non-parquet files
		}

		totalManifestSize += file.Size

		// Resolve the local path in workarea
		localPath := r.bucket.GetPath(file.Key)

		// Check if the local file exists
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			log.Warn("Parquet file from manifest not found locally, skipping",
				"file", fmt.Sprintf("%d/%d", i+1, len(r.manifest.Files)),
				"key", file.Key,
				"size_mb", fmt.Sprintf("%.1f", float64(file.Size)/(1024*1024)))

			skippedFiles++
			continue
		}

		log.Debug("Found local parquet file",
			"file", fmt.Sprintf("%d/%d", i+1, len(r.manifest.Files)),
			"key", file.Key,
			"size_mb", fmt.Sprintf("%.1f", float64(file.Size)/(1024*1024)),
			"local_path", localPath)

		parquetFiles = append(parquetFiles, localPath)
	}

	if len(parquetFiles) == 0 {
		return nil, fmt.Errorf("no parquet files from manifest found locally\nRun 'narwal inventory download' first to download the data")
	}

	log.Info("Resolved inventory files for processing",
		"manifest_files", len(r.manifest.Files),
		"parquet_files_found", len(parquetFiles),
		"skipped_files", skippedFiles,
		"total_data_size_gb", fmt.Sprintf("%.2f", float64(totalManifestSize)/(1024*1024*1024)))

	return &ResolvedParquetFiles{
		Files:             parquetFiles,
		SkippedFiles:      skippedFiles,
		TotalManifestSize: totalManifestSize,
	}, nil
}
