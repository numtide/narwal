package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/adrg/xdg"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// ErrInvalidConfig represents an error indicating that the provided configuration is invalid.
var ErrInvalidConfig = errors.New("invalid config")

type Config struct {
	HTTP HTTP `mapstructure:"http"`
}

func (c *Config) Validate() error {
	if err := c.HTTP.Validate(); err != nil {
		return err
	}

	return nil
}

// SetFlags configures the provided FlagSet with predefined flags. It modifies the passed FlagSet directly.
func SetFlags(fs *pflag.FlagSet) {
	fs.String("http.port", "7777", "HTTP port to listen on")
	fs.String("http.host", "127.0.0.1", "HTTP host to listen on")
}

func NewViper() (*viper.Viper, error) {
	v := viper.New()

	// set config type to TOML
	v.SetConfigType("toml")

	// setup automatic env override with the `NIX_BINARY_CACHE_` prefix
	v.SetEnvPrefix("nix_binary_cache")
	v.AutomaticEnv()

	// to target a sub config section in an ENV variable use "__" in place of "."
	// for example `foo.bar.baz` would be `NIX_BINARY_CACHE__FOO__BAR_BAZ`
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))

	// set config filename to nix-binary-cache.toml
	v.SetConfigName("nix-binary-cache")

	// look in the current working directory and /etc/nix-binary-cache for the nix-binary-cache.toml config file
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/nix-binary-cache")

	// add the standard xdg config file path too
	xdgPath, err := xdg.ConfigFile("nix-binary-cache/nix-binary-cache.toml")
	if err != nil {
		return nil, fmt.Errorf("failed to create xdg path for config file: %w", err)
	}

	v.AddConfigPath(xdgPath)

	return v, nil
}

func FromViper(v *viper.Viper) (*Config, error) {
	cfg := &Config{}

	// add some custom decoders
	decoderOpts := viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(),     // standard decoder
			mapstructure.StringToTimeDurationHookFunc(), // string to time
			mapstructure.StringToSliceHookFunc(","),     // handle lists,
		),
	)

	// unmarshal into config instance
	if err := v.Unmarshal(cfg, decoderOpts); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// validate the config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	return cfg, nil
}
