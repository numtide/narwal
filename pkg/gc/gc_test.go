package gc_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

//nolint:paralleltest // uses shared postgres server
func TestDB(t *testing.T) {
	pgServer := getPostgresServer(t)
	defer pgServer.Cleanup()

	dbURL := pgServer.NewHydraDB(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	pool, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		closeErr := pool.Close(ctx)
		if closeErr != nil {
			t.Fatalf("failed to close database connection: %v", closeErr)
		}
	}()
}
