package gc_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/numtide/narwal/cmd"
	"github.com/numtide/narwal/pkg/gc"
	"github.com/numtide/narwal/pkg/gc/hydratest"
	"github.com/numtide/narwal/pkg/queries"
	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/require"
)

func TestSimpleGCStrategy(t *testing.T) {
	t.Parallel()

	// create a test bucket
	rustfs := getRustfsServer(t.Context())

	awsCfg, s3Cfg, bucketClient := rustfs.NewBucket(t)
	defer rustfs.CleanupBucket(t)

	// create a new db with the hydra schema
	pgServer := getPostgresServer(t.Context())

	dbURL := pgServer.NewHydraDB(t)
	defer pgServer.CleanupDB(t)

	// Create a connection pool
	pool, err := pgx.Connect(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		closeErr := pool.Close(t.Context())
		if closeErr != nil {
			t.Fatalf("failed to close database connection: %v", closeErr)
		}
	}()

	qry := queries.New(pool)

	// Generate test data in database and upload narinfo/nar files to S3
	gen := hydratest.NewGenerator(t, qry, bucketClient)
	gen.Generate()

	// Generate GC targets parquet file containing half of the uploaded store paths
	_, gcTargetsPath := gen.GenerateGCTargets()
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
		"--postgres.url", dbURL,
		"--aws.endpoint", awsCfg.Endpoint,
		fmt.Sprintf("--aws.use_ssl=%t", awsCfg.UseSSL),
		"--aws.credentials.access_key_id", awsCfg.Credentials.AccessKeyID,
		"--aws.credentials.secret_access_key", awsCfg.Credentials.SecretAccessKey,
		"--s3.bucket", s3Cfg.Bucket,
	})

	err = rootCmd.ExecuteContext(t.Context())
	as.NoError(err)

	// Confirm the output file exists
	as.FileExists(outputFile)

	// Validate the contents of the output file
	of, err := os.Open(outputFile) //nolint:gosec // outputFile from test temp dir
	as.NoError(err)

	schema := parquet.SchemaOf(new(gc.RemovalRecord))
	pr := parquet.NewReader(of, schema)

	var (
		readErr error
		record  gc.RemovalRecord
	)

	for {
		readErr = pr.Read(&record)
		if errors.Is(readErr, io.EOF) {
			// no more records
			break
		} else if err != nil {
			t.Fatalf("failed to read from output file: %v", err)
		}

		if record.Error != "" {
			t.Fatalf(
				"found error in output file for store path %s and key %s: %s",
				record.StorePath, record.Key, record.Error,
			)
		}
	}
}
