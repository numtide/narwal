package cmd

import (
	"github.com/numtide/nix-binary-cache/cmd/server"
	"github.com/numtide/nix-binary-cache/pkg/build"
	"github.com/spf13/cobra"
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
	cmd.AddCommand(server.NewCmd())

	return cmd
}
