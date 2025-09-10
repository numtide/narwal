package config

import (
	"strings"

	"github.com/spf13/pflag"
)

type Inventory struct {
	Prefix string `mapstructure:"prefix"`
}

func (i *Inventory) Validate() error {
	// Ensure prefix ends with a slash
	if i.Prefix != "" && !strings.HasSuffix(i.Prefix, "/") {
		i.Prefix += "/"
	}

	return nil
}

func SetInventoryFlags(fs *pflag.FlagSet) {
	fs.String("inventory.prefix", "nix-cache/nix-cache-inventory",
		"Prefix path within the S3 bucket (e.g. 'data/' or 'nix-cache/inventory/')")
}
