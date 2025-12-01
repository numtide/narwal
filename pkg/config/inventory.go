package config

import (
	"strings"

	"github.com/spf13/pflag"
)

type Inventory struct {
	BucketPrefix          string `mapstructure:"bucket_prefix"`
	ForceNarInfoDownload  bool   `mapstructure:"force_nar_info_download"`
	DeleteInvalidNarInfos bool   `mapstructure:"delete_invalid_nar_infos"`
}

func (i *Inventory) Validate() error {
	// Ensure prefix ends with a slash
	if i.BucketPrefix != "" && !strings.HasSuffix(i.BucketPrefix, "/") {
		i.BucketPrefix += "/"
	}

	return nil
}

func SetInventoryFlags(fs *pflag.FlagSet) {
	fs.String("inventory.bucket_prefix", "nix-cache/nix-cache-inventory",
		"Prefix path within the S3 bucket (e.g. 'data/' or 'nix-cache/inventory/')")

	fs.Bool("inventory.force_nar_info_download", false, ""+
		"Ignore whether a manifest file has been marked as downloaded and check each entry instead",
	)

	fs.Bool("inventory.delete_invalid_nar_infos", false,
		"Delete any nar infos with invalid checksums when compared with the manifest",
	)
}
