package server

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func importCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import data into the postgres database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report := args[0]

			// parse viper into our config object
			var cfg *config.Config

			if err := config.FromViper(viper.GetViper(), &cfg); err != nil {
				return fmt.Errorf("failed to create config from viper: %w", err)
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			log.Info("config loaded", "config_file", viper.ConfigFileUsed())

			if err := server.Import(cmd.Context(), cfg, report); err != nil {
				return fmt.Errorf("import failed: %w", err)
			}

			return nil
		},
	}

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
