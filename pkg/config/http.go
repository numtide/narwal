package config

import "fmt"

// HTTP represents the configuration for an HTTP server, including port and interface.
type HTTP struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`

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

	return nil
}
