package config

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/db"
	"github.com/spf13/pflag"
)

type Postgres struct {
	URL string `mapstructure:"url"`
}

func (p *Postgres) Connect(ctx context.Context, migrate bool) (*pgxpool.Pool, error) {
	pg, err := db.Connect(ctx, p.URL, migrate)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	return pg, nil
}

func (p *Postgres) Validate() error {
	if p.URL == "" {
		return fmt.Errorf("%w: postgres url is required", ErrInvalidConfig)
	}

	return nil
}

func SetPostgresFlags(fs *pflag.FlagSet) {
	fs.String("postgres.url", "", "Postgres URL")
}
