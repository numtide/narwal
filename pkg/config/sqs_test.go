package config_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestSQS_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetSQSFlags(fs)

	require.NotNil(t, fs.Lookup("sqs.upload_event_queue"), "sqs.upload_event_queue flag should exist")
	require.NotNil(t, fs.Lookup("sqs.delete_event_queue"), "sqs.delete_event_queue flag should exist")
}

func TestSQS_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		v.Set("sqs.upload_event_queue", "upload-queue")
		v.Set("sqs.delete_event_queue", "delete-queue")

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.SQS)
		require.Equal(t, "upload-queue", cfg.SQS.UploadEventQueue)
		require.Equal(t, "delete-queue", cfg.SQS.DeleteEventQueue)
	})

	t.Run("from flags", func(t *testing.T) {
		t.Parallel()

		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetSQSFlags(fs)

		require.NoError(t, fs.Parse([]string{
			"--sqs.upload_event_queue=flag-upload-queue",
			"--sqs.delete_event_queue=flag-delete-queue",
		}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config

		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.SQS)
		require.Equal(t, "flag-upload-queue", cfg.SQS.UploadEventQueue)
		require.Equal(t, "flag-delete-queue", cfg.SQS.DeleteEventQueue)
	})
}

func TestSQS_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_SQS_UPLOAD_EVENT_QUEUE", "env-upload-queue")
	t.Setenv("NARWAL_SQS_DELETE_EVENT_QUEUE", "env-delete-queue")

	var cfg config.Config

	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.SQS)
	require.Equal(t, "env-upload-queue", cfg.SQS.UploadEventQueue)
	require.Equal(t, "env-delete-queue", cfg.SQS.DeleteEventQueue)
}
