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
	S3       S3       `mapstructure:"s3"`
	HTTP     HTTP     `mapstructure:"http"`
	Postgres Postgres `mapstructure:"postgres"`
}

func (c *Config) Validate() error {
	if err := c.S3.Validate(); err != nil {
		return err
	}

	if err := c.HTTP.Validate(); err != nil {
		return err
	}

	return nil
}

// SetFlags configures the provided FlagSet with predefined flags. It modifies the passed FlagSet directly.
func SetFlags(fs *pflag.FlagSet) {
	fs.String("s3.endpoint", "", "S3 Endpoint URL")
	fs.String("s3.access_key", "", "S3 Access Key")
	fs.String("s3.secret_key", "", "S3 Secret Key")
	fs.String("s3.bucket_name", "", "S3 Bucket Name")
	fs.Bool("s3.ssl_enabled", false, "Use SSL when connecting to S3")

	fs.Int16("http.port", 7777, "HTTP port to listen on")
	fs.String("http.host", "127.0.0.1", "HTTP host to listen on")

	fs.String("postgres.url", "", "Postgres URL")
}

func NewViper() (*viper.Viper, error) {
	v := viper.New()

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
