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

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
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
	mu          sync.RWMutex
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

func (s *rustfsServer) NewBucket(tb testing.TB) (*config.AWS, *config.S3, *awssdk.BucketClient) {
	tb.Helper()

	// Derive the bucket name from the test name
	testName := tb.Name()
	bucketName := sanitizeBucketName(testName)

	// Generate aws and s3 config
	awsCfg := &config.AWS{
		Region:   "us-east-1",
		Endpoint: fmt.Sprintf("localhost:%d", s.port),
		UseSSL:   false,
		Credentials: config.CredentialsConfig{
			AccessKeyID:     "rustfsadmin",
			SecretAccessKey: s.secret,
		},
	}

	s3Cfg := &config.S3{
		Bucket: bucketName,
	}

	// Acquire the write lock
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if this test already has an active bucket
	if _, exists := s.activeTests[bucketName]; exists {
		tb.Fatalf("test %q already has an active bucket", testName)
	}

	// Create a bucket client
	client, err := awssdk.NewBucketClientFromConfig(tb.Context(), awsCfg, s3Cfg)
	if err != nil {
		tb.Fatalf("failed to create bucket client: %v", err)
	}

	// Create the bucket (inside mutex to ensure one-shot creation)
	if err = client.CreateBucket(tb.Context()); err != nil {
		tb.Fatalf("failed to create bucket: %v", err)
	}

	// Track the test as active
	s.activeTests[bucketName] = struct{}{}

	return awsCfg, s3Cfg, client
}

func (s *rustfsServer) CleanupBucket(tb testing.TB) {
	tb.Helper()

	testName := tb.Name()
	bucketName := sanitizeBucketName(testName)

	s.mu.Lock()
	delete(s.activeTests, bucketName)
	s.mu.Unlock()
}

func (s *rustfsServer) Cleanup() {
	s.mu.RLock()
	remaining := len(s.activeTests)
	s.mu.RUnlock()

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

func getRustfsServer(ctx context.Context) *rustfsServer {
	testRustfsOnce.Do(func() {
		testRustfsServer = startRustfsServer(ctx)
	})

	return testRustfsServer
}

func startRustfsServer(ctx context.Context) *rustfsServer {
	tempDir, err := os.MkdirTemp("", "rustfs")
	if err != nil {
		log.Fatalf("failed to create temp dir: %v", err)
	}

	port, err := randPort(ctx)
	if err != nil {
		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			log.Infof("Failed to remove temp directory during startup cleanup: %s", removeErr)
		}

		log.Fatalf("failed to find free port: %v", err)
	}

	// random hex string
	secret, err := randToken(20)
	if err != nil {
		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			log.Infof("Failed to remove temp directory during startup cleanup: %s", removeErr)
		}

		log.Fatalf("failed to generate access key: %v", err)
	}

	dataDir := filepath.Join(tempDir, "data")
	if err = os.MkdirAll(dataDir, 0o750); err != nil {
		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			log.Infof("Failed to remove temp directory during startup cleanup: %s", removeErr)
		}

		log.Fatalf("failed to create data dir: %v", err)
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
		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			log.Infof("Failed to remove temp directory during startup cleanup: %s", removeErr)
		}

		log.Fatalf("failed to start rustfs: %v", err)
	}

	// wait for server to start
	dialer := net.Dialer{}

	for range 200 {
		// Check if context has been cancelled/timed out
		if ctx.Err() != nil {
			terminateProcess(rustfsProc)

			if removeErr := os.RemoveAll(tempDir); removeErr != nil {
				log.Infof("Failed to remove temp directory during startup cleanup: %s", removeErr)
			}

			log.Fatalf("timeout waiting for rustfs server to start: %v", ctx.Err())
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
		terminateProcess(rustfsProc)

		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			log.Infof("Failed to remove temp directory during startup cleanup: %s", removeErr)
		}

		log.Fatalf("failed to wait for rustfs server: %v", err)
	}

	server := &rustfsServer{
		cmd:         rustfsProc,
		tempDir:     tempDir,
		secret:      secret,
		port:        port,
		activeTests: make(map[string]struct{}),
	}

	return server
}
