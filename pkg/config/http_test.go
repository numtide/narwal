package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

const testUsername = "admin"

func TestHTTP_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		http config.HTTP
		err  string
	}{
		{
			name: "valid host and port",
			http: config.HTTP{
				Host: "127.0.0.1",
				Port: 8080,
			},
		},
		{
			name: "valid with all interfaces",
			http: config.HTTP{
				Host: "0.0.0.0",
				Port: 7777,
			},
		},
		{
			name: "empty host",
			http: config.HTTP{
				Port: 8080,
			},
			err: "http host is required",
		},
		{
			name: "zero port",
			http: config.HTTP{
				Host: "127.0.0.1",
			},
			err: "http port is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.http.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHTTP_ValidateBasicAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		http config.HTTP
		err  string
	}{
		{
			name: "basic auth disabled",
			http: config.HTTP{
				Host: "127.0.0.1",
				Port: 8080,
			},
		},
		{
			name: "basic auth enabled with password",
			http: func() config.HTTP {
				h := config.HTTP{
					Host: "127.0.0.1",
					Port: 8080,
				}
				h.BasicAuth.Enabled = true
				h.BasicAuth.Username = testUsername
				h.BasicAuth.Password = "secret"
				return h
			}(),
		},
		{
			name: "basic auth enabled without username",
			http: func() config.HTTP {
				h := config.HTTP{
					Host: "127.0.0.1",
					Port: 8080,
				}
				h.BasicAuth.Enabled = true
				h.BasicAuth.Password = "secret"
				return h
			}(),
			err: "basic auth username is required",
		},
		{
			name: "basic auth enabled without password",
			http: func() config.HTTP {
				h := config.HTTP{
					Host: "127.0.0.1",
					Port: 8080,
				}
				h.BasicAuth.Enabled = true
				h.BasicAuth.Username = testUsername
				return h
			}(),
			err: "either basic auth password or password_file must be provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.http.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHTTP_ValidatePasswordFile(t *testing.T) {
	t.Parallel()

	t.Run("reads password from file", func(t *testing.T) {
		// Create temp password file
		tmpDir := t.TempDir()
		passwordFile := filepath.Join(tmpDir, "password")
		require.NoError(t, os.WriteFile(passwordFile, []byte("file-secret"), 0o600))

		h := config.HTTP{
			Host: "127.0.0.1",
			Port: 8080,
		}
		h.BasicAuth.Enabled = true
		h.BasicAuth.Username = "admin"
		h.BasicAuth.PasswordFile = passwordFile

		err := h.Validate()
		require.NoError(t, err)
		require.Equal(t, "file-secret", h.BasicAuth.Password)
	})

	t.Run("password file not found", func(t *testing.T) {
		h := config.HTTP{
			Host: "127.0.0.1",
			Port: 8080,
		}
		h.BasicAuth.Enabled = true
		h.BasicAuth.Username = "admin"
		h.BasicAuth.PasswordFile = "/nonexistent/password"

		err := h.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, config.ErrInvalidConfig)
	})
}

func TestHTTP_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetHttpFlags(fs)

	// Verify all flags exist
	flags := map[string]string{
		"http.port":                     "7777",
		"http.host":                     "127.0.0.1",
		"http.basic_auth.enabled":       "false",
		"http.basic_auth.username":      "narwal",
		"http.basic_auth.password":      "",
		"http.basic_auth.password_file": "",
	}

	for name, defValue := range flags {
		flag := fs.Lookup(name)
		require.NotNil(t, flag, "flag %s should exist", name)
		require.Equal(t, defValue, flag.DefValue, "flag %s default value", name)
	}
}

func TestHTTP_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()
		v := viper.New()
		v.Set("http.host", "0.0.0.0")
		v.Set("http.port", 9090)
		v.Set("http.basic_auth.enabled", true)
		v.Set("http.basic_auth.username", "testuser")
		v.Set("http.basic_auth.password", "testpass")

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.HTTP)
		require.Equal(t, "0.0.0.0", cfg.HTTP.Host)
		require.Equal(t, 9090, cfg.HTTP.Port)
		require.True(t, cfg.HTTP.BasicAuth.Enabled)
		require.Equal(t, "testuser", cfg.HTTP.BasicAuth.Username)
		require.Equal(t, "testpass", cfg.HTTP.BasicAuth.Password)
	})

	t.Run("from flags", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetHttpFlags(fs)

		require.NoError(t, fs.Parse([]string{
			"--http.host=192.168.1.1",
			"--http.port=3000",
		}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.HTTP)
		require.Equal(t, "192.168.1.1", cfg.HTTP.Host)
		require.Equal(t, 3000, cfg.HTTP.Port)
	})

	t.Run("flag overrides default", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetHttpFlags(fs)
		require.NoError(t, fs.Parse([]string{"--http.port=2222"}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.HTTP)
		require.Equal(t, "127.0.0.1", cfg.HTTP.Host) // default
		require.Equal(t, 2222, cfg.HTTP.Port)        // overridden
	})
}

func TestHTTP_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_HTTP_HOST", "env-host")
	t.Setenv("NARWAL_HTTP_PORT", "3333")
	t.Setenv("NARWAL_HTTP_BASIC_AUTH_ENABLED", "true")
	t.Setenv("NARWAL_HTTP_BASIC_AUTH_USERNAME", "envuser")

	var cfg config.Config
	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.HTTP)
	require.Equal(t, "env-host", cfg.HTTP.Host)
	require.Equal(t, 3333, cfg.HTTP.Port)
	require.True(t, cfg.HTTP.BasicAuth.Enabled)
	require.Equal(t, "envuser", cfg.HTTP.BasicAuth.Username)
}
