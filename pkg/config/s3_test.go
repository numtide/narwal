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
			name: "valid with bucket",
			s3: config.S3{
				Bucket: "my-bucket",
			},
		},
		{
			name: "empty bucket",
			s3:   config.S3{},
			err:  "s3 bucket name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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

func TestS3_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetS3Flags(fs)

	// Verify s3.bucket flag exists
	require.NotNil(t, fs.Lookup("s3.bucket"), "s3.bucket flag should exist")
}

func TestS3_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		v.Set("s3.bucket", "test-bucket")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.S3)
		require.Equal(t, "test-bucket", cfg.S3.Bucket)
	})

	t.Run("from flags", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetS3Flags(fs)

		require.NoError(t, fs.Parse([]string{
			"--s3.bucket=flag-bucket",
		}))
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

	var cfg config.Config

	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.S3)
	require.Equal(t, "env-bucket", cfg.S3.Bucket)
}
