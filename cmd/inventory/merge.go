package inventory

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/numtide/narwal/pkg/inventory/fuse"
	"github.com/spf13/cobra"
)

func mergeCmd() *cobra.Command {
	var (
		orderBy     string
		memoryLimit string
		threads     int
	)

	cmd := &cobra.Command{
		Use:   "merge <manifest-name> <output-file>",
		Short: "Merge parquet files from a manifest into a single sorted file",
		Long: `Merges all parquet files from an inventory manifest into a single
parquet file, sorted by the specified column. Uses DuckDB for memory-efficient
streaming sort with ZSTD compression.

Requires 'nix' to be available in PATH to run DuckDB.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			manifestName := args[0]
			outputFile := args[1]

			// Ensure Badger config is initialized
			if cfg.Badger == nil {
				return errors.New("badger configuration not found")
			}

			// Open the Badger database
			db, err := inventory.OpenDB(cfg.Badger)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}

			defer func() {
				if closeErr := db.Close(); closeErr != nil {
					log.Errorf("failed to close db: %s", closeErr)
				}
			}()

			// Create temp directory for FUSE mount
			mountpoint, err := os.MkdirTemp("", "narwal-fuse-*")
			if err != nil {
				return fmt.Errorf("failed to create temp mountpoint: %w", err)
			}

			defer func() {
				if removeErr := os.RemoveAll(mountpoint); removeErr != nil {
					log.Errorf("failed to remove temp mountpoint: %s", removeErr)
				}
			}()

			// Mount the FUSE filesystem in background
			server, err := fuse.MountFS(db, mountpoint)
			if err != nil {
				return fmt.Errorf("failed to mount filesystem: %w", err)
			}

			defer func() {
				if unmountErr := server.Unmount(); unmountErr != nil {
					log.Errorf("failed to unmount: %s", unmountErr)
				}
			}()

			log.Info("FUSE filesystem mounted", "mountpoint", mountpoint)

			// Wait for mount to be ready by checking if manifests dir is accessible
			manifestsDir := filepath.Join(mountpoint, "manifests", manifestName)

			for range 50 { // Wait up to 5 seconds
				if _, err := os.Stat(manifestsDir); err == nil {
					break
				}

				time.Sleep(100 * time.Millisecond)
			}

			// Verify manifest directory exists
			if _, err := os.Stat(manifestsDir); os.IsNotExist(err) {
				return fmt.Errorf("manifest directory not found: %s", manifestName)
			}

			// Build DuckDB SQL command
			// SELECT * is intentional - we want all columns from the parquet files
			//nolint:unqueryvet
			duckdbSQL := fmt.Sprintf(`
SET memory_limit = '%s';
SET temp_directory = '%s';
SET threads = %d;
SET preserve_insertion_order = false;
COPY (
    SELECT * FROM read_parquet('%s/*.parquet')
    ORDER BY %s
) TO '%s'
(FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 100000);
`, memoryLimit, mountpoint, threads, manifestsDir, orderBy, outputFile)

			log.Info("running DuckDB merge", "manifest", manifestName, "output", outputFile, "order_by", orderBy)

			// Execute DuckDB via nix
			// The SQL is constructed from user-provided flags which is intentional
			//nolint:gosec
			duckdbCmd := exec.CommandContext(cmd.Context(), "nix", "run", "nixpkgs#duckdb", "--", "-c", duckdbSQL)
			duckdbCmd.Stdout = os.Stdout
			duckdbCmd.Stderr = os.Stderr

			if err := duckdbCmd.Run(); err != nil {
				return fmt.Errorf("duckdb execution failed: %w", err)
			}

			log.Info("merge complete", "output", outputFile)

			return nil
		},
	}

	cmd.Flags().StringVar(&orderBy, "order-by", "key", "Column to order results by")
	cmd.Flags().StringVar(&memoryLimit, "memory-limit", "24GB", "DuckDB memory limit")
	cmd.Flags().IntVar(&threads, "threads", 4, "DuckDB thread count")
	cmd.SilenceUsage = true

	return cmd
}
