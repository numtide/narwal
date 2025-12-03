package config

import (
	"errors"
	"fmt"
	"reflect"
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
			if c.AWS == nil {
				return nil
			}
			return c.AWS.Validate()
		},
		func(c *Config) error {
			if c.Inventory == nil {
				return nil
			}
			return c.Inventory.Validate()
		},
		func(c *Config) error {
			if c.S3 == nil {
				return nil
			}
			return c.S3.Validate()
		},
		func(c *Config) error {
			if c.SQS == nil {
				return nil
			}
			return c.SQS.Validate()
		},
	}
)

type Config struct {
	Badger    *Badger    `mapstructure:"badger"`
	Inventory *Inventory `mapstructure:"inventory"`
	GC        *GC        `mapstructure:"gc"`
	HTTP      *HTTP      `mapstructure:"http"`
	Postgres  *Postgres  `mapstructure:"postgres"`
	AWS       *AWS       `mapstructure:"aws"`
	S3        *S3        `mapstructure:"s3"`
	SQS       *SQS       `mapstructure:"sqs"`
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

	// enable automatic env variable reading
	v.AutomaticEnv()

	// bind env vars with single-underscore format (e.g., NARWAL_S3_USE_SSL)
	BindEnvVars(v, "NARWAL", Config{})

	// set config filename to narwal.toml
	v.SetConfigName("narwal")

	// look in the current working directory and /etc/narwal for the narwal.toml config file
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/narwal")

	// add the standard xdg config directory too
	v.AddConfigPath(xdg.ConfigHome + "/narwal")

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

// BindEnvVars walks the config struct and binds each field to an environment variable.
// This allows single-underscore env vars like NARWAL_S3_USE_SSL instead of double-underscore.
func BindEnvVars(v *viper.Viper, prefix string, cfg any) {
	bindEnvVarsRecursive(v, prefix, "", reflect.TypeOf(cfg))
}

func bindEnvVarsRecursive(v *viper.Viper, envPrefix, keyPrefix string, t reflect.Type) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return
	}

	for i := range t.NumField() {
		field := t.Field(i)

		// get the mapstructure tag, skip if not present
		tag := field.Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}

		// build the viper key path (dot-separated)
		viperKey := tag
		if keyPrefix != "" {
			viperKey = keyPrefix + "." + tag
		}

		// build the env var name (underscore-separated, uppercase)
		envVar := strings.ToUpper(envPrefix + "_" + strings.ReplaceAll(viperKey, ".", "_"))

		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		if fieldType.Kind() == reflect.Struct {
			// recurse into nested structs
			bindEnvVarsRecursive(v, envPrefix, viperKey, fieldType)
		} else {
			// bind leaf field to env var
			_ = v.BindEnv(viperKey, envVar)
		}
	}
}
