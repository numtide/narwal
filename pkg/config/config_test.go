package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Config
		err  string
	}{
		{
			name: "empty config - all nil",
			cfg:  config.Config{},
		},
		{
			name: "valid badger only",
			cfg: config.Config{
				Badger: &config.Badger{Path: "./db"},
			},
		},
		{
			name: "invalid badger",
			cfg: config.Config{
				Badger: &config.Badger{Path: ""},
			},
			err: "badger db path is required",
		},
		{
			name: "valid AWS only",
			cfg: config.Config{
				AWS: &config.AWS{Region: "us-east-1"},
			},
		},
		{
			name: "valid S3 only",
			cfg: config.Config{
				S3: &config.S3{Bucket: "bucket"},
			},
		},
		{
			name: "invalid S3",
			cfg: config.Config{
				S3: &config.S3{Bucket: ""},
			},
			err: "s3 bucket name is required",
		},
		{
			name: "valid inventory only",
			cfg: config.Config{
				Inventory: &config.Inventory{BucketPrefix: "prefix"},
			},
		},
		{
			name: "multiple valid configs",
			cfg: config.Config{
				Badger: &config.Badger{Path: "./db"},
				AWS:    &config.AWS{Region: "us-east-1"},
				S3:     &config.S3{Bucket: "bucket"},
			},
		},
		{
			name: "first invalid stops validation",
			cfg: config.Config{
				Badger: &config.Badger{Path: ""}, // invalid
				S3:     &config.S3{Bucket: "bucket"},
			},
			err: "badger db path is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestConfig_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("unmarshals complete config", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		v.Set("badger.path", "/path/to/db")
		v.Set("s3.bucket", "test-bucket")
		v.Set("aws.region", "us-west-2")
		v.Set("inventory.bucket_prefix", "nix-cache/")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)

		require.NotNil(t, cfg.Badger)
		require.Equal(t, "/path/to/db", cfg.Badger.Path)

		require.NotNil(t, cfg.S3)
		require.Equal(t, "test-bucket", cfg.S3.Bucket)

		require.NotNil(t, cfg.AWS)
		require.Equal(t, "us-west-2", cfg.AWS.Region)

		require.NotNil(t, cfg.Inventory)
		require.Equal(t, "nix-cache/", cfg.Inventory.BucketPrefix)
	})

	t.Run("handles nested credentials", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		v.Set("aws.credentials.access_key_id", "AKIATEST")
		v.Set("aws.credentials.secret_access_key", "secret")
		v.Set("aws.credentials.profile", "production")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)

		require.NotNil(t, cfg.AWS)
		require.Equal(t, "AKIATEST", cfg.AWS.Credentials.AccessKeyID)
		require.Equal(t, "secret", cfg.AWS.Credentials.SecretAccessKey)
		require.Equal(t, "production", cfg.AWS.Credentials.Profile)
	})
}

func TestConfig_BindEnvVars(t *testing.T) {
	t.Run("binds top-level fields", func(t *testing.T) {
		v := viper.New()
		config.BindEnvVars(v, "NARWAL", config.Config{})

		t.Setenv("NARWAL_BADGER_PATH", "/env/path")
		t.Setenv("NARWAL_POSTGRES_URL", "postgres://env/db")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)

		require.NotNil(t, cfg.Badger)
		require.Equal(t, "/env/path", cfg.Badger.Path)
	})

	t.Run("binds nested fields", func(t *testing.T) {
		v := viper.New()
		config.BindEnvVars(v, "NARWAL", config.Config{})

		t.Setenv("NARWAL_S3_BUCKET", "env-bucket")
		t.Setenv("NARWAL_AWS_CREDENTIALS_ACCESS_KEY_ID", "ENV_KEY_ID")
		t.Setenv("NARWAL_AWS_CREDENTIALS_SECRET_ACCESS_KEY", "ENV_SECRET")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)

		require.NotNil(t, cfg.S3)
		require.Equal(t, "env-bucket", cfg.S3.Bucket)
		require.NotNil(t, cfg.AWS)
		require.Equal(t, "ENV_KEY_ID", cfg.AWS.Credentials.AccessKeyID)
		require.Equal(t, "ENV_SECRET", cfg.AWS.Credentials.SecretAccessKey)
	})

	t.Run("env vars work without config", func(t *testing.T) {
		v := viper.New()
		config.BindEnvVars(v, "NARWAL", config.Config{})

		// Set env vars without any v.Set() calls
		t.Setenv("NARWAL_S3_BUCKET", "env-bucket")
		t.Setenv("NARWAL_AWS_REGION", "env-region")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)

		require.NotNil(t, cfg.S3)
		require.Equal(t, "env-bucket", cfg.S3.Bucket)
		require.NotNil(t, cfg.AWS)
		require.Equal(t, "env-region", cfg.AWS.Region)
	})
}

