package server

import (
	"fmt"

	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCmd() *cobra.Command {
	// create the command
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Server management commands",
	}

	// add our config flags to the command's persistent flag set
	// so they're available to all subcommands
	fs := cmd.PersistentFlags()
	config.SetS3Flags(fs)
	config.SetPostgresFlags(fs)

	// bind our command's flags to viper
	if err := viper.BindPFlags(fs); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind flags to viper: %w", err))
	}

	// add subcommands
	cmd.AddCommand(runCmd())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
