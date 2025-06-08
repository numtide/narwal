package workarea_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/numtide/narwal/pkg/workarea"
)

const (
	testBucket  = "test-bucket"
	testFile    = "test-file.txt"
	testContent = "test content"
)

func TestNew(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	if wa.GetBasePath() != tempDir {
		t.Errorf("Expected base path %s, got %s", tempDir, wa.GetBasePath())
	}

	// Check that temp directory was created
	tempPath := filepath.Join(tempDir, ".tmp")
	if _, err := os.Stat(tempPath); os.IsNotExist(err) {
		t.Errorf("Temp directory was not created: %s", tempPath)
	}
}

func TestNewEmptyPath(t *testing.T) {
	t.Parallel()

	_, err := workarea.New("")
	if err == nil {
		t.Error("Expected error for empty base path, got nil")
	}
}

func TestGetPath(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := "path/to/file.txt"

	path := bucket.GetPath(key)

	// Should contain bucket name
	if !strings.Contains(path, "test-bucket") {
		t.Errorf("Path should contain bucket name: %s", path)
	}

	// Should contain sanitized key (slashes replaced with underscores)
	sanitizedKey := "path_to_file.txt"
	if !strings.Contains(path, sanitizedKey) {
		t.Errorf("Path should contain sanitized key: %s", path)
	}

	// Should be under base path
	if !strings.HasPrefix(path, tempDir) {
		t.Errorf("Path should be under base path: %s", path)
	}
}

func TestGetFileInfo(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := testFile

	// Test non-existing file
	info := bucket.GetFileInfo(key)
	if info.Exists {
		t.Error("File should not exist")
	}

	// Create a file manually
	path := bucket.GetPath(key)

	err = os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	content := []byte("test content")

	err = os.WriteFile(path, content, 0o600)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Test existing file
	info = bucket.GetFileInfo(key)

	if !info.Exists {
		t.Error("File should exist")
	}

	if info.Size != int64(len(content)) {
		t.Errorf("Expected size %d, got %d", len(content), info.Size)
	}
}

func TestExists(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := testFile

	// Test non-existing file
	if bucket.Exists(key) {
		t.Error("File should not exist")
	}

	// Create file using a mock S3 client
	client := newMockS3Client()
	content := testContent + " for downloading"
	client.addObject(testBucket, key, content)

	err = bucket.Download(t.Context(), client, key, nil)
	if err != nil {
		t.Fatalf("Failed to download content: %v", err)
	}

	// Test existing file
	if !bucket.Exists(key) {
		t.Error("File should exist")
	}
}

func TestDownload(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := testFile
	content := "This is test content for downloading"

	// Create mock S3 client
	client := newMockS3Client()
	client.addObject(testBucket, key, content)

	// Test downloading
	err = bucket.Download(t.Context(), client, key, nil)
	if err != nil {
		t.Fatalf("Failed to download content: %v", err)
	}

	// Verify file was created
	path := bucket.GetPath(key)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("File should have been created")
	}

	// Verify content
	savedContent, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("Failed to read saved file: %v", err)
	}

	if string(savedContent) != content {
		t.Errorf("Expected content %q, got %q", content, string(savedContent))
	}
}

func TestDownloadWithProgress(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := testFile
	content := "This is test content for downloading with progress tracking"

	var progressCalls []struct {
		bucket  string
		key     string
		written int64
		total   int64
	}

	progressCallback := func(b, k string, written, total int64) {
		progressCalls = append(progressCalls, struct {
			bucket  string
			key     string
			written int64
			total   int64
		}{b, k, written, total})
	}

	// Create mock S3 client
	client := newMockS3Client()
	client.addObject(testBucket, key, content)

	err = bucket.Download(t.Context(), client, key, progressCallback)
	if err != nil {
		t.Fatalf("Failed to download content: %v", err)
	}

	// Verify progress was tracked
	if len(progressCalls) == 0 {
		t.Error("Expected progress callbacks, got none")
	}

	// Check final progress call
	final := progressCalls[len(progressCalls)-1]
	if final.bucket != testBucket {
		t.Errorf("Expected bucket %s, got %s", testBucket, final.bucket)
	}

	if final.key != key {
		t.Errorf("Expected key %s, got %s", key, final.key)
	}

	if final.written != int64(len(content)) {
		t.Errorf("Expected written %d, got %d", len(content), final.written)
	}

	if final.total != int64(len(content)) {
		t.Errorf("Expected total %d, got %d", len(content), final.total)
	}
}

