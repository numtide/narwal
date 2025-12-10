package server

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func importNarinfosCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import-narinfos",
		Short: "Import all narinfos from badger inventory into postgres",
		Long: `Import all narinfos from the badger inventory database into the PostgreSQL nar_info table.

This command uses Badger's Stream API for efficient reading and PostgreSQL COPY
protocol for fast bulk insertion. The nar_info table is truncated before import.

Recommended PostgreSQL settings for bulk import:
  SET synchronous_commit = off;
  SET max_wal_size = '10GB';
  SET checkpoint_completion_target = 0.9;`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// parse viper into our config object
			var cfg *config.Config

			if err := config.FromViper(viper.GetViper(), &cfg); err != nil {
				return fmt.Errorf("failed to create config from viper: %w", err)
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			log.Info("config loaded", "config_file", viper.ConfigFileUsed())

			if err := server.ImportNarinfos(cmd.Context(), cfg); err != nil {
				return fmt.Errorf("narinfo import failed: %w", err)
			}

			return nil
		},
	}

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
