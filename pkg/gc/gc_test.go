//nolint:gochecknoglobals
package gc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/cmd"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/gc"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// nixBase32Alphabet is the base32 alphabet used by Nix for store paths.
// It excludes e, o, t, u to avoid confusion with similar-looking characters.
const nixBase32Alphabet = "0123456789abcdfghijklmnpqrsvwxyz"

// nixHashLength is the length of a Nix store path hash (32 characters).
const nixHashLength = 32

// packageNames contains real nixpkgs package names for realistic test data.
var packageNames = []string{
	"firefox", "chromium", "thunderbird", "libreoffice", "gimp",
	"inkscape", "blender", "vlc", "mpv", "ffmpeg",
	"git", "vim", "neovim", "emacs", "vscode",
	"tmux", "zsh", "fish", "bash", "coreutils",
	"nginx", "apache", "caddy", "traefik", "haproxy",
	"docker", "podman", "kubernetes", "terraform", "ansible",
	"python3", "nodejs", "go", "rustc", "gcc",
	"clang", "llvm", "cmake", "ninja", "meson",
	"openssl", "curl", "wget", "jq", "ripgrep",
}

// packageVersions contains realistic version strings.
var packageVersions = []string{
	"1.0.0", "1.0.1", "1.1.0", "1.2.0", "1.2.3",
	"2.0.0", "2.1.0", "2.2.0", "2.3.1", "2.4.0",
	"3.0.0", "3.1.0", "3.2.0", "3.3.0", "3.4.0",
	"23.05", "23.11", "24.05", "24.11", "unstable",
}

// generateNixHash generates a random but valid-looking Nix base32 hash.
func generateNixHash(rng *rand.Rand) string {
	hash := make([]byte, nixHashLength)
	for i := range hash {
		hash[i] = nixBase32Alphabet[rng.Intn(len(nixBase32Alphabet))]
	}

	return string(hash)
}

// generateStorePath generates a Nix store path for a package with the given name and version.
func generateStorePath(rng *rand.Rand, name, version string) string {
	hash := generateNixHash(rng)

	return "/nix/store/" + hash + "-" + name + "-" + version
}

// narInfo stores the FileHash and FileSize for a NAR uploaded to S3.
type narInfo struct {
	fileHash [32]byte
	fileSize uint64
}

// s3TestGenerator generates test data for S3-only GC tests.
type s3TestGenerator struct {
	tb           testing.TB
	rng          *rand.Rand
	bucketClient *awssdk.BucketClient

	// uploadedNars maps store path to NAR file info
	uploadedNars   map[string]narInfo
	uploadedNarsMu sync.Mutex
}

// newS3TestGenerator creates a generator for S3-only test data.
func newS3TestGenerator(tb testing.TB, bucketClient *awssdk.BucketClient) *s3TestGenerator {
	tb.Helper()

	// Use FNV hash of test name for a deterministic seed
	h := fnv.New64a()
	if _, err := h.Write([]byte(tb.Name())); err != nil {
		tb.Fatalf("failed to hash test name: %v", err)
	}

	seed := int64(h.Sum64()) //nolint:gosec

	return &s3TestGenerator{
		tb:           tb,
		rng:          rand.New(rand.NewSource(seed)), //nolint:gosec
		bucketClient: bucketClient,
		uploadedNars: make(map[string]narInfo),
	}
}

