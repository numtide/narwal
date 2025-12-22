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
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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
	bucketCount atomic.Int32
}

func (s *rustfsServer) Client(tb testing.TB) *minio.Client {
	tb.Helper()

	endpoint := fmt.Sprintf("localhost:%d", s.port)
	// minio-go client works with any S3-compatible storage including RustFS
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4("rustfsadmin", s.secret, ""),
		Secure: false,
	})
	if err != nil {
		tb.Fatalf("failed to create minio client: %v", err)
	}

	return minioClient
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

	// Generate a unique name for the bucket
	bucketName := "test-bucket-" + strconv.Itoa(int(s.bucketCount.Add(1)))

	if err := client.MakeBucket(tb.Context(), bucketName, minio.MakeBucketOptions{}); err != nil {
		tb.Fatalf("failed to create bucket: %v", err)
	}

	return bucketName, client
}

func (s *rustfsServer) Cleanup() {
	defer func() {
		if err := os.RemoveAll(s.tempDir); err != nil {
			log.Warn("Failed to remove rustfs temp directory", "error", err)
		}
	}()

	terminateProcess(s.cmd)
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
		cmd:     rustfsProc,
		tempDir: tempDir,
		secret:  secret,
		port:    port,
	}

	defer func() {
		if err != nil {
			server.Cleanup()
		}
	}()

	return server
}
