package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCmd() *cobra.Command {
	// create a viper instance for reading in config
	v, err := config.NewViper()
	if err != nil {
		cobra.CheckErr(fmt.Errorf("failed to create viper instance: %w", err))
	}

	// create the command
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run an instance of the Data Mesher server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runE(v, cmd, args)
		},
	}

	// add our config flags to the command's flag set
	fs := cmd.Flags()

	config.SetFlags(fs)

	// add a config-file flag
	fs.String(
		"config-file", "",
		"Load the config file from the given path",
	)

	// add a log level flag
	fs.String("log_level", "info", "Log level (warn, info, debug)")

	// bind our command's flags to viper
	if err := v.BindPFlags(fs); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind global config to viper: %w", err))
	}

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}

func runE(v *viper.Viper, cmd *cobra.Command, _ []string) error {
	// set config file path in viper based on the config flag
	configFile, err := cmd.Flags().GetString("config-file")
	if err != nil {
		return errors.New("failed to parse config-file flag")
	}

	v.SetConfigFile(configFile)

	// read in config
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// configure logging
	logLevel, err := log.ParseLevel(v.GetString("log_level"))
	if err != nil {
		return fmt.Errorf("failed to parse log level: %w", err)
	}

	log.SetLevel(logLevel)
	log.SetReportTimestamp(true)

	// parse viper into our config object
	cfg, err := config.FromViper(v)
	if err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	log.Info("config loaded", "config_file", v.ConfigFileUsed())

	// create a server
	srv, err := server.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	ctx, cancel := context.WithCancelCause(cmd.Context())
	defer cancel(nil)

	if err = srv.Start(ctx); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	go func() {
		// listen for shutdown signal
		exit := make(chan os.Signal, 1)
		signal.Notify(exit, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-exit

		log.Info("shutdown signal received")

		// stop the server, waiting up to 30 seconds for it to complete
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		err = srv.Stop(shutdownCtx)

		cancel(err)
	}()

	// block until the app context has completed
	<-ctx.Done()

	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("server failure: %w", ctx.Err())
	}

	return nil
}
