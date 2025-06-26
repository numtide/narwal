package config

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// BindInventoryFlagsToViper binds common inventory command flags to viper.
// This eliminates code duplication across inventory commands.
func BindInventoryFlagsToViper(cmd *cobra.Command) {
	// Common flags used across inventory commands
	flagBindings := []string{
		"report",
		"bucket",
		"region",
		"prefix",
		"parallel",
		"error-file",
	}

	for _, flagName := range flagBindings {
		if flag := cmd.Flag(flagName); flag != nil && flag.Changed {
			viper.Set(flagName, flag.Value.String())
		}
	}
}

// ParseAndValidateInventoryConfig parses viper config and validates inventory settings.
// This centralizes the common config parsing pattern used across inventory commands.
func ParseAndValidateInventoryConfig(ctx context.Context) (*Config, *Inventory, error) {
	var fullCfg Config
	if err := FromViper(viper.GetViper(), &fullCfg); err != nil {
		return nil, nil, fmt.Errorf("failed to create config from viper: %w", err)
	}

	cfg := &fullCfg.Inventory
	if err := cfg.Validate(ctx, nil, fullCfg.Workarea.Path); err != nil {
		return nil, nil, fmt.Errorf("invalid config: %w", err)
	}

	return &fullCfg, cfg, nil
}