// generate creates test data by uploading narinfo/nar files to S3.
func (g *s3TestGenerator) generate(count int) {
	tb := g.tb
	tb.Helper()

	numWorkers := 16

	tb.Logf("Uploading %d narinfo/nar file pairs to S3 with %d workers...", count, numWorkers)

	start := time.Now()

	// Generate store paths
	storePaths := make([]string, count)
	for i := range count {
		pkgName := packageNames[g.rng.Intn(len(packageNames))]
		version := packageVersions[g.rng.Intn(len(packageVersions))]
		storePaths[i] = generateStorePath(g.rng, pkgName, version)
	}

	// Pre-generate worker seeds before spawning goroutines (rand.Rand is not safe for concurrent use)
	workerSeeds := make([]int64, numWorkers)
	for i := range numWorkers {
		workerSeeds[i] = g.rng.Int63()
	}

	// Create a channel to distribute work
	pathChan := make(chan string, 256)

	// Track progress atomically
	var uploaded atomic.Int64

	// Create an errgroup for executing the workers
	eg, egCtx := errgroup.WithContext(tb.Context())

	// Start the workers
	for i := range numWorkers {
		workerSeed := workerSeeds[i]

		eg.Go(func() error {
			// Each worker needs its own RNG for deterministic but independent random data
			workerRng := rand.New(rand.NewSource(workerSeed)) //nolint:gosec

			for storePath := range pathChan {
				if uploadErr := g.uploadToS3(egCtx, storePath, workerRng); uploadErr != nil {
					return uploadErr
				}

				count := uploaded.Add(1)
				if count%1000 == 0 {
					tb.Logf("Uploaded %d files...", count)
				}
			}

			return nil
		})
	}

	// Send work to the workers
LOOP:
	for _, storePath := range storePaths {
		select {
		case pathChan <- storePath:
		case <-egCtx.Done():
			break LOOP
		}
	}

	// Close the channel to signal no more work
	close(pathChan)

	// Wait for all workers to complete
	if err := eg.Wait(); err != nil {
		tb.Fatalf("failed to upload narinfo/nar files: %v", err)
	}

	tb.Logf("S3 upload complete in %s: %d file pairs", time.Since(start).Round(time.Millisecond), len(storePaths))
}

// uploadToS3 uploads a narinfo file and corresponding NAR file to S3.
func (g *s3TestGenerator) uploadToS3(ctx context.Context, storePath string, rng *rand.Rand) error {
	// Generate random NAR bytes (small, 64-256 bytes)
	narSize := 64 + rng.Intn(193) // 64-256 bytes
	narBytes := make([]byte, narSize)

	_, err := rng.Read(narBytes)
	if err != nil {
		return fmt.Errorf("failed to generate random NAR bytes: %w", err)
	}

	// Calculate SHA256 hash of NAR content
	narHash := sha256.Sum256(narBytes)
	fileHashStr := nixbase32.EncodeToString(narHash[:])

	// Store the file info for later use by generateGCTargets
	g.uploadedNarsMu.Lock()
	g.uploadedNars[storePath] = narInfo{
		fileHash: narHash,
		fileSize: uint64(narSize), //nolint:gosec // narSize is small (64-256 bytes)
	}
	g.uploadedNarsMu.Unlock()

	// Extract store path hash for narinfo filename: /nix/store/<hash>-...
	storePathHash := storePath[11:43]
	narinfoKey := storePathHash + ".narinfo"
	narKey := "nar/" + fileHashStr + ".nar"

	// Generate narinfo content
	narinfoContent := fmt.Sprintf(`StorePaths: %s
URL: %s
Compression: none
FileHash: sha256:%s
FileSize: %d
NarHash: sha256:%s
NarSize: %d
References:
Deriver: unknown-deriver
`, storePath, narKey, fileHashStr, narSize, fileHashStr, narSize)

	// Upload NAR file
	err = g.bucketClient.PutObject(ctx, narKey,
		bytes.NewReader(narBytes), int64(narSize),
		"application/x-nix-nar")
	if err != nil {
		return fmt.Errorf("failed to upload NAR %s: %w", narKey, err)
	}

	// Upload narinfo file
	err = g.bucketClient.PutObject(ctx, narinfoKey,
		bytes.NewReader([]byte(narinfoContent)), int64(len(narinfoContent)),
		"text/x-nix-narinfo")
	if err != nil {
		return fmt.Errorf("failed to upload narinfo %s: %w", narinfoKey, err)
	}

	return nil
}

