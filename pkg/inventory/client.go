package inventory

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
)

// S3Client defines the interface for S3 operations needed by the inventory client.
type S3Client interface {
	ListObjectsV2(
		ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options),
	) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// DownloadProgressCallback is called during file download to report progress.
type DownloadProgressCallback func(key string, downloaded int64, total int64)

// Client provides functionality to interact with S3 inventory data.
type Client struct {
	s3Client S3Client
	bucket   string
	prefix   string
	workDir  string
	tempDir  string
}

// NewClient creates a new inventory client with working directory management.
func NewClient(s3Client S3Client, bucket, prefix, workDir string) (*Client, error) {
	// Ensure prefix ends with a slash if not empty
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// Create working directory if it doesn't exist
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	// Create temporary directory for downloads
	tempDir := filepath.Join(workDir, ".tmp")
	if err := os.MkdirAll(tempDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	return &Client{
		s3Client: s3Client,
		bucket:   bucket,
		prefix:   prefix,
		workDir:  workDir,
		tempDir:  tempDir,
	}, nil
}

// GetDates returns a list of available inventory dates, ordered lexicographically.
func (c *Client) GetDates(ctx context.Context) ([]string, error) {
	log.Debug("Listing inventory dates", "bucket", c.bucket, "prefix", c.prefix)

	var dates []string

	paginator := s3.NewListObjectsV2Paginator(c.s3Client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(c.bucket),
		Prefix:    aws.String(c.prefix),
		Delimiter: aws.String("/"),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		// Extract date directories from common prefixes
		for _, commonPrefix := range page.CommonPrefixes {
			if commonPrefix.Prefix == nil {
				continue
			}

			// Extract the date part from the prefix
			// Example: "nix-cache/nix-cache-inventory/2025-06-03T01-00Z/" -> "2025-06-03T01-00Z"
			prefixStr := *commonPrefix.Prefix
			if strings.HasPrefix(prefixStr, c.prefix) {
				dateDir := strings.TrimPrefix(prefixStr, c.prefix)
				dateDir = strings.TrimSuffix(dateDir, "/")

				// Basic validation that this looks like a date directory
				if len(dateDir) > 0 && strings.Contains(dateDir, "T") {
					dates = append(dates, dateDir)
				}
			}
		}
	}

	// Sort lexicographically (which works for ISO 8601 date format)
	sort.Strings(dates)

	log.Debug("Found inventory dates", "count", len(dates), "dates", dates)

	return dates, nil
}

// DownloadFile downloads a single parquet file if not already cached and returns a parquet reader.
func (c *Client) DownloadFile(ctx context.Context, file InventoryManifestInfo, progressCallback DownloadProgressCallback) (ParquetFileReader, error) {
	localPath := filepath.Join(c.workDir, file.Key)

	// Check if file already exists and has correct size
	if fileInfo, err := os.Stat(localPath); err == nil {
		if fileInfo.Size() == file.Size {
			log.Debug("File already cached", "key", file.Key, "path", localPath)
			// Still call progress callback for cached files to maintain consistent behavior
			if progressCallback != nil {
				progressCallback(file.Key, file.Size, file.Size)
			}

			return NewParquetFileReader(localPath, file.Size)
		}
	}

	log.Debug("Downloading parquet file", "key", file.Key)

	if err := c.downloadFile(ctx, file.Key, file.Size, localPath, progressCallback); err != nil {
		return nil, err
	}

	return NewParquetFileReader(localPath, file.Size)
}

// downloadFile downloads a single file from S3 to the local path.
func (c *Client) downloadFile(ctx context.Context, key string, expectedSize int64, localPath string, progressCallback DownloadProgressCallback) error {
	// Create directory structure if needed
	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}

	// Create temporary file path
	tempPath := filepath.Join(c.tempDir, fmt.Sprintf("%s_%d.tmp", filepath.Base(key), time.Now().UnixNano()))

	// Get object from S3
	result, err := c.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to get object from S3: %w", err)
	}
	defer result.Body.Close() //nolint:errcheck

	// Create temporary file
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tempPath) //nolint:errcheck

	// Create progress reader if we have progress tracking
	var reader io.Reader = result.Body
	if progressCallback != nil {
		reader = &progressReader{
			reader:   result.Body,
			total:    expectedSize,
			key:      key,
			callback: progressCallback,
			ctx:      ctx,
		}
	}

	// Copy to temporary file
	_, err = io.Copy(tempFile, reader)
	_ = tempFile.Close()

	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	// Move temporary file to final location
	if err := os.Rename(tempPath, localPath); err != nil {
		return fmt.Errorf("failed to move file to final location: %w", err)
	}

	log.Debug("File downloaded successfully", "key", key, "path", localPath, "size", formatBytes(expectedSize))

	return nil
}

// GetLocalParquetPath returns the local path for a parquet file.
func (c *Client) GetLocalParquetPath(key string) string {
	return filepath.Join(c.workDir, key)
}

// progressReader wraps an io.Reader to provide download progress tracking.
type progressReader struct {
	reader     io.Reader
	total      int64
	downloaded int64
	key        string
	callback   DownloadProgressCallback
	ctx        context.Context
}

func (pr *progressReader) Read(p []byte) (int, error) {
	// Check if context was cancelled
	if pr.ctx.Err() != nil {
		return 0, pr.ctx.Err()
	}

	n, err := pr.reader.Read(p)
	pr.downloaded += int64(n)

	// Report progress
	if pr.callback != nil && n > 0 {
		pr.callback(pr.key, pr.downloaded, pr.total)
	}

	return n, err
}

// formatBytes formats byte count in human readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
