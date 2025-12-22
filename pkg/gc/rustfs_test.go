// Adapted from https://github.com/Mic92/niks3
package gc_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	"github.com/minio/minio-go/v7"
	miniocreds "github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/numtide/narwal/pkg/awssdk"
)

//nolint:gochecknoglobals
var (
	testRustfsOnce   sync.Once
	testRustfsServer *rustfsServer
)

type rustfsServer struct {
	cmd         *exec.Cmd
	tempDir     string
	secret      string
	port        uint16
	mu          sync.Mutex
	activeTests map[string]struct{}
}

func sanitizeBucketName(testName string) string {
	// Replace "/" and other special chars with "-" (S3 doesn't allow underscores)
	result := strings.ReplaceAll(testName, "/", "-")
	result = strings.ReplaceAll(result, "_", "-")
	result = strings.ReplaceAll(result, " ", "-")

	// Convert to lowercase (S3 requirement)
	result = strings.ToLower(result)

	// Ensure it starts with a letter (prepend "t-" if it starts with a number)
	if len(result) > 0 && result[0] >= '0' && result[0] <= '9' {
		result = "t-" + result
	}

	// Truncate to 63 chars (S3 limit)
	if len(result) > 63 {
		result = result[:63]
	}

	// Remove trailing hyphens (S3 requirement)
	result = strings.TrimRight(result, "-")

	return result
}

func (s *rustfsServer) Client(tb testing.TB) *minio.Client {
	tb.Helper()

	endpoint := fmt.Sprintf("localhost:%d", s.port)
	// minio-go client works with any S3-compatible storage including RustFS
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  miniocreds.NewStaticV4("rustfsadmin", s.secret, ""),
		Secure: false,
	})
	if err != nil {
		tb.Fatalf("failed to create minio client: %v", err)
	}

	return minioClient
}

func (s *rustfsServer) BucketClient(tb testing.TB, bucketName string) *awssdk.BucketClient {
	tb.Helper()

	endpoint := fmt.Sprintf("http://localhost:%d", s.port)

	cfg, err := awsconfig.LoadDefaultConfig(tb.Context(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("rustfsadmin", s.secret, "")),
		awsconfig.WithBaseEndpoint(endpoint),
	)
	if err != nil {
		tb.Fatalf("failed to load AWS config: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	return awssdk.NewBucketClientFromSDK(s3Client, bucketName)
}

func randToken(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to read random bytes: %w", err)
	}

	return hex.EncodeToString(bytes), nil
}

func randPort(ctx context.Context) (uint16, error) {
	lc := net.ListenConfig{}

	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to listen: %w", err)
	}

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()

		return 0, errors.New("listener did not return *net.TCPAddr")
	}

	port := uint16(addr.Port) //nolint:gosec
	_ = ln.Close()

	return port, nil
}

func (s *rustfsServer) NewBucket(tb testing.TB) (string, *minio.Client) {
	tb.Helper()

	client := s.Client(tb)

	testName := tb.Name()
	bucketName := sanitizeBucketName(testName)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if this test already has an active bucket
	if _, exists := s.activeTests[bucketName]; exists {
		tb.Fatalf("test %q already has an active bucket", testName)
	}

	// Create the bucket (inside mutex to ensure one-shot creation)
	if err := client.MakeBucket(tb.Context(), bucketName, minio.MakeBucketOptions{}); err != nil {
		tb.Fatalf("failed to create bucket: %v", err)
	}

	// Track the test as active
	s.activeTests[bucketName] = struct{}{}

	return bucketName, client
}

func (s *rustfsServer) Cleanup(tb testing.TB) {
	tb.Helper()

	testName := tb.Name()
	bucketName := sanitizeBucketName(testName)

	s.mu.Lock()
	delete(s.activeTests, bucketName)
	remaining := len(s.activeTests)
	s.mu.Unlock()

	// Only terminate when all tests have cleaned up
	if remaining == 0 {
		defer func() {
			if err := os.RemoveAll(s.tempDir); err != nil {
				log.Warn("Failed to remove rustfs temp directory", "error", err)
			}
		}()

		terminateProcess(s.cmd)
	}
}

func getRustfsServer(tb testing.TB) *rustfsServer {
	tb.Helper()

	testRustfsOnce.Do(func() {
		testRustfsServer = startRustfsServer(tb)
	})

	return testRustfsServer
}

func startRustfsServer(tb testing.TB) *rustfsServer {
	tb.Helper()

	tempDir, err := os.MkdirTemp("", "rustfs") //nolint:usetesting // need manual cleanup for shared server
	if err != nil {
		tb.Fatalf("failed to create temp dir: %v", err)
	}

	defer func() {
		if err != nil {
			if removeErr := os.RemoveAll(tempDir); removeErr != nil {
				tb.Logf("Failed to remove temp directory during startup cleanup: %s", removeErr)
			}
		}
	}()

	ctx := tb.Context()

	port, err := randPort(ctx)
	if err != nil {
		tb.Fatalf("failed to find free port: %v", err)
	}

	// random hex string
	secret, err := randToken(20)
	if err != nil {
		tb.Fatalf("failed to generate access key: %v", err)
	}

	dataDir := filepath.Join(tempDir, "data")
	if err = os.MkdirAll(dataDir, 0o750); err != nil {
		tb.Fatalf("failed to create data dir: %v", err)
	}

	//nolint:gosec
	rustfsProc := exec.CommandContext(ctx, "rustfs",
		"--address", fmt.Sprintf("127.0.0.1:%d", port),
		"--access-key", "rustfsadmin",
		"--secret-key", secret,
		dataDir)
	rustfsProc.Stdout = os.Stdout
	rustfsProc.Stderr = os.Stderr
	rustfsProc.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	env := os.Environ()
	env = append(env, "AWS_ACCESS_KEY_ID=rustfsadmin")
	env = append(env, "AWS_SECRET_ACCESS_KEY="+secret)
	rustfsProc.Env = env

	if err = rustfsProc.Start(); err != nil {
		tb.Fatalf("failed to start rustfs: %v", err)
	}

	// wait for server to start
	dialer := net.Dialer{}

	for range 200 {
		// Check if context has been cancelled/timed out
		if ctx.Err() != nil {
			tb.Fatalf("timeout waiting for rustfs server to start: %v", ctx.Err())
		}

		var conn net.Conn

		conn, err = dialer.DialContext(ctx, "tcp", fmt.Sprintf("localhost:%d", port))
		if err == nil {
			_ = conn.Close()

			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		tb.Fatalf("failed to wait for rustfs server: %v", err)
	}

	server := &rustfsServer{
		cmd:         rustfsProc,
		tempDir:     tempDir,
		secret:      secret,
		port:        port,
		activeTests: make(map[string]struct{}),
	}

	defer func() {
		if err != nil {
			// Force cleanup on startup failure (no tests registered yet)
			defer func() {
				if removeErr := os.RemoveAll(server.tempDir); removeErr != nil {
					log.Warn("Failed to remove rustfs temp directory", "error", removeErr)
				}
			}()

			terminateProcess(server.cmd)
		}
	}()

	return server
}