func TestDownloadSizeMismatch(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := testFile
	content := testContent

	// Create mock S3 client with mismatched content length
	client := newMockS3Client()
	client.objects[key] = &mockS3Object{
		content: content,
		size:    999, // Wrong size
	}

	err = bucket.Download(t.Context(), client, key, nil)

	if err == nil {
		t.Error("Expected error for size mismatch, got nil")
	}

	if !strings.Contains(err.Error(), "size mismatch") {
		t.Errorf("Expected size mismatch error, got: %v", err)
	}
}

func TestDownloadCancellation(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := testFile

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Create mock S3 client that will fail with context error
	client := newMockS3Client()
	client.errors[key] = context.Canceled

	err = bucket.Download(ctx, client, key, nil)
	if err == nil {
		t.Error("Expected error for cancelled context, got nil")
	}

	if !strings.Contains(err.Error(), "cancelled") &&
		!strings.Contains(err.Error(), "context canceled") &&
		!errors.Is(err, context.Canceled) {
		t.Errorf("Expected cancellation error, got: %v", err)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := "test-file.txt"
	content := "test content"

	// Create file first using mock S3 client
	client := newMockS3Client()
	client.addObject(testBucket, key, content)

	err = bucket.Download(t.Context(), client, key, nil)
	if err != nil {
		t.Fatalf("Failed to download content: %v", err)
	}

	// Verify file exists
	if !bucket.Exists(key) {
		t.Error("File should exist before removal")
	}

	// Remove file
	err = bucket.Remove(key)
	if err != nil {
		t.Fatalf("Failed to remove file: %v", err)
	}

	// Verify file is gone
	if bucket.Exists(key) {
		t.Error("File should not exist after removal")
	}
}

func TestRemoveNonExistent(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())

	// Should not error when removing non-existent file
	err = bucket.Remove("non-existent-file.txt")
	if err != nil {
		t.Errorf("Should not error when removing non-existent file: %v", err)
	}
}

func TestRemoveAll(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	client := newMockS3Client()

	// Create multiple files in the bucket
	files := []string{"file1.txt", "file2.txt", "dir/file3.txt"}
	for _, key := range files {
		// Create the file
		content := "content for " + key
		client.addObject(testBucket, key, content)

		err = bucket.Download(t.Context(), client, key, nil)
		if err != nil {
			t.Fatalf("Failed to download %s: %v", key, err)
		}
	}

	// Verify files exist
	for _, key := range files {
		if !bucket.Exists(key) {
			t.Errorf("File %s should exist", key)
		}
	}

	// Remove entire bucket
	err = bucket.RemoveAll()
	if err != nil {
		t.Fatalf("Failed to remove bucket: %v", err)
	}

	// Verify all files are gone
	for _, key := range files {
		if bucket.Exists(key) {
			t.Errorf("File %s should not exist after bucket removal", key)
		}
	}
}

func TestCleanTemp(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	// Create some temp files manually
	tempFiles := []string{"temp1.tmp", "temp2.tmp", "temp3.tmp"}
	for _, filename := range tempFiles {
		path := filepath.Join(wa.TempDir, filename)

		err = os.WriteFile(path, []byte("temp content"), 0o600)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
	}

	// Verify temp files exist
	for _, filename := range tempFiles {
		path := filepath.Join(wa.TempDir, filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Temp file should exist: %s", path)
		}
	}

	// Clean temp files
	err = wa.CleanTemp()
	if err != nil {
		t.Fatalf("Failed to clean temp files: %v", err)
	}

	// Verify temp files are gone
	for _, filename := range tempFiles {
		path := filepath.Join(wa.TempDir, filename)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Temp file should not exist after cleaning: %s", path)
		}
	}
}

