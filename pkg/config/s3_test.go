package config_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestS3_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s3   config.S3
		err  string
	}{
		{
			name: "valid with bucket only",
			s3: config.S3{
				Bucket: "my-bucket",
			},
		},
		{
			name: "valid with bucket and region",
			s3: config.S3{
				Bucket: "my-bucket",
				Region: "us-east-1",
			},
		},
		{
			name: "valid with bucket and endpoint",
			s3: config.S3{
				Bucket:   "my-bucket",
				Endpoint: "http://localhost:9000",
				UseSSL:   false,
			},
		},
		{
			name: "valid with credentials",
			s3: config.S3{
				Bucket: "my-bucket",
				Credentials: config.CredentialsConfig{
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				},
			},
		},
		{
			name: "valid with profile",
			s3: config.S3{
				Bucket: "my-bucket",
				Credentials: config.CredentialsConfig{
					Profile: "production",
				},
			},
		},
		{
			name: "empty bucket",
			s3:   config.S3{},
			err:  "s3 bucket name is required",
		},
		{
			name: "bucket with both region and endpoint",
			s3: config.S3{
				Bucket:   "my-bucket",
				Region:   "us-east-1",
				Endpoint: "http://localhost:9000",
			},
			err: "cannot specify both Region and Endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.s3.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestS3_ValidateCredentialsConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		creds config.CredentialsConfig
		err   string
	}{
		{
			name:  "empty credentials (fallback to AWS CLI)",
			creds: config.CredentialsConfig{},
		},
		{
			name: "direct credentials",
			creds: config.CredentialsConfig{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "secret",
			},
		},
		{
			name: "with session token",
			creds: config.CredentialsConfig{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "secret",
				SessionToken:    "token",
			},
		},
		{
			name: "profile only",
			creds: config.CredentialsConfig{
				Profile: "production",
			},
		},
		{
			name: "credentials file",
			creds: config.CredentialsConfig{
				File: "~/.aws/credentials",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.creds.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestS3_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetS3Flags(fs)

	// Verify all flags exist
	flags := []string{
		"s3.bucket",
		"s3.region",
		"s3.endpoint",
		"s3.use_ssl",
		"s3.credentials.access_key_id",
		"s3.credentials.secret_access_key",
		"s3.credentials.session_token",
		"s3.credentials.file",
		"s3.credentials.profile",
	}

	for _, flag := range flags {
		require.NotNil(t, fs.Lookup(flag), "flag %s should exist", flag)
	}
}

func TestS3_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()
		v := viper.New()
		v.Set("s3.bucket", "test-bucket")
		v.Set("s3.region", "us-west-2")
		v.Set("s3.use_ssl", true)
		v.Set("s3.credentials.access_key_id", "AKIATEST")
		v.Set("s3.credentials.secret_access_key", "secret123")

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.S3)
		require.Equal(t, "test-bucket", cfg.S3.Bucket)
		require.Equal(t, "us-west-2", cfg.S3.Region)
		require.True(t, cfg.S3.UseSSL)
		require.Equal(t, "AKIATEST", cfg.S3.Credentials.AccessKeyID)
		require.Equal(t, "secret123", cfg.S3.Credentials.SecretAccessKey)
	})

	t.Run("from flags", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetS3Flags(fs)

		require.NoError(t, fs.Parse([]string{
			"--s3.bucket=flag-bucket",
			"--s3.endpoint=http://localhost:9000",
		}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.S3)
		require.Equal(t, "flag-bucket", cfg.S3.Bucket)
		require.Equal(t, "http://localhost:9000", cfg.S3.Endpoint)
	})

	t.Run("flag overrides default", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetS3Flags(fs)

		require.NoError(t, fs.Parse([]string{"--s3.bucket=flag-bucket"}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.S3)
		require.Equal(t, "flag-bucket", cfg.S3.Bucket)
	})
}

func TestS3_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_S3_BUCKET", "env-bucket")
	t.Setenv("NARWAL_S3_REGION", "ap-southeast-1")
	t.Setenv("NARWAL_S3_USE_SSL", "true")
	t.Setenv("NARWAL_S3_CREDENTIALS_ACCESS_KEY_ID", "ENV_KEY")

	var cfg config.Config
	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.S3)
	require.Equal(t, "env-bucket", cfg.S3.Bucket)
	require.Equal(t, "ap-southeast-1", cfg.S3.Region)
	require.True(t, cfg.S3.UseSSL)
	require.Equal(t, "ENV_KEY", cfg.S3.Credentials.AccessKeyID)
}
