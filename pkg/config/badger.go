package config

import (
	"fmt"

	"github.com/spf13/pflag"
)

type Badger struct {
	Path string `mapstructure:"path"`
}

func (b *Badger) Validate() error {
	if b.Path == "" {
		return fmt.Errorf("%w: badger db path is required", ErrInvalidConfig)
	}

	return nil
}

func SetBadgerFlags(fs *pflag.FlagSet) {
	// todo smarter defaulting of the path, maybe follow XDG pattern like with treefmt
	fs.String("badger.path", "./narwal-db", "Path to the Badger db file.")
}
