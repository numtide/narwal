package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	log.Debug("connecting to database", "url", url)

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}

	// migrate the database
	log.Debug("migrating database")
	goose.SetBaseFS(embedMigrations)

	db := stdlib.OpenDBFromPool(pool)

	goose.SetLogger(log.StandardLog())

	if err = goose.SetDialect("postgres"); err != nil {
		return nil, fmt.Errorf("failed to set dialect: %w", err)
	} else if err = goose.Up(db, "migrations"); err != nil {
		return nil, fmt.Errorf("failed to migrate db: %w", err)
	}

	return pool, nil
}
