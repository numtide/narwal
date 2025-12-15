package inventory

import (
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
)

func exportNarinfoCmd() *cobra.Command {
	var profilePath string

	cmd := &cobra.Command{
		Use:   "export-narinfos <output-file>",
		Short: "Export narinfo entries from badger database to a parquet file",
		Long: `Export all narinfo entries from the badger database to a single parquet file.
Uses ZSTD compression and includes a bloom filter on the hash column for fast lookups.
Progress is logged every 50,000 records.
Supports graceful cancellation with Ctrl+C.

Use --profile to generate a CPU profile for performance analysis.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputPath := args[0]

			cfg, err := loadConfig(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Start CPU profiling if requested
			if profilePath != "" {
				f, err := os.Create(profilePath) //nolint:gosec // user-provided path is intentional
				if err != nil {
					return fmt.Errorf("failed to create profile file: %w", err)
				}

				defer func() {
					if closeErr := f.Close(); closeErr != nil {
						log.Errorf("failed to close profile file: %s", closeErr)
					}
				}()

				if err := pprof.StartCPUProfile(f); err != nil {
					return fmt.Errorf("failed to start CPU profile: %w", err)
				}

				defer pprof.StopCPUProfile()

				log.Info("CPU profiling enabled", "output", profilePath)
			}

			return inventory.ExportNarinfos(cmd.Context(), cfg, outputPath)
		},
	}

	// Set flags
	config.SetBadgerFlags(cmd.Flags())

	cmd.Flags().StringVar(&profilePath, "profile", "", "Write CPU profile to file (use 'go tool pprof' to analyze)")

	// Silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