func TestConfig_ConfigureViper(t *testing.T) {
	t.Parallel()

	t.Run("configures viper with correct settings", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		err := config.ConfigureViper(v)
		require.NoError(t, err)
	})
}

func TestConfig_FromTOMLFile(t *testing.T) {
	t.Parallel()

	// Create a temp TOML config file
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "narwal.toml")

	//nolint:gosec // Test credentials are not real
	tomlContent := `
[badger]
path = "/toml/badger/path"

[s3]
bucket = "toml-bucket"

[aws]
region = "us-east-1"
use_ssl = true

[aws.credentials]
access_key_id = "TOML_KEY"
secret_access_key = "TOML_SECRET"

[postgres]
url = "postgres://toml:toml@localhost:5432/tomldb"

[inventory]
bucket_prefix = "toml/prefix/"
force_nar_info_download = true
`

	require.NoError(t, os.WriteFile(configFile, []byte(tomlContent), 0o600))

	v := viper.New()
	v.SetConfigType("toml")
	v.SetConfigFile(configFile)

	require.NoError(t, v.ReadInConfig())

	var cfg config.Config

	err := config.FromViper(v, &cfg)
	require.NoError(t, err)

	require.NotNil(t, cfg.Badger)
	require.Equal(t, "/toml/badger/path", cfg.Badger.Path)

	require.NotNil(t, cfg.S3)
	require.Equal(t, "toml-bucket", cfg.S3.Bucket)

	require.NotNil(t, cfg.AWS)
	require.Equal(t, "us-east-1", cfg.AWS.Region)
	require.True(t, cfg.AWS.UseSSL)
	require.Equal(t, "TOML_KEY", cfg.AWS.Credentials.AccessKeyID)
	require.Equal(t, "TOML_SECRET", cfg.AWS.Credentials.SecretAccessKey)

	require.NotNil(t, cfg.Inventory)
	require.Equal(t, "toml/prefix/", cfg.Inventory.BucketPrefix)
	require.True(t, cfg.Inventory.ForceNarInfoDownload)
}

func TestConfig_Precedence(t *testing.T) {
	t.Run("env overrides config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "narwal.toml")

		tomlContent := `
[s3]
bucket = "toml-bucket"

[aws]
region = "toml-region"
`

		require.NoError(t, os.WriteFile(configFile, []byte(tomlContent), 0o600))

		v := viper.New()
		v.SetConfigType("toml")
		v.SetConfigFile(configFile)

		// Bind env vars
		config.BindEnvVars(v, "NARWAL", config.Config{})

		// Read config file
		require.NoError(t, v.ReadInConfig())

		// Set env var for bucket (env should override config file)
		t.Setenv("NARWAL_S3_BUCKET", "env-bucket")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)

		require.NotNil(t, cfg.S3)
		require.Equal(t, "env-bucket", cfg.S3.Bucket) // env wins over toml
		require.NotNil(t, cfg.AWS)
		require.Equal(t, "toml-region", cfg.AWS.Region) // toml value (no override)
	})

	t.Run("viper set has highest precedence", func(t *testing.T) {
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "narwal.toml")

		tomlContent := `
[s3]
bucket = "toml-bucket"
`

		require.NoError(t, os.WriteFile(configFile, []byte(tomlContent), 0o600))

		v := viper.New()
		v.SetConfigType("toml")
		v.SetConfigFile(configFile)

		// Bind env vars
		config.BindEnvVars(v, "NARWAL", config.Config{})

		// Read config file
		require.NoError(t, v.ReadInConfig())

		// Set env var
		t.Setenv("NARWAL_S3_BUCKET", "env-bucket")

		// v.Set() has highest precedence in viper
		v.Set("s3.bucket", "set-bucket")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)

		require.NotNil(t, cfg.S3)
		require.Equal(t, "set-bucket", cfg.S3.Bucket) // v.Set() has highest precedence
	})
}
