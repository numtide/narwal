package gc

import (
	"fmt"
	"regexp"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//nolint:gochecknoglobals
var (
	cfg *config.GC

	// s3 client will be used when we implement deletions
	//nolint:unused
	s3 *minio.Client
	pg *pgxpool.Pool

	storePathPattern = regexp.MustCompile(`^([a-z0-9]{32})|/nix/store/([a-z0-9]{32})-.*$`)
)

func NewCmd() *cobra.Command {
	// create the command
	cmd := &cobra.Command{
		Use:                "gc",
		Short:              "Set GC roots and run garbage collection",
		PersistentPreRunE:  preRunE,
		PersistentPostRunE: postRunE,
	}

	// bind our command's flags to viper
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	cmd.AddCommand(root())
	cmd.AddCommand(plan())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

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
	pg, err = cfg.Postgres.Connect(cmd.Context())
	if err != nil {
		//nolint:wrapcheck
		return err
	}

	// connect to s3
	if s3, err = cfg.S3.Connect(); err != nil {
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
