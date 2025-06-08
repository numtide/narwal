package config

import (
	"context"
	"fmt"

	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/spf13/pflag"
)

type S3 struct {
	// S3 bucket name (required)
	Bucket string `mapstructure:"bucket"`

	// AWS region (for AWS S3) - use either Region OR Endpoint, not both
	Region string `mapstructure:"region"`

	// S3 endpoint URL (for custom S3-compatible services like MinIO)
	Endpoint string `mapstructure:"endpoint"`

	// SSL settings
	UseSSL bool `mapstructure:"use_ssl"`

	// S3 credentials configuration
	Credentials CredentialsConfig `mapstructure:"credentials"`
}

type CredentialsConfig struct {
	// Direct AWS credentials
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	SessionToken    string `mapstructure:"session_token"`

	// AWS credentials file
	File string `mapstructure:"file"`

	// AWS CLI profile (alternative to direct credentials)
	Profile string `mapstructure:"profile"`
}

func (s *S3) Connect(ctx context.Context) (*awssdk.BucketClient, error) {
	// Create AWS credentials
	creds, err := awssdk.NewCredentials(ctx, awssdk.CredentialsConfig{
		AccessKeyID:     s.Credentials.AccessKeyID,
		SecretAccessKey: s.Credentials.SecretAccessKey,
		SessionToken:    s.Credentials.SessionToken,
		File:            s.Credentials.File,
		Profile:         s.Credentials.Profile,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS credentials: %w", err)
	}

	// Create bucket-bound S3 client
	client, err := awssdk.NewBucketClient(ctx, awssdk.BucketConfig{
		Bucket:   s.Bucket,
		Region:   s.Region,
		Endpoint: s.Endpoint,
		UseSSL:   s.UseSSL,
	}, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	return client, nil
}

func (s *S3) Validate() error {
	// Validate bucket name
	if s.Bucket == "" {
		return fmt.Errorf("%w: s3 bucket name is required", ErrInvalidConfig)
	}

	// Validate endpoint configuration: cannot specify both Region and Endpoint
	if s.Region != "" && s.Endpoint != "" {
		return fmt.Errorf("%w: cannot specify both Region and Endpoint - "+
			"use Region for AWS S3 or Endpoint for custom S3-compatible services", ErrInvalidConfig)
	}

	// Note: It's valid to specify neither Region nor Endpoint
	// - If Endpoint is specified: use custom S3-compatible service
	// - If Region is specified: use AWS S3 with that region
	// - If neither: use AWS S3 with auto-detected region

	// Validate credentials
	return s.Credentials.Validate()
}

func (c *CredentialsConfig) Validate() error {
	// Validate credentials: either direct keys, profile, or fallback to AWS CLI default
	// Allow fallback to AWS CLI default credentials when no explicit credentials are provided
	// This will be validated when NewCredentials is called
	return nil
}

func setS3Flags(fs *pflag.FlagSet) {
	fs.String("s3.bucket", "", "S3 Bucket Name")
	fs.String("s3.region", "", "AWS region (for AWS S3)")
	fs.String("s3.endpoint", "", "S3 Endpoint URL (for custom S3-compatible services)")
	fs.Bool("s3.use_ssl", false, "Use SSL when connecting to S3")
	fs.String("s3.credentials.access_key_id", "", "S3 Access Key ID")
	fs.String("s3.credentials.secret_access_key", "", "S3 Secret Access Key")
	fs.String("s3.credentials.session_token", "", "S3 Session Token")
	fs.String("s3.credentials.file", "", "AWS credentials file path")
	fs.String("s3.credentials.profile", "", "AWS CLI profile name")
}
