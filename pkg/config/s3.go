package config

import "fmt"

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
