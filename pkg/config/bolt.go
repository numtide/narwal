package config

import (
	"fmt"

	"github.com/spf13/pflag"
)

type Bolt struct {
	Path string `mapstructure:"path"`
}

func (b *Bolt) Validate() error {
	if b.Path == "" {
		return fmt.Errorf("%w: bolt db path is required", ErrInvalidConfig)
	}

	return nil
}

func SetBoltFlags(fs *pflag.FlagSet) {
	// todo smarter defaulting of the path, maybe follow XDG pattern like with treefmt
	fs.String("bolt.path", "./narwal.db", "Path to the BoltDB file.")
}