func TestSanitizeForFilesystem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"normal-name", "normal-name"},
		{"name/with/slashes", "name_with_slashes"},
		{"name\\with\\backslashes", "name_with_backslashes"},
		{"name:with:colons", "name_with_colons"},
		{"name with spaces", "name_with_spaces"},
		{"name*with?special<chars>", "name_with_special_chars_"},
	}

	for _, test := range tests {
		result := workarea.SanitizeForFilesystem(test.input)
		if result != test.expected {
			t.Errorf("sanitizeForFilesystem(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestSanitizeLongName(t *testing.T) {
	t.Parallel()
	// Create a very long name
	longName := strings.Repeat("a", 250)
	result := workarea.SanitizeForFilesystem(longName)

	if len(result) > 200 {
		t.Errorf("Sanitized name should be at most 200 characters, got %d", len(result))
	}

	// Should end with hash for uniqueness
	if !strings.Contains(result, "_") {
		t.Error("Long name should contain hash suffix")
	}
}

func TestSharding(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket("test-bucket", workarea.DefaultBucketConfig())
	client := newMockS3Client()

	// Create files with different keys to test sharding
	keys := []string{
		"file1.txt",
		"file2.txt",
		"different-file.txt",
		"another/path/file.txt",
	}

	paths := make([]string, len(keys))
	for i, key := range keys {
		paths[i] = bucket.GetPath(key)

		// Create the file
		content := "content for " + key
		client.addObject("test-bucket", key, content)

		err = bucket.Download(t.Context(), client, key, nil)
		if err != nil {
			t.Fatalf("Failed to download %s: %v", key, err)
		}
	}

	// Verify files are in different shard directories (likely but not guaranteed)
	shards := make(map[string]bool)

	for _, path := range paths {
		parts := strings.Split(path, string(filepath.Separator))
		// Find the shard part (should be 5-character hex)
		for _, part := range parts {
			if len(part) == 5 && isHex(part) {
				shards[part] = true
				break
			}
		}
	}

	// We should have at least some sharding (though all files could theoretically hash to same shard)
	if len(shards) == 0 {
		t.Error("Expected to find shard directories")
	}
}

// Helper functions for tests

func TestCreateSymlink(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := "test-file.txt"
	content := "test content for symlink"

	// Create file first using mock S3 client
	client := newMockS3Client()
	client.addObject(testBucket, key, content)

	err = bucket.Download(t.Context(), client, key, nil)
	if err != nil {
		t.Fatalf("Failed to download content: %v", err)
	}

	// Create symlink
	symlinkPath := filepath.Join(tempDir, "symlinks", "linked-file.txt")

	err = bucket.CreateSymlink(key, symlinkPath)
	if err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Verify symlink exists and points to correct file
	linkInfo, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("Failed to stat symlink: %v", err)
	}

	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Error("Expected file to be a symlink")
	}

	// Verify symlink content
	symlinkContent, err := os.ReadFile(symlinkPath) //nolint:gosec
	if err != nil {
		t.Fatalf("Failed to read symlink content: %v", err)
	}

	if string(symlinkContent) != content {
		t.Errorf("Expected symlink content %q, got %q", content, string(symlinkContent))
	}
}

func TestCreateSymlinkNonExistentFile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	wa, err := workarea.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create work area: %v", err)
	}

	bucket := wa.Bucket(testBucket, workarea.DefaultBucketConfig())
	key := "non-existent-file.txt"

	// Try to create symlink to non-existent file
	symlinkPath := filepath.Join(tempDir, "symlinks", "linked-file.txt")
	err = bucket.CreateSymlink(key, symlinkPath)

	if err == nil {
		t.Error("Expected error when creating symlink to non-existent file")
	}

	if !strings.Contains(err.Error(), "cached file does not exist") {
		t.Errorf("Expected 'cached file does not exist' error, got: %v", err)
	}
}

// Helper functions for tests

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}

	return true
}
