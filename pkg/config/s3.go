package config

import (
	"fmt"

	"github.com/spf13/pflag"
)

type S3 struct {
	Endpoint   string `mapstructure:"endpoint"`
	AccessKey  string `mapstructure:"access_key"`
	SecretKey  string `mapstructure:"secret_key"`
	BucketName string `mapstructure:"bucket_name"`
	SSLEnabled bool   `mapstructure:"ssl_enabled"`
}

func (s *S3) Validate() error {
	if s.Endpoint == "" {
		return fmt.Errorf("%w: s3 endpoint is required", ErrInvalidConfig)
	}

	if s.AccessKey == "" {
		return fmt.Errorf("%w: s3 access key is required", ErrInvalidConfig)
	}

	if s.SecretKey == "" {
		return fmt.Errorf("%w: s3 secret key is required", ErrInvalidConfig)
	}

	if s.BucketName == "" {
		return fmt.Errorf("%w: s3 bucket name is required", ErrInvalidConfig)
	}

	return nil
}

func setS3Flags(fs *pflag.FlagSet) {
	fs.String("s3.endpoint", "", "S3 Endpoint URL")
	fs.String("s3.access_key", "", "S3 Access Key")
	fs.String("s3.secret_key", "", "S3 Secret Key")
	fs.String("s3.bucket_name", "", "S3 Bucket Name")
	fs.Bool("s3.ssl_enabled", false, "Use SSL when connecting to S3")
}
