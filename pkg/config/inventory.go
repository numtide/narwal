package config

import (
	"strings"

	"github.com/spf13/pflag"
)

type Inventory struct {
	BucketPrefix string `mapstructure:"bucket-prefix"`
}

func (i *Inventory) Validate() error {
	// Ensure prefix ends with a slash
	if i.BucketPrefix != "" && !strings.HasSuffix(i.BucketPrefix, "/") {
		i.BucketPrefix += "/"
	}

	return nil
}

func SetInventoryFlags(fs *pflag.FlagSet) {
	fs.String("inventory.bucket-prefix", "nix-cache/nix-cache-inventory",
		"Prefix path within the S3 bucket (e.g. 'data/' or 'nix-cache/inventory/')")
}
