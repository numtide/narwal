package config

import (
	"fmt"

	"github.com/spf13/pflag"
)

type S3 struct {
	// S3 bucket name (required)
	Bucket string `mapstructure:"bucket"`
}

func (s *S3) Validate() error {
	// Validate bucket name
	if s.Bucket == "" {
		return fmt.Errorf("%w: s3 bucket name is required", ErrInvalidConfig)
	}

	return nil
}

func SetS3Flags(fs *pflag.FlagSet) {
	fs.String("s3.bucket", "", "S3 Bucket Name")
}
