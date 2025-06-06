package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/spf13/pflag"
)

type S3 struct {
	Endpoint      string `mapstructure:"endpoint"`
	AccessKey     string `mapstructure:"access_key"`
	AccessKeyFile string `mapstructure:"access_key_file"`
	SecretKey     string `mapstructure:"secret_key"`
	SecretKeyFile string `mapstructure:"secret_key_file"`
	BucketName    string `mapstructure:"bucket_name"`
	SSLEnabled    bool   `mapstructure:"ssl_enabled"`
}

func (s *S3) Connect() (*minio.Client, error) {
	// connect to s3
	s3, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.SSLEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to s3: %w", err)
	}

	return s3, nil
}

func (s *S3) Validate() error {
	if s.Endpoint == "" {
		return fmt.Errorf("%w: s3 endpoint is required", ErrInvalidConfig)
	}

	// prefer key files
	if s.AccessKeyFile != "" {
		buf, err := os.ReadFile(s.AccessKeyFile)
		if err != nil {
			return fmt.Errorf("%w: failed to read s3 access key file", ErrInvalidConfig)
		}

		s.AccessKey = strings.TrimSpace(string(buf))
	} else if s.AccessKey == "" {
		return fmt.Errorf("%w: s3 access key is required", ErrInvalidConfig)
	}

	if s.SecretKeyFile != "" {
		buf, err := os.ReadFile(s.SecretKeyFile)
		if err != nil {
			return fmt.Errorf("%w: failed to read s3 secret key file", ErrInvalidConfig)
		}

		s.SecretKey = strings.TrimSpace(string(buf))
	} else if s.SecretKey == "" {
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
