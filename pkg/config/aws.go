package config

import (
	"fmt"

	"github.com/spf13/pflag"
)

type AWS struct {
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

func (a *AWS) Validate() error {
	// Validate endpoint configuration: cannot specify both Region and Endpoint
	if a.Region != "" && a.Endpoint != "" {
		return fmt.Errorf("%w: cannot specify both Region and Endpoint - "+
			"use Region for AWS S3 or Endpoint for custom S3-compatible services", ErrInvalidConfig)
	}

	// Note: It's valid to specify neither Region nor Endpoint
	// - If Endpoint is specified: use custom S3-compatible service
	// - If Region is specified: use AWS S3 with that region
	// - If neither: use AWS S3 with auto-detected region

	// Validate credentials
	return a.Credentials.Validate()
}

func (c *CredentialsConfig) Validate() error {
	// Validate credentials: either direct keys, profile, or fallback to AWS CLI default
	// Allow fallback to AWS CLI default credentials when no explicit credentials are provided
	// This will be validated when NewCredentials is called
	return nil
}

func SetAWSFlags(fs *pflag.FlagSet) {
	fs.String("aws.region", "", "AWS region (for AWS S3)")
	fs.String("aws.endpoint", "", "S3 Endpoint URL (for custom S3-compatible services)")
	fs.Bool("aws.use_ssl", false, "Use SSL when connecting to S3")
	fs.String("aws.credentials.access_key_id", "", "S3 Access Key ID")
	fs.String("aws.credentials.secret_access_key", "", "S3 Secret Access Key")
	fs.String("aws.credentials.session_token", "", "S3 Session Token")
	fs.String("aws.credentials.file", "", "AWS credentials file path")
	fs.String("aws.credentials.profile", "", "AWS CLI profile name")
}
