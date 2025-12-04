package config_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestAWS_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		aws  config.AWS
		err  string
	}{
		{
			name: "valid empty (defaults)",
			aws:  config.AWS{},
		},
		{
			name: "valid with region",
			aws: config.AWS{
				Region: "us-east-1",
			},
		},
		{
			name: "valid with endpoint",
			aws: config.AWS{
				Endpoint: "http://localhost:9000",
				UseSSL:   false,
			},
		},
		{
			name: "valid with credentials",
			aws: config.AWS{
				Credentials: config.CredentialsConfig{
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				},
			},
		},
		{
			name: "valid with profile",
			aws: config.AWS{
				Credentials: config.CredentialsConfig{
					Profile: "production",
				},
			},
		},
		{
			name: "both region and endpoint",
			aws: config.AWS{
				Region:   "us-east-1",
				Endpoint: "http://localhost:9000",
			},
			err: "cannot specify both Region and Endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.aws.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAWS_ValidateCredentialsConfig(t *testing.T) {
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
			t.Parallel()

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

func TestAWS_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetAWSFlags(fs)

	// Verify all flags exist
	flags := []string{
		"aws.region",
		"aws.endpoint",
		"aws.use_ssl",
		"aws.credentials.access_key_id",
		"aws.credentials.secret_access_key",
		"aws.credentials.session_token",
		"aws.credentials.file",
		"aws.credentials.profile",
	}

	for _, flag := range flags {
		require.NotNil(t, fs.Lookup(flag), "flag %s should exist", flag)
	}

	// Verify bucket flag does NOT exist (moved to S3 config)
	require.Nil(t, fs.Lookup("aws.bucket"), "aws.bucket flag should not exist")
}

func TestAWS_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		v.Set("aws.region", "us-west-2")
		v.Set("aws.use_ssl", true)
		v.Set("aws.credentials.access_key_id", "AKIATEST")
		v.Set("aws.credentials.secret_access_key", "secret123")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.AWS)
		require.Equal(t, "us-west-2", cfg.AWS.Region)
		require.True(t, cfg.AWS.UseSSL)
		require.Equal(t, "AKIATEST", cfg.AWS.Credentials.AccessKeyID)
		require.Equal(t, "secret123", cfg.AWS.Credentials.SecretAccessKey)
	})

	t.Run("from flags", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetAWSFlags(fs)

		require.NoError(t, fs.Parse([]string{
			"--aws.endpoint=http://localhost:9000",
		}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.AWS)
		require.Equal(t, "http://localhost:9000", cfg.AWS.Endpoint)
	})
}

func TestAWS_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_AWS_REGION", "ap-southeast-1")
	t.Setenv("NARWAL_AWS_USE_SSL", "true")
	t.Setenv("NARWAL_AWS_CREDENTIALS_ACCESS_KEY_ID", "ENV_KEY")

	var cfg config.Config

	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.AWS)
	require.Equal(t, "ap-southeast-1", cfg.AWS.Region)
	require.True(t, cfg.AWS.UseSSL)
	require.Equal(t, "ENV_KEY", cfg.AWS.Credentials.AccessKeyID)
}
