package cobrautil

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func LoadConfig(cmd *cobra.Command, _ []string) (*config.Config, error) {
	var (
		err error
		cfg *config.Config
	)

	// bind our command's flags to viper
	if err = viper.BindPFlags(cmd.Flags()); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	// parse viper into our config object
	v := viper.GetViper()
	if err = config.FromViper(v, &cfg); err != nil {
		return nil, fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err = cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())

	return cfg, nil
}
