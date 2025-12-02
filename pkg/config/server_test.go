package config_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestServer_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		server config.Server
		err    string
	}{
		{
			name: "valid config",
			server: config.Server{
				AWS: config.AWS{},
				S3: config.S3{
					Bucket: "my-bucket",
				},
				HTTP: config.HTTP{
					Host: "127.0.0.1",
					Port: 8080,
				},
				Postgres: config.Postgres{
					URL: "postgres://user:pass@localhost:5432/db",
				},
			},
		},
		{
			name: "missing S3 bucket",
			server: config.Server{
				AWS: config.AWS{},
				S3:  config.S3{},
				HTTP: config.HTTP{
					Host: "127.0.0.1",
					Port: 8080,
				},
				Postgres: config.Postgres{
					URL: "postgres://user:pass@localhost:5432/db",
				},
			},
			err: "s3 bucket name is required",
		},
		{
			name: "missing HTTP host",
			server: config.Server{
				AWS: config.AWS{},
				S3: config.S3{
					Bucket: "my-bucket",
				},
				HTTP: config.HTTP{
					Port: 8080,
				},
				Postgres: config.Postgres{
					URL: "postgres://user:pass@localhost:5432/db",
				},
			},
			err: "http host is required",
		},
		{
			name: "missing HTTP port",
			server: config.Server{
				AWS: config.AWS{},
				S3: config.S3{
					Bucket: "my-bucket",
				},
				HTTP: config.HTTP{
					Host: "127.0.0.1",
				},
				Postgres: config.Postgres{
					URL: "postgres://user:pass@localhost:5432/db",
				},
			},
			err: "http port is required",
		},
		{
			name: "missing postgres URL",
			server: config.Server{
				AWS: config.AWS{},
				S3: config.S3{
					Bucket: "my-bucket",
				},
				HTTP: config.HTTP{
					Host: "127.0.0.1",
					Port: 8080,
				},
				Postgres: config.Postgres{},
			},
			err: "postgres url is required",
		},
		{
			name:   "empty config",
			server: config.Server{},
			err:    "s3 bucket name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.server.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServer_ValidateNestedConfigs(t *testing.T) {
	t.Parallel()

	t.Run("AWS validation propagates", func(t *testing.T) {
		t.Parallel()

		server := config.Server{
			AWS: config.AWS{
				Region:   "us-east-1",
				Endpoint: "http://localhost:9000", // conflict
			},
			S3: config.S3{
				Bucket: "bucket",
			},
			HTTP: config.HTTP{
				Host: "127.0.0.1",
				Port: 8080,
			},
			Postgres: config.Postgres{
				URL: "postgres://user:pass@localhost:5432/db",
			},
		}

		err := server.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "Region")
		require.Contains(t, err.Error(), "Endpoint")
	})

	t.Run("HTTP validation propagates", func(t *testing.T) {
		t.Parallel()

		server := config.Server{
			AWS: config.AWS{},
			S3: config.S3{
				Bucket: "bucket",
			},
			HTTP: config.HTTP{
				Host: "",
				Port: 8080,
			},
			Postgres: config.Postgres{
				URL: "postgres://user:pass@localhost:5432/db",
			},
		}

		err := server.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "http")
	})

	t.Run("Postgres validation propagates", func(t *testing.T) {
		t.Parallel()

		server := config.Server{
			AWS: config.AWS{},
			S3: config.S3{
				Bucket: "bucket",
			},
			HTTP: config.HTTP{
				Host: "127.0.0.1",
				Port: 8080,
			},
			Postgres: config.Postgres{
				URL: "",
			},
		}

		err := server.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "postgres")
	})
}
