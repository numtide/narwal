package workarea

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/minio/minio-go/v7"
)

// WorkArea provides a local caching area for S3 content organized by bucket and key.
// Files are sharded by prefix to avoid hitting filesystem inode limits.
type WorkArea struct {
	BasePath string
	TempDir  string
	log      *log.Logger
}

// Bucket provides bucket-specific operations within a WorkArea.
type Bucket struct {
	workArea   *WorkArea
	bucketName string
	log        *log.Logger
}

// FileInfo contains metadata about a cached file.
type FileInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
	Exists  bool
}

// DownloadProgressCallback is called during download to report progress.
type DownloadProgressCallback func(bucket, key string, written, total int64)

// ObjectReader defines the interface for reading S3 objects.
type ObjectReader interface {
	io.Reader
	io.Closer
	Stat() (minio.ObjectInfo, error)
}

// S3Client defines the interface for S3 operations needed by the work area.
type S3Client interface {
	GetObject(ctx context.Context, key string, opts minio.GetObjectOptions) (ObjectReader, error)
}

// BucketClientAdapter adapts awssdk.BucketClient to the S3Client interface.
type BucketClientAdapter struct {
	bucketClient interface {
		GetObject(ctx context.Context, key string, opts minio.GetObjectOptions) (*minio.Object, error)
	}
}

// NewBucketClientAdapter creates an adapter for awssdk.BucketClient.
func NewBucketClientAdapter(bucketClient interface {
	GetObject(ctx context.Context, key string, opts minio.GetObjectOptions) (*minio.Object, error)
},
) *BucketClientAdapter {
	return &BucketClientAdapter{
		bucketClient: bucketClient,
	}
}

// GetObject implements S3Client interface by wrapping awssdk.BucketClient.
func (a *BucketClientAdapter) GetObject( //nolint:ireturn
	ctx context.Context, key string, opts minio.GetObjectOptions,
) (ObjectReader, error) {
	return a.bucketClient.GetObject(ctx, key, opts) //nolint:wrapcheck
}

// New creates a new WorkArea with the specified base path.
func New(basePath string) (*WorkArea, error) {
	if basePath == "" {
		return nil, errors.New("base path cannot be empty")
	}

	// Create base directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	// Create temporary directory for atomic operations
	tempDir := filepath.Join(basePath, ".tmp")
	if err := os.MkdirAll(tempDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	return &WorkArea{
		BasePath: basePath,
		TempDir:  tempDir,
		log:      log.WithPrefix("workarea"),
	}, nil
}

// Bucket returns a Bucket instance for the specified bucket name.
func (wa *WorkArea) Bucket(bucketName string) *Bucket {
	return &Bucket{
		workArea:   wa,
		bucketName: bucketName,
		log:        wa.log.WithPrefix("bucket:" + bucketName),
	}
}

// CleanTemp removes all temporary files that may have been left behind.
func (wa *WorkArea) CleanTemp() error {
	entries, err := os.ReadDir(wa.TempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Temp dir doesn't exist, nothing to clean
		}

		return fmt.Errorf("failed to read temp directory: %w", err)
	}

	var errors []string

	cleaned := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		tempFile := filepath.Join(wa.TempDir, entry.Name())
		if err := os.Remove(tempFile); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", entry.Name(), err))
		} else {
			cleaned++
		}
	}

	if len(errors) > 0 {
		wa.log.Warn("Some temp files could not be cleaned", "errors", errors, "cleaned", cleaned)
		return fmt.Errorf("failed to clean some temp files: %s", strings.Join(errors, ", "))
	}

	if cleaned > 0 {
		wa.log.Debug("Temp files cleaned", "count", cleaned)
	}

	return nil
}

// GetBasePath returns the base path of the work area.
func (wa *WorkArea) GetBasePath() string {
	return wa.BasePath
}

// GetPath returns the local path where a file would be stored for the given key.
func (b *Bucket) GetPath(key string) string {
	return b.workArea.getShardedPath(b.bucketName, key)
}

// GetFileInfo returns information about a cached file.
func (b *Bucket) GetFileInfo(key string) *FileInfo {
	path := b.workArea.getShardedPath(b.bucketName, key)

	info := &FileInfo{
		Path:   path,
		Exists: false,
	}

	if stat, err := os.Stat(path); err == nil {
		info.Exists = true
		info.Size = stat.Size()
		info.ModTime = stat.ModTime()
	}

	return info
}

// Exists checks if a file exists in the cache.
func (b *Bucket) Exists(key string) bool {
	info := b.GetFileInfo(key)
	return info.Exists
}

