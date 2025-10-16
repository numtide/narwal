package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run an instance of the Data Mesher server",
		RunE:  runE,
	}

	// add http flags to the command
	fs := cmd.Flags()
	config.SetHttpFlags(fs)

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}

func runE(cmd *cobra.Command, _ []string) error {
	// parse viper into our config object
	var cfg *config.Server

	if err := config.FromViper(viper.GetViper(), &cfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())

	// create a server
	srv, err := server.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	ctx := cmd.Context()

	if err = srv.Start(ctx); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// monitor context for cancellation (from SIGINT/SIGTERM handled in main)
	go func() {
		<-ctx.Done()
		log.Info("shutdown signal received")

		// stop the server, waiting up to 30 seconds for it to complete
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		//nolint:contextcheck
		if err := srv.Stop(shutdownCtx); err != nil {
			log.Error("error during server shutdown", "error", err)
		} else {
			log.Info("server shutdown completed")
		}
	}()

	// block until the app context has completed
	<-ctx.Done()

	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("server failure: %w", ctx.Err())
	}

	return nil
}