// generateGCTargets creates a parquet file containing NarInfoRecords for half
// of the uploaded store paths.
func (g *s3TestGenerator) generateGCTargets() (int, string) {
	tb := g.tb
	tb.Helper()

	if len(g.uploadedNars) == 0 {
		tb.Fatal("generateGCTargets called before generate() or no uploads occurred")
	}

	// Collect all store paths
	storePaths := make([]string, 0, len(g.uploadedNars))
	for path := range g.uploadedNars {
		storePaths = append(storePaths, path)
	}

	// Select half randomly
	numTargets := len(storePaths) / 2
	tb.Logf("Generating gc-targets.parquet with %d entries (half of %d)...", numTargets, len(storePaths))

	// Shuffle using generator's RNG for determinism
	g.rng.Shuffle(len(storePaths), func(i, j int) {
		storePaths[i], storePaths[j] = storePaths[j], storePaths[i]
	})
	selectedPaths := storePaths[:numTargets]

	// Create temp file
	gcTargetsPath := filepath.Join(tb.TempDir(), "gc-targets.parquet")

	file, err := os.Create(gcTargetsPath) //nolint:gosec // path from test temp dir
	if err != nil {
		tb.Fatalf("failed to create gc-targets file: %v", err)
	}

	// Create parquet writer
	writer := parquet.NewGenericWriter[inventory.NarInfoRecord](file)

	// Generate NarInfoRecords
	records := make([]inventory.NarInfoRecord, 0, numTargets)

	for _, storePath := range selectedPaths {
		info := g.uploadedNars[storePath]
		record := g.createNarInfoRecord(storePath, info)
		records = append(records, record)
	}

	// Write records
	if _, err := writer.Write(records); err != nil {
		_ = file.Close()

		tb.Fatalf("failed to write parquet records: %v", err)
	}

	if err := writer.Close(); err != nil {
		_ = file.Close()

		tb.Fatalf("failed to close parquet writer: %v", err)
	}

	if err := file.Close(); err != nil {
		tb.Fatalf("failed to close file: %v", err)
	}

	tb.Logf("Generated gc-targets.parquet with %d records at %s", len(records), gcTargetsPath)

	return len(records), gcTargetsPath
}

// createNarInfoRecord creates a NarInfoRecord from a store path and its associated NAR info.
func (g *s3TestGenerator) createNarInfoRecord(storePath string, info narInfo) inventory.NarInfoRecord {
	// Extract hash and pname from store path: /nix/store/<hash>-<pname>
	hashStr := storePath[11:43] // 32-char nixbase32 hash
	pname := storePath[44:]     // Everything after the dash

	// Decode hash from nixbase32 to bytes
	var hash [20]byte

	decoded, err := nixbase32.DecodeString(hashStr)
	if err == nil && len(decoded) == 20 {
		copy(hash[:], decoded)
	}

	return inventory.NarInfoRecord{
		Hash:        hash,
		Pname:       pname,
		Compression: "none",
		FileHash:    info.fileHash,
		FileSize:    info.fileSize,
		NarHash:     info.fileHash, // Same as FileHash since Compression is "none"
		NarSize:     info.fileSize,
	}
}

func TestSimpleGCStrategy(t *testing.T) {
	t.Parallel()

	// create a test bucket
	rustfs := getRustfsServer(t.Context())

	awsCfg, s3Cfg, bucketClient := rustfs.NewBucket(t)
	defer rustfs.CleanupBucket(t)

	// Generate test data in S3
	gen := newS3TestGenerator(t, bucketClient)
	gen.generate(100) // Upload 100 store paths

	// Generate GC targets parquet file containing half of the uploaded store paths
	targetCount, gcTargetsPath := gen.generateGCTargets()
	t.Logf("GC targets file: %s", gcTargetsPath)

	// Create output file path in temp directory
	outputFile := t.TempDir() + "/output.parquet"

	as := require.New(t)

	// Create the cobra command and execute "gc simple" with positional args
	rootCmd := cmd.New()
	rootCmd.SetArgs([]string{
		"gc", "simple",
		gcTargetsPath, // positional arg 1: input file
		outputFile,    // positional arg 2: output file
		"--aws.endpoint", awsCfg.Endpoint,
		fmt.Sprintf("--aws.use_ssl=%t", awsCfg.UseSSL),
		"--aws.credentials.access_key_id", awsCfg.Credentials.AccessKeyID,
		"--aws.credentials.secret_access_key", awsCfg.Credentials.SecretAccessKey,
		"--s3.bucket", s3Cfg.Bucket,
	})

	err := rootCmd.ExecuteContext(t.Context())
	as.NoError(err)

	// Confirm the output file exists
	as.FileExists(outputFile)

	// Validate the contents of the output file
	of, err := os.Open(outputFile) //nolint:gosec
	as.NoError(err)

	schema := parquet.SchemaOf(new(gc.RemovalRecord))
	pr := parquet.NewReader(of, schema)

	var (
		readErr     error
		record      gc.RemovalRecord
		recordCount int
	)

	for {
		readErr = pr.Read(&record)
		if errors.Is(readErr, io.EOF) {
			// no more records
			break
		} else if err != nil {
			t.Fatalf("failed to read from output file: %v", err)
		}

		recordCount++

		if record.Error != "" {
			t.Fatalf(
				"found error in output file for store path %s and key %s: %s",
				record.StorePath, record.Key, record.Error,
			)
		}
	}

	// for each target there is an associated nar file
	expectedRecordCount := targetCount * 2
	as.Equal(
		expectedRecordCount, recordCount,
		"expected %d records in output file, got %d", expectedRecordCount, recordCount,
	)
}

