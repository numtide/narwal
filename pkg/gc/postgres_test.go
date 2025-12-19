// Adapted from https://github.com/Mic92/niks3
package gc_test

import (
	"context"
	"fmt"
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
	cmd     *exec.Cmd
	tempDir string
	dbCount atomic.Int32
}

func (s *postgresServer) NewDB(tb testing.TB) string {
	tb.Helper()

	// Generate a unique name for the database
	dbName := "db_" + strconv.Itoa(int(s.dbCount.Add(1)))

	// Create the database
	//nolint:gosec
	command := exec.CommandContext(tb.Context(), "createdb", "-h", testPostgresServer.tempDir, "-U", "postgres", dbName)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err := command.Run()
	if err != nil {
		tb.Fatalf("failed to create database %s: %v", dbName, err)
	}

	return fmt.Sprintf("postgres://?dbname=%s&user=postgres&host=%s", dbName, testPostgresServer.tempDir)
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
		tb.Fatalf("failed to read hydra schema from %s: %v", schemaPath, err)
	}

	ctx, cancel := context.WithTimeout(tb.Context(), 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbUrl)
	if err != nil {
		tb.Fatalf("failed to connect to database: %v", err)
	}

	defer func() {
		closeErr := conn.Close(ctx)
		if closeErr != nil {
			tb.Fatalf("failed to close database connection: %v", closeErr)
		}
	}()

	ctx, cancel = context.WithTimeout(tb.Context(), 10*time.Second)
	defer cancel()

	_, err = conn.Exec(ctx, string(schemaSQL))
	if err != nil {
		tb.Fatalf("failed to execute hydra schema: %v", err)
	}

	// Log all tables in the database
	if debugPostgres {
		queries := queries.New(conn)

		tableNames, err := queries.ListTables(ctx)
		if err != nil {
			tb.Fatalf("failed to list tables: %v", err)
		}

		for _, tableName := range tableNames {
			tb.Logf("Hydra table: %s", tableName)
		}
	}

	return dbUrl
}

func (s *postgresServer) Cleanup() {
	defer func() {
		if err := os.RemoveAll(s.tempDir); err != nil {
			log.Warn("Failed to remove postgres temp directory", "error", err)
		}
	}()

	terminateProcess(s.cmd)
}

func getPostgresServer(tb testing.TB) *postgresServer {
	tb.Helper()

	testPostgresOnce.Do(func() {
		testPostgresServer, errPostgresStart = startPostgresServer(tb)
	})

	if errPostgresStart != nil {
		tb.Fatalf("failed to start postgres server: %s", errPostgresStart)
	}

	return testPostgresServer
}

func startPostgresServer(tb testing.TB) (*postgresServer, error) {
	tb.Helper()

	// unload environment variables from the devenv
	_ = os.Unsetenv("DATABASE_URL")
	_ = os.Unsetenv("PGDATABASE")
	_ = os.Unsetenv("PGUSER")
	_ = os.Unsetenv("PGHOST")

	tempDir, err := os.MkdirTemp("", "postgres") //nolint:usetesting // need manual cleanup for shared server
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
	initdb := exec.CommandContext(tb.Context(), "initdb", "-D", dbPath, "-U", "postgres") //nolint:gosec // test helper
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

	postgresProc := exec.CommandContext(tb.Context(), "postgres", args...) //nolint:gosec // test helper
	postgresProc.Stdout = os.Stdout
	postgresProc.Stderr = os.Stderr
	postgresProc.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err = postgresProc.Start(); err != nil {
		return nil, fmt.Errorf("failed to start postgres: %w", err)
	}

	server := &postgresServer{
		cmd:     postgresProc,
		tempDir: tempDir,
	}

	defer func() {
		if err != nil {
			server.Cleanup()
		}
	}()

	for range 30 {
		// Check if context has been cancelled/timed out
		if tb.Context().Err() != nil {
			return nil, fmt.Errorf("timeout waiting for postgres to start: %w", tb.Context().Err())
		}

		//nolint:gosec // test helper
		waitForPostgres := exec.CommandContext(tb.Context(), "pg_isready", "-h", tempDir, "-U", "postgres")
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
