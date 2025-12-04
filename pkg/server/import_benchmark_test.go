package server_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // MD5 used only for checksums, not security
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/server"
	"github.com/parquet-go/parquet-go"
)

const (
	// Benchmark configuration.
	benchmarkSeed   = 42
	objectsPerFile  = 1_000_000
	numParquetFiles = 3
)

// objectTypeWeights defines the distribution of object types for realistic test data.
//
//nolint:gochecknoglobals // Test configuration constants.
var objectTypeWeights = []struct {
	prefix      string
	weight      int
	hashLen     int
	suffix      string
	compression []string
}{
	// 45% nar files.
	{"nar/", 45, 52, ".nar", []string{".zstd", ".xz", ".gz", ""}},
	// 45% narinfo files.
	{"", 45, 32, ".narinfo", []string{""}},
	// 5% ls files.
	{"", 5, 32, ".ls", []string{".xz", ".zstd", ""}},
	// 3% log files.
	{"log/", 3, 32, "-foo.drv", []string{".zstd", ".bz2", ""}},
	// 2% debug files.
	{"debuginfo/", 2, 40, ".debug", []string{".br", ".xz", ""}},
}

// randReader adapts rand.Rand to io.Reader for UUID generation.
type randReader struct {
	rng *rand.Rand
}

func (r *randReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r.rng.IntN(256))
	}

	return len(p), nil
}

// generateRandomHash generates a random hex string of the specified length.
func generateRandomHash(rng *rand.Rand, length int) string {
	numBytes := (length + 1) / 2

	buf := make([]byte, numBytes)
	for i := range buf {
		buf[i] = byte(rng.IntN(256))
	}

	return hex.EncodeToString(buf)[:length]
}

// generateTestObjects creates a slice of inventory.Object with realistic paths.
// Uses a seeded RNG for reproducibility.
func generateTestObjects(rng *rand.Rand, count int) []inventory.Object {
	objects := make([]inventory.Object, count)

	// Build cumulative weights for weighted random selection.
	totalWeight := 0
	for _, w := range objectTypeWeights {
		totalWeight += w.weight
	}

	for i := range count {
		// Select object type based on weights.
		r := rng.IntN(totalWeight)
		cumulative := 0

		var selected int

		for j, w := range objectTypeWeights {
			cumulative += w.weight
			if r < cumulative {
				selected = j
				break
			}
		}

		typeInfo := objectTypeWeights[selected]

		// Generate path.
		hash := generateRandomHash(rng, typeInfo.hashLen)
		compression := typeInfo.compression[rng.IntN(len(typeInfo.compression))]
		path := typeInfo.prefix + hash + typeInfo.suffix + compression

		// Generate realistic metadata.
		objects[i] = inventory.Object{
			Bucket:           "nix-cache",
			Key:              path,
			Size:             rng.Int64N(100_000_000) + 1000, // 1KB to 100MB
			LastModifiedDate: time.Now().Add(-time.Duration(rng.IntN(365*24)) * time.Hour).UnixMilli(),
			ETag:             fmt.Sprintf("\"%s\"", generateRandomHash(rng, 32)),
			StorageClass:     "STANDARD",
		}
	}

	return objects
}

// generateParquetFile creates a parquet file containing the specified number of objects.
func generateParquetFile(rng *rand.Rand, objectCount int) ([]byte, error) {
	objects := generateTestObjects(rng, objectCount)

	var buf bytes.Buffer
	if err := parquet.Write(&buf, objects); err != nil {
		return nil, fmt.Errorf("failed to write parquet: %w", err)
	}

	return buf.Bytes(), nil
}