func TestSimpleGCStrategyDryRun(t *testing.T) {
	t.Parallel()

	// create a test bucket
	rustfs := getRustfsServer(t.Context())

	awsCfg, s3Cfg, bucketClient := rustfs.NewBucket(t)
	defer rustfs.CleanupBucket(t)

	// Generate test data in S3
	gen := newS3TestGenerator(t, bucketClient)
	gen.generate(100) // Upload 100 store paths

	// Count S3 objects before dry-run
	s3CountBefore := 0

	for obj := range bucketClient.ListObjects(t.Context(), "", true) {
		if obj.Err != nil {
			t.Fatalf("failed to list S3 objects before dry-run: %v", obj.Err)
		}

		s3CountBefore++
	}

	t.Logf("S3 objects before dry-run: %d", s3CountBefore)

	// Generate GC targets parquet file containing half of the uploaded store paths
	targetCount, gcTargetsPath := gen.generateGCTargets()
	t.Logf("GC targets file: %s with %d targets", gcTargetsPath, targetCount)

	// Create output file path in temp directory
	outputFile := t.TempDir() + "/output.parquet"

	as := require.New(t)

	// Create the cobra command and execute "gc simple" with --dry-run
	rootCmd := cmd.New()
	rootCmd.SetArgs([]string{
		"gc", "simple",
		gcTargetsPath, // positional arg 1: input file
		outputFile,    // positional arg 2: output file
		"--dry-run",
		"--aws.endpoint", awsCfg.Endpoint,
		fmt.Sprintf("--aws.use_ssl=%t", awsCfg.UseSSL),
		"--aws.credentials.access_key_id", awsCfg.Credentials.AccessKeyID,
		"--aws.credentials.secret_access_key", awsCfg.Credentials.SecretAccessKey,
		"--s3.bucket", s3Cfg.Bucket,
	})

	err := rootCmd.ExecuteContext(t.Context())
	as.NoError(err)

	// Confirm the output file exists
	as.FileExists(outputFile)

	// Verify that all S3 objects still exist (dry-run should not delete them)
	s3CountAfter := 0

	for obj := range bucketClient.ListObjects(t.Context(), "", true) {
		as.NoError(obj.Err, "failed to list S3 objects after dry-run")

		s3CountAfter++
	}

	as.Equal(s3CountBefore, s3CountAfter, "S3 object count should be unchanged after dry-run")

	// Validate the contents of the output file
	of, err := os.Open(outputFile) //nolint:gosec // outputFile from test temp dir
	as.NoError(err)

	schema := parquet.SchemaOf(new(gc.RemovalRecord))
	pr := parquet.NewReader(of, schema)

	var (
		readErr     error
		record      gc.RemovalRecord
		recordCount int
	)

	for {
		readErr = pr.Read(&record)
		if errors.Is(readErr, io.EOF) {
			// no more records
			break
		} else if err != nil {
			t.Fatalf("failed to read from output file: %v", err)
		}

		recordCount++

		if record.Error != "" {
			t.Fatalf(
				"found error in output file for store path %s and key %s: %s",
				record.StorePath, record.Key, record.Error,
			)
		}
	}

	// for each target there is an associated nar file
	expectedRecordCount := targetCount * 2
	as.Equal(
		expectedRecordCount, recordCount,
		"expected %d records in output file, got %d", expectedRecordCount, recordCount,
	)
}
