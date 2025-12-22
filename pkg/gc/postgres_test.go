package gc_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5"
	"github.com/numtide/narwal/pkg/queries"
)

//nolint:gochecknoglobals
var (
	testPostgresOnce   sync.Once
	testPostgresServer *postgresServer

	errPostgresStart error
)

const (
	debugPostgres = false
)

type postgresServer struct {
	cmd         *exec.Cmd
	tempDir     string
	mu          sync.RWMutex
	activeTests map[string]struct{}
}

func sanitizeDBName(testName string) string {
	// Replace "/" and other special chars with "_"
	result := strings.ReplaceAll(testName, "/", "_")
	result = strings.ReplaceAll(result, " ", "_")
	result = strings.ReplaceAll(result, "-", "_")

	// Ensure it starts with a letter (prepend "t_" if it starts with a number)
	if len(result) > 0 && result[0] >= '0' && result[0] <= '9' {
		result = "t_" + result
	}

	// Truncate to 63 chars (PostgreSQL limit)
	if len(result) > 63 {
		result = result[:63]
	}

	// Convert to lowercase (PostgreSQL convention)
	result = strings.ToLower(result)

	return result
}

func (s *postgresServer) NewDB(tb testing.TB) string {
	tb.Helper()

	testName := tb.Name()
	dbName := sanitizeDBName(testName)

	// Acquire the write lock
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if this test already has an active database
	if _, exists := s.activeTests[dbName]; exists {
		tb.Fatalf("test %q already has an active database", testName)
	}

	// Create the database (inside mutex to ensure one-shot creation)

	//nolint:gosec
	command := exec.CommandContext(tb.Context(), "createdb", "-h", s.tempDir, "-U", "postgres", dbName)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err := command.Run()
	if err != nil {
		tb.Fatalf("failed to create database %s: %v", dbName, err)
	}

	// Track the test as active
	s.activeTests[dbName] = struct{}{}

	return fmt.Sprintf("postgres://?dbname=%s&user=postgres&host=%s", dbName, s.tempDir)
}

func (s *postgresServer) NewHydraDB(tb testing.TB) string {
	tb.Helper()

	dbUrl := s.NewDB(tb)

	hydraSrc := os.Getenv("HYDRA_SRC")
	if hydraSrc == "" {
		tb.Fatal("HYDRA_SRC environment variable not set")
	}

	schemaPath := filepath.Join(hydraSrc, "src", "sql", "hydra.sql")

	schemaSQL, err := os.ReadFile(schemaPath) //nolint:gosec // path from trusted env var
	if err != nil {
		log.Fatalf("failed to read hydra schema from %s: %v", schemaPath, err)
	}

	ctx, cancel := context.WithTimeout(tb.Context(), 10*time.Second)

	conn, err := pgx.Connect(ctx, dbUrl)
	if err != nil {
		cancel()
		log.Fatalf("failed to connect to database: %v", err)
	}

	cancel()

	ctx, cancel = context.WithTimeout(tb.Context(), 10*time.Second)

	_, err = conn.Exec(ctx, string(schemaSQL))
	if err != nil {
		cancel()

		_ = conn.Close(tb.Context())

		log.Fatalf("failed to execute hydra schema: %v", err)
	}

	cancel()

	// Log all tables in the database
	if debugPostgres {
		queries := queries.New(conn)

		tableNames, err := queries.ListTables(ctx)
		if err != nil {
			_ = conn.Close(tb.Context())

			log.Fatalf("failed to list tables: %v", err)
		}

		for _, tableName := range tableNames {
			tb.Logf("Hydra table: %s", tableName)
		}
	}

	if closeErr := conn.Close(tb.Context()); closeErr != nil {
		log.Fatalf("failed to close database connection: %v", closeErr)
	}

	return dbUrl
}

func (s *postgresServer) CleanupDB(tb testing.TB) {
	tb.Helper()

	dbName := sanitizeDBName(tb.Name())

	s.mu.Lock()
	delete(s.activeTests, dbName)
	s.mu.Unlock()
}

func (s *postgresServer) Cleanup() {
	s.mu.RLock()
	remaining := len(s.activeTests)
	s.mu.RUnlock()

	// Only terminate when all tests have cleaned up
	if remaining == 0 {
		defer func() {
			if err := os.RemoveAll(s.tempDir); err != nil {
				log.Warn("Failed to remove postgres temp directory", "error", err)
			}
		}()

		terminateProcess(s.cmd)
	}
}

