package gc_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/numtide/narwal/pkg/gc/hydratest"
	"github.com/numtide/narwal/pkg/queries"
)

func TestDB(t *testing.T) {
	// TODO fix concurrent use of test postgres and rustfs servers

	// get a rustfs server going
	rustfs := getRustfsServer(t)
	defer rustfs.Cleanup(t)

	// create a test bucket
	rustfs.NewBucket(t)

	// get a postgres server going
	pgServer := getPostgresServer(t)
	defer pgServer.Cleanup(t)

	// create a new db and add test data to the db
	dbURL := pgServer.NewHydraDB(t)

	pool, err := pgx.Connect(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}

	qry := queries.New(pool)

	hydratest.Generate(t, qry)

	defer func() {
		closeErr := pool.Close(t.Context())
		if closeErr != nil {
			t.Fatalf("failed to close database connection: %v", closeErr)
		}
	}()
}
