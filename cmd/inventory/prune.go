package inventory

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/dustin/go-humanize"
	"github.com/numtide/narwal/pkg/cobrautil"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
)

func pruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune <report-ids>",
		Short: "Remove manifests not in the provided list",
		Long: `Remove manifests and their associated files from the local database
that are not in the provided comma-separated list of report IDs.

Example:
  narwal inventory prune 2024-01-01T00-00Z,2024-01-02T00-00Z`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse comma-separated report IDs
			reportIDs := strings.Split(args[0], ",")

			// Trim whitespace from each ID
			for i, id := range reportIDs {
				reportIDs[i] = strings.TrimSpace(id)
			}

			// Filter out empty strings
			var keepReports []string

			for _, id := range reportIDs {
				if id != "" {
					keepReports = append(keepReports, id)
				}
			}

			if len(keepReports) == 0 {
				return errors.New("at least one report ID must be provided")
			}

			log.Infof("keeping %d reports: %v", len(keepReports), keepReports)

			cfg, err := cobrautil.LoadConfig(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			result, err := inventory.Prune(cfg.Badger, keepReports)
			if err != nil {
				return fmt.Errorf("failed to prune: %w", err)
			}

			log.Infof("pruned %d manifests and %d files", result.ManifestsDeleted, result.FilesDeleted)

			reclaimed := max(0, result.VlogSizeBefore-result.VlogSizeAfter)
			log.Infof("vlog: %s -> %s (reclaimed %s in %d GC cycles)",
				humanize.Bytes(uint64(max(0, result.VlogSizeBefore))), //nolint:gosec
				humanize.Bytes(uint64(max(0, result.VlogSizeAfter))),  //nolint:gosec
				humanize.Bytes(uint64(reclaimed)),                     //nolint:gosec
				result.GCRuns)

			return nil
		},
	}

	config.SetBadgerFlags(cmd.Flags())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
