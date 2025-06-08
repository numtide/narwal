package workarea_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/workarea"
)

// mockS3Client for examples.
type mockS3Client struct {
	objects map[string]*mockS3Object
	errors  map[string]error
}

type mockS3Object struct {
	content string
	size    int64
}

// mockMinioObject implements the minio.Object interface for testing.
type mockMinioObject struct {
	reader io.Reader
	size   int64
}

// Read implements io.Reader.
func (m *mockMinioObject) Read(p []byte) (int, error) {
	return m.reader.Read(p) //nolint:wrapcheck
}

// Close implements io.Closer.
func (m *mockMinioObject) Close() error {
	return nil
}

// Stat returns object stats.
func (m *mockMinioObject) Stat() (minio.ObjectInfo, error) {
	return minio.ObjectInfo{
		Size: m.size,
	}, nil
}

// GetObject implements the S3Client interface.
//
//nolint:ireturn,nolintlint
func (m *mockS3Client) GetObject(
	ctx context.Context, key string, opts minio.GetObjectOptions,
) (workarea.ObjectReader, error) {
	// Check for configured errors first
	if err, exists := m.errors[key]; exists {
		return nil, err
	}

	// Check if object exists
	obj, exists := m.objects[key]
	if !exists {
		return nil, errors.New("NoSuchKey: The specified key does not exist")
	}

	// Create a mock minio.Object
	mockObject := &mockMinioObject{
		reader: strings.NewReader(obj.content),
		size:   obj.size,
	}

	return mockObject, nil
}

// newMockS3Client creates a new mock S3 client for testing.
func newMockS3Client() *mockS3Client {
	return &mockS3Client{
		objects: make(map[string]*mockS3Object),
		errors:  make(map[string]error),
	}
}

// addObject adds an object to the mock S3 client.
func (m *mockS3Client) addObject(bucket, key, content string) { //nolint:unparam
	// Since BucketClient is bound to a bucket, we only store the key without bucket prefix
	_ = bucket // unused parameter
	m.objects[key] = &mockS3Object{
		content: content,
		size:    int64(len(content)),
	}
}

func ExampleWorkArea_basic() {
	// Create a new work area
	wa, err := workarea.New("/tmp/cache")
	if err != nil {
		log.Fatal(err)
	}

	bucketName := "my-s3-bucket"
	key := "data/inventory/2025-01-01/manifest.json"
	content := `{"version": "1.0", "files": ["file1.parquet", "file2.parquet"]}`

	// Create mock S3 client (in real usage, you'd use awssdk.BucketClient)
	client := &mockS3Client{
		objects: map[string]*mockS3Object{
			key: {
				content: content,
				size:    int64(len(content)),
			},
		},
	}

	// Get bucket interface
	bucket := wa.Bucket(bucketName, workarea.DefaultBucketConfig())

	// Download content to cache
	err = bucket.Download(context.Background(), client, key, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Check if file exists
	exists := bucket.Exists(key)
	fmt.Printf("File cached: %t\n", exists)

	// Get file path
	path := bucket.GetPath(key)
	fmt.Printf("Cached at: %s\n", path)

	// Output:
	// File cached: true
	// Cached at: /tmp/cache/my-s3-bucket/7af7e/data_inventory_2025-01-01_manifest.json
}

func ExampleWorkArea_withProgress() { //nolint:testableexamples
	wa, err := workarea.New("/tmp/cache")
	if err != nil {
		log.Fatal(err)
	}

	bucketName := "downloads"
	key := "large-file.dat"
	content := strings.Repeat("data", 1000) // 4KB of data

	// Create mock S3 client
	client := &mockS3Client{
		objects: map[string]*mockS3Object{
			key: {
				content: content,
				size:    int64(len(content)),
			},
		},
	}

	// Get bucket interface
	bucket := wa.Bucket(bucketName, workarea.DefaultBucketConfig())

	// Progress tracking
	progressCallback := func(bucket, key string, written, total int64) {
		percent := float64(written) / float64(total) * 100
		fmt.Printf("Progress: %.1f%% (%d/%d bytes)\n", percent, written, total)
	}

	err = bucket.Download(context.Background(), client, key, progressCallback)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Download complete!")
}

func ExampleWorkArea_cacheManagement() { //nolint:testableexamples
	wa, err := workarea.New("/tmp/cache")
	if err != nil {
		log.Fatal(err)
	}

	bucketName := "temp-data"
	bucket := wa.Bucket(bucketName, workarea.DefaultBucketConfig())

	// Cache multiple files
	files := map[string]string{
		"file1.txt":     "content1",
		"file2.txt":     "content2",
		"dir/file3.txt": "content3",
	}

	// Create mock S3 client with all files
	client := &mockS3Client{
		objects: make(map[string]*mockS3Object),
	}
	for key, content := range files {
		client.objects[key] = &mockS3Object{
			content: content,
			size:    int64(len(content)),
		}
	}

	for key := range files {
		if err := bucket.Download(context.Background(), client, key, nil); err != nil {
			log.Fatal(err)
		}
	}

	// Check what's cached
	for key := range files {
		exists := bucket.Exists(key)
		fmt.Printf("%s cached: %t\n", key, exists)
	}

	// Remove specific file
	if err := bucket.Remove("file1.txt"); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("file1.txt after removal: %t\n", bucket.Exists("file1.txt"))

	// Remove entire bucket cache
	if err := bucket.RemoveAll(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("file2.txt after bucket removal: %t\n", bucket.Exists("file2.txt"))

	// Clean up all temporary files
	if err := wa.CleanTemp(); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Temporary files cleaned")
}

func ExampleWorkArea_fileInfo() { //nolint:testableexamples
	wa, err := workarea.New("/tmp/cache")
	if err != nil {
		log.Fatal(err)
	}

	bucketName := "analytics"
	key := "report.csv"
	content := "date,value\n2025-01-01,100\n2025-01-02,200"

	// Create mock S3 client
	client := &mockS3Client{
		objects: map[string]*mockS3Object{
			key: {
				content: content,
				size:    int64(len(content)),
			},
		},
	}

	// Get bucket interface
	bucket := wa.Bucket(bucketName, workarea.DefaultBucketConfig())

	// Cache the file
	if err := bucket.Download(context.Background(), client, key, nil); err != nil {
		log.Fatal(err)
	}

	// Get detailed file information
	info := bucket.GetFileInfo(key)
	fmt.Printf("Path: %s\n", info.Path)
	fmt.Printf("Exists: %t\n", info.Exists)
	fmt.Printf("Size: %d bytes\n", info.Size)
	fmt.Printf("Modified: %v\n", info.ModTime.Format("2006-01-02 15:04:05"))
}
