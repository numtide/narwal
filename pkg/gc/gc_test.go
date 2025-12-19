package gc_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/numtide/narwal/pkg/gc/hydratest"
	"github.com/numtide/narwal/pkg/queries"
)

//nolint:paralleltest // uses shared postgres server
func TestDB(t *testing.T) {
	pgServer := getPostgresServer(t)
	defer pgServer.Cleanup()

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
