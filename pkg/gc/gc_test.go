package gc_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/numtide/narwal/pkg/gc/hydratest"
	"github.com/numtide/narwal/pkg/queries"
)

func TestDB(t *testing.T) {
	t.Parallel()

	// get a rustfs server going
	rustfs := getRustfsServer(t)
	defer rustfs.Cleanup(t)

	// create a test bucket
	bucketName, _ := rustfs.NewBucket(t)
	bucketClient := rustfs.BucketClient(t, bucketName)

	// get a postgres server going
	pgServer := getPostgresServer(t)
	defer pgServer.Cleanup(t)

	// create a new db and add test data to the db
	dbURL := pgServer.NewHydraDB(t)

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
	hydratest.Generate(t, qry, bucketClient)
}