// setupTestBadgerDB creates a temporary BadgerDB with test manifest and parquet files.
// Returns the database, report ID, and cleanup function.
func setupTestBadgerDB(tb testing.TB, seed uint64) (*badger.DB, string, func()) {
	tb.Helper()

	// Create temp directory.
	tempDir := tb.TempDir()

	// Open BadgerDB with minimal logging.
	opts := badger.DefaultOptions(filepath.Join(tempDir, "db")).
		WithLogger(nil) // Disable logging for benchmarks

	tempDB, err := badger.Open(opts)
	if err != nil {
		tb.Fatalf("failed to open badger DB: %v", err)
	}

	cleanup := func() {
		_ = tempDB.Close()
	}

	// Create seeded RNG for reproducibility.
	//nolint:gosec // Weak RNG is intentional for reproducible benchmark data.
	rng := rand.New(rand.NewPCG(seed, seed))

	// Generate parquet files and build manifest.
	manifestFiles := make([]inventory.ManifestFile, numParquetFiles)

	for i := range numParquetFiles {
		tb.Logf("Generating parquet file %d/%d with %d objects...", i+1, numParquetFiles, objectsPerFile)

		data, err := generateParquetFile(rng, objectsPerFile)
		if err != nil {
			cleanup()
			tb.Fatalf("failed to generate parquet file %d: %v", i, err)
		}

		// Calculate MD5 checksum.
		hash := md5.Sum(data) //nolint:gosec // MD5 is for checksum verification, not security
		checksum := hex.EncodeToString(hash[:])

		// Create manifest file entry with UUID key matching real S3 inventory format.
		// Format: nix-cache/nix-cache-inventory/data/<uuid>.parquet
		fileUUID := uuid.Must(uuid.NewRandomFromReader(&randReader{rng}))
		manifestFiles[i] = inventory.ManifestFile{
			Key:         "nix-cache/nix-cache-inventory/data/" + fileUUID.String() + ".parquet",
			Size:        uint64(len(data)),
			MD5Checksum: checksum,
			Data:        data,
		}
	}

	// Create manifest matching real S3 inventory format.
	manifest := &inventory.Manifest{
		Files:        manifestFiles,
		SourceBucket: "nix-cache",
		DestBucket:   "nix-cache-inventory",
		Version:      "2016-11-30",
		CreationTime: time.Now().Format(time.RFC3339),
		FileFormat:   "Parquet",
		FileSchema:   "Bucket, Key, Size, LastModifiedDate, ETag, StorageClass",
	}

	reportID := "benchmark-report"

	// Store manifest and files in BadgerDB.
	err = tempDB.Update(func(tx *badger.Txn) error {
		// Store manifest.
		if err := inventory.PutManifest(tx, reportID, manifest); err != nil {
			return fmt.Errorf("failed to put manifest: %w", err)
		}

		// Store each parquet file.
		for i := range manifestFiles {
			if err := inventory.PutManifestFile(tx, &manifestFiles[i]); err != nil {
				return fmt.Errorf("failed to put manifest file %d: %w", i, err)
			}
		}

		return nil
	})
	if err != nil {
		cleanup()
		tb.Fatalf("failed to populate badger: %v", err)
	}

	tb.Logf("Created BadgerDB with %d parquet files (%d objects each)", numParquetFiles, objectsPerFile)

	return tempDB, reportID, cleanup
}

// BenchmarkImport benchmarks the import of 3 million objects from BadgerDB to PostgreSQL.
func BenchmarkImport(b *testing.B) {
	ctx := context.Background()

	// Start PostgreSQL server.
	b.Log("Starting PostgreSQL server...")

	pgServer, err := startPostgresServer(ctx)
	if err != nil {
		b.Fatalf("failed to start postgres: %v", err)
	}

	defer pgServer.Cleanup()

	// Create database.
	dbName := fmt.Sprintf("benchmark_%d", testDBCount.Add(1))
	connStr := "host=" + pgServer.tempDir + " user=postgres dbname=postgres"

	// Create test database.
	pool, err := db.Connect(ctx, connStr, false)
	if err != nil {
		b.Fatalf("failed to connect to postgres: %v", err)
	}

	_, err = pool.Exec(ctx, "CREATE DATABASE "+dbName)
	if err != nil {
		pool.Close()
		b.Fatalf("failed to create database: %v", err)
	}

	pool.Close()

	// Connect to the new database with migrations.
	benchConnStr := "host=" + pgServer.tempDir + " user=postgres dbname=" + dbName

	pgPool, err := db.Connect(ctx, benchConnStr, true)
	if err != nil {
		b.Fatalf("failed to connect to benchmark database: %v", err)
	}

	defer pgPool.Close()

	b.Log("PostgreSQL ready, setting up test data...")

	// Setup BadgerDB with test data.
	badgerDB, reportID, cleanup := setupTestBadgerDB(b, benchmarkSeed)
	defer cleanup()

	totalObjects := objectsPerFile * numParquetFiles

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Clear state between iterations to allow re-import
		if _, err = pgPool.Exec(ctx, "TRUNCATE imported_manifest_file"); err != nil {
			b.Fatalf("failed to truncate imported_manifest_file: %v", err)
		}

		if _, err = pgPool.Exec(ctx, "TRUNCATE object"); err != nil {
			b.Fatalf("failed to truncate object: %v", err)
		}

		start := time.Now()

		// Run the import.
		if err = server.ImportManifestForTest(ctx, badgerDB, pgPool, reportID); err != nil {
			b.Fatalf("import failed: %v", err)
		}

		elapsed := time.Since(start)
		rate := float64(totalObjects) / elapsed.Seconds()

		b.ReportMetric(float64(totalObjects), "objects")
		b.ReportMetric(rate, "objects/sec")
	}
}
