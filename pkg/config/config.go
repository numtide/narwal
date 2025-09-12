package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/adrg/xdg"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

var (
	// ErrInvalidConfig represents an error indicating that the provided configuration is invalid.
	ErrInvalidConfig = errors.New("invalid config")

	//nolint:gochecknoglobals
	validators = []validator{
		func(c *Config) error {
			if c.Badger == nil {
				return nil
			}
			return c.Badger.Validate()
		},
		func(c *Config) error {
			if c.GC == nil {
				return nil
			}
			return c.GC.Validate()
		},
		func(c *Config) error {
			if c.HTTP == nil {
				return nil
			}
			return c.HTTP.Validate()
		},
		func(c *Config) error {
			if c.Postgres == nil {
				return nil
			}
			return c.Postgres.Validate()
		},
		func(c *Config) error {
			if c.S3 == nil {
				return nil
			}
			return c.S3.Validate()
		},
		func(c *Config) error {
			if c.Inventory == nil {
				return nil
			}
			return c.Inventory.Validate()
		},
	}
)

type Config struct {
	Badger    *Badger    `mapstructure:"badger"`
	Inventory *Inventory `mapstructure:"inventory"`
	GC        *GC        `mapstructure:"gc"`
	HTTP      *HTTP      `mapstructure:"http"`
	Postgres  *Postgres  `mapstructure:"postgres"`
	S3        *S3        `mapstructure:"s3"`
}

type validator = func(*Config) error

func (c *Config) Validate() error {
	for _, v := range validators {
		if err := v(c); err != nil {
			return err
		}
	}

	return nil
}

func ConfigureViper(v *viper.Viper) error {
	// set config type to TOML
	v.SetConfigType("toml")

	// setup automatic env override with the `NARWAL` prefix
	v.SetEnvPrefix("narwal")
	v.AutomaticEnv()

	// to target a sub config section in an ENV variable use "__" in place of "."
	// for example `foo.bar.baz` would be `NARWAL_FOO__BAR__BAZ`
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))

	// set config filename to narwal.toml
	v.SetConfigName("narwal")

	// look in the current working directory and /etc/narwal for the narwal.toml config file
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/narwal")

	// add the standard xdg config file path too
	xdgPath, err := xdg.ConfigFile("narwal/narwal.toml")
	if err != nil {
		return fmt.Errorf("failed to create xdg path for config file: %w", err)
	}

	v.AddConfigPath(xdgPath)

	return nil
}

func FromViper(v *viper.Viper, cfg any) error {
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
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return nil
}
