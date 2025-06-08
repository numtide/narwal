package config

import (
	"fmt"

	"github.com/spf13/pflag"
)

// Workarea represents the configuration for the local working directory.
type Workarea struct {
	Path string `mapstructure:"path"`
}

// Validate checks the workarea configuration to ensure required fields are set.
func (w *Workarea) Validate() error {
	if w.Path == "" {
		return fmt.Errorf("%w: workarea path is required", ErrInvalidConfig)
	}

	return nil
}

func setWorkareaFlags(fs *pflag.FlagSet) {
	fs.String("workarea.path", "./work", "Local directory for caching files and temporary data")
}
