package config_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestPostgres_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		postgres config.Postgres
		err      string
	}{
		{
			name:     "valid URL",
			postgres: config.Postgres{URL: "postgres://user:pass@localhost:5432/db"},
		},
		{
			name:     "URL with sslmode",
			postgres: config.Postgres{URL: "postgres://user:pass@localhost:5432/db?sslmode=disable"},
		},
		{
			name:     "empty URL",
			postgres: config.Postgres{},
			err:      "postgres url is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.postgres.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPostgres_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetPostgresFlags(fs)

	urlFlag := fs.Lookup("postgres.url")
	require.NotNil(t, urlFlag)
	require.Empty(t, urlFlag.DefValue)
}

func TestPostgres_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()
		v := viper.New()
		v.Set("postgres.url", "postgres://test:test@localhost:5432/testdb")

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Postgres)
		require.Equal(t, "postgres://test:test@localhost:5432/testdb", cfg.Postgres.URL)
	})

	t.Run("from flags", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetPostgresFlags(fs)

		require.NoError(t, fs.Parse([]string{"--postgres.url=postgres://flag:flag@localhost:5432/flagdb"}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Postgres)
		require.Equal(t, "postgres://flag:flag@localhost:5432/flagdb", cfg.Postgres.URL)
	})

	t.Run("flag overrides default", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetPostgresFlags(fs)
		require.NoError(t, fs.Parse([]string{"--postgres.url=postgres://flag:flag@localhost:5432/flagdb"}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Postgres)
		require.Equal(t, "postgres://flag:flag@localhost:5432/flagdb", cfg.Postgres.URL)
	})
}

func TestPostgres_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_POSTGRES_URL", "postgres://env:env@localhost:5432/envdb")

	var cfg config.Config
	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Postgres)
	require.Equal(t, "postgres://env:env@localhost:5432/envdb", cfg.Postgres.URL)
}
