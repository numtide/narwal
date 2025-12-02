package config_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestBadger_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		badger config.Badger
		err    string
	}{
		{
			name:   "valid path",
			badger: config.Badger{Path: "./test-db"},
		},
		{
			name:   "absolute path",
			badger: config.Badger{Path: "/var/lib/narwal/db"},
		},
		{
			name:   "empty path",
			badger: config.Badger{},
			err:    "badger db path is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.badger.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBadger_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetBadgerFlags(fs)

	pathFlag := fs.Lookup("badger.path")
	require.NotNil(t, pathFlag)
	require.Equal(t, "./narwal-db", pathFlag.DefValue)
}

func TestBadger_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		v.Set("badger.path", "/custom/path")

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Badger)
		require.Equal(t, "/custom/path", cfg.Badger.Path)
	})

	t.Run("from flags", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetBadgerFlags(fs)

		require.NoError(t, fs.Parse([]string{"--badger.path=/flag/path"}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Badger)
		require.Equal(t, "/flag/path", cfg.Badger.Path)
	})

	t.Run("uses default when no flag provided", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetBadgerFlags(fs)

		require.NoError(t, fs.Parse([]string{}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Badger)
		require.Equal(t, "./narwal-db", cfg.Badger.Path)
	})
}

func TestBadger_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_BADGER_PATH", "/env/path")

	var cfg config.Config
	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Badger)
	require.Equal(t, "/env/path", cfg.Badger.Path)
}
