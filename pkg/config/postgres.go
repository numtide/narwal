package config

import (
	"fmt"

	"github.com/spf13/pflag"
)

type Postgres struct {
	URL string `mapstructure:"url"`
}

func (p *Postgres) Validate() error {
	if p.URL == "" {
		return fmt.Errorf("%w: postgres url is required", ErrInvalidConfig)
	}

	return nil
}

func setPostgresFlags(fs *pflag.FlagSet) {
	fs.String("postgres.url", "", "Postgres URL")
}
