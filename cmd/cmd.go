package cmd

import (
	"fmt"

	"github.com/numtide/narwal/cmd/inventory"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/build"
	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configFile  string //nolint:gochecknoglobals
	logLevelStr string //nolint:gochecknoglobals
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:     build.Name,
		Short:   "Nix Binary Cache",
		Version: build.Version,
	}

	// update version template
	cmd.SetVersionTemplate(build.Name + " " + "{{.Version}}")

	// add subcommands
	cmd.AddCommand(inventory.NewCmd())

	// add some flags common to all subcommands
	fs := cmd.PersistentFlags()

	// add a config-file flag
	fs.StringVar(&configFile, "config-file", "", "Load the config file from the given path")

	// add a log level flag
	fs.StringVar(&logLevelStr, "log_level", "warn", "Log level (warn, info, debug)")

	// configure viper
	err := config.ConfigureViper(viper.GetViper())
	if err != nil {
		cobra.CheckErr(fmt.Errorf("failed to create viper instance: %w", err))
	}

	// bind our command's flags to viper
	err = viper.BindPFlags(fs)
	if err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	cobra.OnInitialize(initConfig)

	return cmd
}

func initConfig() {
	viper.SetConfigFile(configFile)

	// read in config
	if err := viper.ReadInConfig(); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to read config file: %w", err))
	}

	// configure logging
	logLevel, err := log.ParseLevel(logLevelStr)
	if err != nil {
		cobra.CheckErr(fmt.Errorf("failed to parse log level: %w", err))
	}

	log.SetLevel(logLevel)
	log.SetReportTimestamp(true)
}
