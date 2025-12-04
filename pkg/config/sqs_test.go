package config_test

import (
	"testing"
	"time"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestSQS_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sqs  config.SQS
		err  string
	}{
		{
			name: "valid with zero timeout (uses default)",
			sqs:  config.SQS{LongPollingTimeout: 0},
		},
		{
			name: "valid with custom timeout",
			sqs:  config.SQS{LongPollingTimeout: 10 * time.Second},
		},
		{
			name: "valid with max timeout",
			sqs:  config.SQS{LongPollingTimeout: 20 * time.Second},
		},
		{
			name: "invalid negative timeout",
			sqs:  config.SQS{LongPollingTimeout: -1 * time.Second},
			err:  "cannot be negative",
		},
		{
			name: "invalid exceeds max timeout",
			sqs:  config.SQS{LongPollingTimeout: 21 * time.Second},
			err:  "cannot exceed 20 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.sqs.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSQS_GetLongPollingTimeout(t *testing.T) {
	t.Parallel()

	t.Run("returns default when zero", func(t *testing.T) {
		t.Parallel()

		sqs := config.SQS{LongPollingTimeout: 0}
		require.Equal(t, config.DefaultLongPollingTimeout, sqs.GetLongPollingTimeout())
	})

	t.Run("returns configured value", func(t *testing.T) {
		t.Parallel()

		sqs := config.SQS{LongPollingTimeout: 5 * time.Second}
		require.Equal(t, 5*time.Second, sqs.GetLongPollingTimeout())
	})
}

func TestSQS_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetSQSFlags(fs)

	require.NotNil(t, fs.Lookup("sqs.upload_event_queue"), "sqs.upload_event_queue flag should exist")
	require.NotNil(t, fs.Lookup("sqs.delete_event_queue"), "sqs.delete_event_queue flag should exist")
	require.NotNil(t, fs.Lookup("sqs.long_polling_timeout"), "sqs.long_polling_timeout flag should exist")
}

func TestSQS_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		v.Set("sqs.upload_event_queue", "upload-queue")
		v.Set("sqs.delete_event_queue", "delete-queue")
		v.Set("sqs.long_polling_timeout", "15s")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.SQS)
		require.Equal(t, "upload-queue", cfg.SQS.UploadEventQueue)
		require.Equal(t, "delete-queue", cfg.SQS.DeleteEventQueue)
		require.Equal(t, 15*time.Second, cfg.SQS.LongPollingTimeout)
	})

	t.Run("from flags", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetSQSFlags(fs)

		require.NoError(t, fs.Parse([]string{
			"--sqs.upload_event_queue=flag-upload-queue",
			"--sqs.delete_event_queue=flag-delete-queue",
			"--sqs.long_polling_timeout=10s",
		}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.SQS)
		require.Equal(t, "flag-upload-queue", cfg.SQS.UploadEventQueue)
		require.Equal(t, "flag-delete-queue", cfg.SQS.DeleteEventQueue)
		require.Equal(t, 10*time.Second, cfg.SQS.LongPollingTimeout)
	})
}

func TestSQS_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_SQS_UPLOAD_EVENT_QUEUE", "env-upload-queue")
	t.Setenv("NARWAL_SQS_DELETE_EVENT_QUEUE", "env-delete-queue")
	t.Setenv("NARWAL_SQS_LONG_POLLING_TIMEOUT", "5s")

	var cfg config.Config

	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.SQS)
	require.Equal(t, "env-upload-queue", cfg.SQS.UploadEventQueue)
	require.Equal(t, "env-delete-queue", cfg.SQS.DeleteEventQueue)
	require.Equal(t, 5*time.Second, cfg.SQS.LongPollingTimeout)
}
