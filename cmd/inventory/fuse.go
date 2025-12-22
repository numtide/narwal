package inventory

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/cobrautil"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/inventory/fuse"
	"github.com/spf13/cobra"
)

func fuseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fuse",
		Short: "Mount the inventory database as a FUSE filesystem",
		Long: `Mount the inventory database as a FUSE filesystem to browse manifests and NAR info files.

The filesystem will expose two directories:
- manifests/: Contains all inventory manifest files and their related parquet files
- narinfo/: Contains all NAR info files

Files are mounted read-only. Press Ctrl+C to unmount.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mountpoint := args[0]

			cfg, err := cobrautil.LoadConfig(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Ensure Badger config is initialized
			if cfg.Badger == nil {
				return errors.New("badger configuration not found")
			}

			// Open the Badger database
			db, err := inventory.OpenDB(cfg.Badger, true)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}

			defer func() {
				if closeErr := db.Close(); closeErr != nil {
					log.Errorf("failed to close db: %s", err)
				}
			}()

			// Check if mountpoint exists
			if _, err := os.Stat(mountpoint); os.IsNotExist(err) {
				// Create the mountpoint if it doesn't exist
				if err := os.MkdirAll(mountpoint, 0o750); err != nil {
					return fmt.Errorf("failed to create mountpoint: %w", err)
				}

				log.Info("created mountpoint", "path", mountpoint)
			}

			// Mount the FUSE filesystem
			server, err := fuse.MountFS(db, mountpoint)
			if err != nil {
				return fmt.Errorf("failed to mount filesystem: %w", err)
			}

			log.Info("FUSE filesystem mounted", "mountpoint", mountpoint)
			log.Info("Press Ctrl+C to unmount")

			// Setup signal handling for graceful shutdown
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			// Wait for interrupt signal
			<-sigChan

			log.Info("unmounting filesystem...")

			err = server.Unmount()
			if err != nil {
				return fmt.Errorf("failed to unmount: %w", err)
			}

			log.Info("filesystem unmounted successfully")

			return nil
		},
	}

	config.SetBadgerFlags(cmd.Flags())

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}