// Download downloads an S3 object and stores it in the work area.
// The operation is atomic - content is written to a temporary file first, then moved.
func (b *Bucket) Download(ctx context.Context, s3Client S3Client, key string,
	progressCallback DownloadProgressCallback,
) error {
	// Check if file already exists
	info := b.GetFileInfo(key)
	if info.Exists {
		b.log.Debug("File already cached", "key", key, "path", info.Path)
		// Still call progress callback for cached files to maintain consistent behavior
		if progressCallback != nil {
			progressCallback(b.bucketName, key, info.Size, info.Size)
		}

		return nil
	}

	b.log.Debug("Downloading from S3", "key", key)

	// Get object from S3
	object, err := s3Client.GetObject(ctx, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to get object from S3: %w", err)
	}

	defer func() {
		if closeErr := object.Close(); closeErr != nil {
			b.log.Warn("Failed to close S3 response body", "error", closeErr)
		}
	}()

	// Get expected size from S3 response
	stat, err := object.Stat()
	if err != nil {
		return fmt.Errorf("failed to get object stats: %w", err)
	}

	expectedSize := stat.Size

	targetPath := b.workArea.getShardedPath(b.bucketName, key)

	// Create directory structure
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}

	// Create temporary file for atomic operation
	tempFile := filepath.Join(b.workArea.TempDir, fmt.Sprintf("%s_%s_%d.tmp",
		SanitizeForFilesystem(b.bucketName),
		SanitizeForFilesystem(key),
		time.Now().UnixNano()))

	file, err := os.Create(tempFile) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}

	defer func() {
		_ = file.Close()
		_ = os.Remove(tempFile)
	}()

	// Create progress tracking writer if callback provided
	var writer io.Writer = file

	var progressTracker *progressWriter

	if progressCallback != nil {
		progressTracker = &progressWriter{
			writer:   file,
			bucket:   b.bucketName,
			key:      key,
			total:    expectedSize,
			callback: progressCallback,
		}
		writer = progressTracker
	}

	// Copy content to temporary file
	written, err := io.Copy(writer, object)
	if err != nil {
		return fmt.Errorf("failed to write content: %w", err)
	}

	// Close the file before moving
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Validate size if expected size was provided
	if expectedSize > 0 && written != expectedSize {
		return fmt.Errorf("size mismatch: expected %d bytes, got %d bytes", expectedSize, written)
	}

	// Atomically move temporary file to target location
	if err := os.Rename(tempFile, targetPath); err != nil {
		return fmt.Errorf("failed to move file to target location: %w", err)
	}

	b.log.Debug("File cached successfully",
		"key", key,
		"path", targetPath,
		"size", written,
	)

	return nil
}

// Remove deletes a cached file.
func (b *Bucket) Remove(key string) error {
	path := b.workArea.getShardedPath(b.bucketName, key)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove cached file: %w", err)
	}

	b.log.Debug("File removed from cache", "key", key, "path", path)

	return nil
}

// RemoveAll removes all cached files for this bucket.
func (b *Bucket) RemoveAll() error {
	safeBucket := SanitizeForFilesystem(b.bucketName)
	bucketPath := filepath.Join(b.workArea.BasePath, safeBucket)

	if err := os.RemoveAll(bucketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove bucket cache: %w", err)
	}

	b.log.Debug("Bucket cache removed", "path", bucketPath)

	return nil
}

// getShardedPath returns the full local path for a bucket/key combination.
// Uses the first 5 characters of the key's SHA256 hash for sharding.
func (wa *WorkArea) getShardedPath(bucket, key string) string {
	// Create a deterministic shard based on the key
	hash := sha256.Sum256([]byte(key))
	shard := hex.EncodeToString(hash[:3])[:5]

	// Sanitize bucket name and key for filesystem use
	safeBucket := SanitizeForFilesystem(bucket)
	safeKey := SanitizeForFilesystem(key)

	// Build the path: basePath/bucket/shard/key
	return filepath.Join(wa.BasePath, safeBucket, shard, safeKey)
}

// progressWriter wraps an io.Writer to provide progress tracking.
type progressWriter struct {
	writer   io.Writer
	bucket   string
	key      string
	total    int64
	written  int64
	callback DownloadProgressCallback
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	pw.written += int64(n)

	// Report progress
	if pw.callback != nil && n > 0 {
		pw.callback(pw.bucket, pw.key, pw.written, pw.total)
	}

	if err != nil {
		return n, fmt.Errorf("write failed: %w", err)
	}

	return n, nil
}

// SanitizeForFilesystem removes or replaces characters that are not safe for filesystem use.
func SanitizeForFilesystem(name string) string {
	// Replace problematic characters with underscores
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)

	result := replacer.Replace(name)

	// Ensure it's not too long (filesystem limits)
	if len(result) > 200 {
		// Take first 189 chars and add hash suffix for uniqueness (189 + 11 = 200)
		hash := sha256.Sum256([]byte(name))
		result = result[:189] + fmt.Sprintf("_%x", hash[:5])
	}

	return result
}
