package root

import (
	"fmt"
	"regexp"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//nolint:gochecknoglobals
var (
	cfg *config.GC

	pg *pgxpool.Pool

	storePathPattern = regexp.MustCompile(`^([a-z0-9]{32})|/nix/store/([a-z0-9]{32})-.*$`)
)

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "root",
		Short:              "GC root related commands",
		PersistentPreRunE:  preRunE,
		PersistentPostRunE: postRunE,
	}

	config.SetPostgresFlags(cmd.PersistentFlags())

	cmd.AddCommand(rootAdd())
	cmd.AddCommand(rootRemove())
	cmd.AddCommand(rootList())

	return cmd
}

func preRunE(cmd *cobra.Command, _ []string) error {
	var err error

	// parse viper into our config object
	if err = config.FromViper(viper.GetViper(), &cfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err = cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())

	// connect to postgres
	pg, err = cfg.Postgres.Connect(cmd.Context(), false)
	if err != nil {
		//nolint:wrapcheck
		return err
	}

	return nil
}

func postRunE(cmd *cobra.Command, _ []string) error {
	pg.Close()
	return nil
}

func extractNarHash(path string) (string, error) {
	matches := storePathPattern.FindStringSubmatch(path)
	if len(matches) != 3 {
		return "", fmt.Errorf("could not extract nar hash: %s", path)
	}

	result := matches[1]
	if result == "" {
		result = matches[2]
	}

	return result, nil
}
