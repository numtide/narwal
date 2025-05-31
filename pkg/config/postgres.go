package config

import "fmt"

type Postgres struct {
	URL string `mapstructure:"url"`
}

func (p *Postgres) Validate() error {
	if p.URL == "" {
		return fmt.Errorf("%w: postgres url is required", ErrInvalidConfig)
	}

	return nil
}
