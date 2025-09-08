package config

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
)

// HTTP represents the configuration for an HTTP server, including port and interface.
type HTTP struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`

	// BasicAuth configuration
	BasicAuth struct {
		// Enable basic authentication
		Enabled bool `mapstructure:"enabled"`
		// Username for basic authentication
		Username string `mapstructure:"username"`
		// Password for basic authentication, can be a direct value
		Password string `mapstructure:"password"`
		// PasswordFile is a path to a file containing the password
		PasswordFile string `mapstructure:"password_file"`
	} `mapstructure:"basic_auth"`

	ListenAddr string `mapstructure:"-"`
}

// Validate checks the HTTP configuration to ensure required fields are set and derives the listen address.
// Returns an error if the configuration is invalid.
func (h *HTTP) Validate() error {
	if h.Host == "" {
		return fmt.Errorf("%w: http host is required", ErrInvalidConfig)
	}

	if h.Port == 0 {
		return fmt.Errorf("%w: http port is required", ErrInvalidConfig)
	}

	h.ListenAddr = fmt.Sprintf("%s:%d", h.Host, h.Port)

	if !h.BasicAuth.Enabled {
		return nil
	}

	if h.BasicAuth.Username == "" {
		return fmt.Errorf("%w: basic auth username is required when basic auth is enabled", ErrInvalidConfig)
	}

	if h.BasicAuth.PasswordFile != "" {
		password, err := os.ReadFile(h.BasicAuth.PasswordFile)
		if err != nil {
			return fmt.Errorf("%w: failed to read password file: %w", ErrInvalidConfig, err)
		}

		h.BasicAuth.Password = string(password)
	}

	if h.BasicAuth.Password == "" {
		return fmt.Errorf(
			"%w: either basic auth password or password_file must be provided when basic auth is enabled",
			ErrInvalidConfig,
		)
	}

	return nil
}

func SetHttpFlags(fs *pflag.FlagSet) {
	fs.Int16("http.port", 7777, "HTTP port to listen on")
	fs.String("http.host", "127.0.0.1", "HTTP host to listen on")

	// Basic Auth flags
	fs.Bool("http.basic_auth.enabled", false, "Enable HTTP Basic Authentication")
	fs.String("http.basic_auth.username", "narwal", "Username for HTTP Basic Authentication")
	fs.String("http.basic_auth.password", "", "Password for HTTP Basic Authentication (direct value)")
	fs.String("http.basic_auth.password_file", "", "Path to file containing password for HTTP Basic Authentication")
}