func getPostgresServer(ctx context.Context) *postgresServer {
	testPostgresOnce.Do(func() {
		testPostgresServer, errPostgresStart = startPostgresServer(ctx)
	})

	if errPostgresStart != nil {
		log.Fatalf("failed to start postgres server: %s", errPostgresStart)
	}

	return testPostgresServer
}

func startPostgresServer(ctx context.Context) (*postgresServer, error) {
	// unload environment variables from the devenv
	_ = os.Unsetenv("DATABASE_URL")
	_ = os.Unsetenv("PGDATABASE")
	_ = os.Unsetenv("PGUSER")
	_ = os.Unsetenv("PGHOST")

	tempDir, err := os.MkdirTemp("", "postgres")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	defer func() {
		if err != nil {
			if removeErr := os.RemoveAll(tempDir); removeErr != nil {
				log.Warn("Failed to remove temp dir", "error", removeErr)
			}
		}
	}()

	// initialize the database
	dbPath := filepath.Join(tempDir, "data")
	initdb := exec.CommandContext(ctx, "initdb", "-D", dbPath, "-U", "postgres") //nolint:gosec // test helper
	initdb.Stdout = os.Stdout
	initdb.Stderr = os.Stderr

	if err = initdb.Run(); err != nil {
		return nil, fmt.Errorf("failed to run initdb: %w", err)
	}

	args := []string{
		"-D", dbPath,
		"-k", tempDir,
		"-c", "listen_addresses=",
		// Performance settings for tests (unsafe for production, fast for tests)
		"-c", "fsync=off",
		"-c", "synchronous_commit=off",
		"-c", "full_page_writes=off",
		"-c", "max_wal_size=2GB",
		"-c", "checkpoint_timeout=1h",
		"-c", "wal_level=minimal",
		"-c", "max_wal_senders=0",
	}

	if debugPostgres {
		args = append(args, "-c", "log_statement=all", "-c", "log_min_duration_statement=0")
	}

	postgresProc := exec.CommandContext(ctx, "postgres", args...) //nolint:gosec // test helper
	postgresProc.Stdout = os.Stdout
	postgresProc.Stderr = os.Stderr
	postgresProc.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err = postgresProc.Start(); err != nil {
		return nil, fmt.Errorf("failed to start postgres: %w", err)
	}

	server := &postgresServer{
		cmd:         postgresProc,
		tempDir:     tempDir,
		activeTests: make(map[string]struct{}),
	}

	defer func() {
		if err != nil {
			// Force cleanup on startup failure (no tests registered yet)
			defer func() {
				if removeErr := os.RemoveAll(server.tempDir); removeErr != nil {
					log.Warn("Failed to remove postgres temp directory", "error", removeErr)
				}
			}()

			terminateProcess(server.cmd)
		}
	}()

	for range 30 {
		// Check if context has been cancelled/timed out
		if ctx.Err() != nil {
			return nil, fmt.Errorf("timeout waiting for postgres to start: %w", ctx.Err())
		}

		//nolint:gosec // test helper
		waitForPostgres := exec.CommandContext(ctx, "pg_isready", "-h", tempDir, "-U", "postgres")
		waitForPostgres.Stdout = os.Stdout
		waitForPostgres.Stderr = os.Stderr

		err = waitForPostgres.Run()
		if err == nil {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to wait for postgres: %w", err)
	}

	return server, nil
}

func terminateProcess(cmd *exec.Cmd) {
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		log.Error("failed to get pgid", "error", err)

		return
	}

	time.AfterFunc(10*time.Second, func() {
		err = syscall.Kill(pgid, syscall.SIGKILL)
		if err != nil {
			log.Error("failed to kill %s: %s", cmd, err)

			return
		}

		log.Infof("killed %s", cmd.String())
	})

	err = syscall.Kill(pgid, syscall.SIGTERM)
	if err != nil {
		log.Errorf("failed to kill '%s': %s", cmd, err)
	}

	err = cmd.Wait()
	if err != nil {
		log.Error("failed to wait for '%s': %s", cmd, err)

		return
	}
}
