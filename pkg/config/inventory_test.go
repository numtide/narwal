package config_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestInventory_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		inventory      config.Inventory
		expectedPrefix string
		err            string
	}{
		{
			name:           "empty prefix",
			inventory:      config.Inventory{},
			expectedPrefix: "",
		},
		{
			name: "prefix without trailing slash",
			inventory: config.Inventory{
				BucketPrefix: "nix-cache/inventory",
			},
			expectedPrefix: "nix-cache/inventory/",
		},
		{
			name: "prefix with trailing slash",
			inventory: config.Inventory{
				BucketPrefix: "nix-cache/inventory/",
			},
			expectedPrefix: "nix-cache/inventory/",
		},
		{
			name: "with force download flag",
			inventory: config.Inventory{
				BucketPrefix:         "data/",
				ForceNarInfoDownload: true,
			},
			expectedPrefix: "data/",
		},
		{
			name: "with delete invalid flag",
			inventory: config.Inventory{
				BucketPrefix:          "data/",
				DeleteInvalidNarInfos: true,
			},
			expectedPrefix: "data/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.inventory.Validate()
			if tt.err != "" {
				require.ErrorIs(t, err, config.ErrInvalidConfig)
				require.ErrorContains(t, err, tt.err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedPrefix, tt.inventory.BucketPrefix)
			}
		})
	}
}

func TestInventory_SetFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetInventoryFlags(fs)

	// Verify all flags exist
	flags := map[string]string{
		"inventory.bucket_prefix":            "nix-cache/nix-cache-inventory",
		"inventory.force_nar_info_download":  "false",
		"inventory.delete_invalid_nar_infos": "false",
	}

	for name, defValue := range flags {
		flag := fs.Lookup(name)
		require.NotNil(t, flag, "flag %s should exist", name)
		require.Equal(t, defValue, flag.DefValue, "flag %s default value", name)
	}
}

func TestInventory_FromViper(t *testing.T) {
	t.Parallel()

	t.Run("from viper set", func(t *testing.T) {
		t.Parallel()
		v := viper.New()
		v.Set("inventory.bucket_prefix", "custom/prefix")
		v.Set("inventory.force_nar_info_download", true)
		v.Set("inventory.delete_invalid_nar_infos", true)

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Inventory)
		require.Equal(t, "custom/prefix", cfg.Inventory.BucketPrefix)
		require.True(t, cfg.Inventory.ForceNarInfoDownload)
		require.True(t, cfg.Inventory.DeleteInvalidNarInfos)
	})

	t.Run("from flags", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetInventoryFlags(fs)

		require.NoError(t, fs.Parse([]string{
			"--inventory.bucket_prefix=flag/prefix",
			"--inventory.force_nar_info_download=true",
		}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Inventory)
		require.Equal(t, "flag/prefix", cfg.Inventory.BucketPrefix)
		require.True(t, cfg.Inventory.ForceNarInfoDownload)
	})

	t.Run("flag overrides default", func(t *testing.T) {
		v := viper.New()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.SetInventoryFlags(fs)
		require.NoError(t, fs.Parse([]string{
			"--inventory.bucket_prefix=flag/prefix",
		}))
		require.NoError(t, v.BindPFlags(fs))

		var cfg config.Config
		err := config.FromViper(v, &cfg)
		require.NoError(t, err)
		require.NotNil(t, cfg.Inventory)
		require.Equal(t, "flag/prefix", cfg.Inventory.BucketPrefix)
		require.False(t, cfg.Inventory.ForceNarInfoDownload) // default
	})
}

func TestInventory_EnvOverride(t *testing.T) {
	v := viper.New()
	config.BindEnvVars(v, "NARWAL", config.Config{})

	t.Setenv("NARWAL_INVENTORY_BUCKET_PREFIX", "env/prefix")
	t.Setenv("NARWAL_INVENTORY_FORCE_NAR_INFO_DOWNLOAD", "true")
	t.Setenv("NARWAL_INVENTORY_DELETE_INVALID_NAR_INFOS", "true")

	var cfg config.Config
	err := config.FromViper(v, &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Inventory)
	require.Equal(t, "env/prefix", cfg.Inventory.BucketPrefix)
	require.True(t, cfg.Inventory.ForceNarInfoDownload)
	require.True(t, cfg.Inventory.DeleteInvalidNarInfos)
}
