package gc

import (
	"errors"
	"fmt"

	"github.com/numtide/narwal/pkg/cobrautil"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/gc"
	"github.com/spf13/cobra"
)

func simpleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "simple <input_file> <output_file>",
		Short: "Run a simple GC",
		Long: `Run a simple garbage collection strategy on the binary cache.

This command reads GC targets from a parquet input file containing NarInfo records,
removes the corresponding narinfo and nar files from S3, and writes the results to
a parquet output file.

The input file should be a parquet file with NarInfo records identifying store paths
to be garbage collected. The output file will contain GC records documenting each
deletion attempt and any errors encountered.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			inputFile := args[0]
			outputFile := args[1]

			cfg, err := cobrautil.LoadConfig(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return fmt.Errorf("failed to get dry-run flag: %w", err)
			}

			strategy := gc.NewSimple(cfg, inputFile, outputFile, dryRun)

			stats, err := strategy.Run(cmd.Context())
			if err != nil {
				return fmt.Errorf("failed to run simple GC strategy: %w", err)
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), stats)
			if err != nil {
				return fmt.Errorf("failed to print GC stats: %w", err)
			}

			// Exit non-zero if there were any fatal errors during GC
			if stats.Removed.Errors > 0 {
				return errors.New("errors during GC")
			}

			// Exit non-zero if any store paths were missing in S3
			if stats.TotalMissingTargets() > 0 {
				return errors.New("missing store paths during GC")
			}

			return nil
		},
	}

	fs := cmd.Flags()

	fs.Bool("dry-run", false, "Perform a dry run without actually deleting anything")

	config.SetAWSFlags(fs)
	config.SetS3Flags(fs)

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
