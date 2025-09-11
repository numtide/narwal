package inventory

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/numtide/narwal/pkg/inventory"
	"github.com/spf13/cobra"
)

func manifestCmd() *cobra.Command {
	var (
		showStats    bool
		outputFormat string
	)

	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Get manifest information for a specific inventory report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report := args[0]

			client := inventory.NewClient(s3, cfg.Inventory.BucketPrefix)

			manifest, err := client.GetManifest(cmd.Context(), report)
			if err != nil {
				return fmt.Errorf("failed to get manifest for report %s: %w", report, err)
			}

			// Display results based on format
			switch {
			case showStats:
				return displayStats(manifest)
			case outputFormat == "json":
				return displayJSON(manifest)
			default:
				return displayTable(manifest)
			}
		},
	}

	// Add command-specific flags
	fs := cmd.Flags()
	fs.BoolVar(&showStats, "stats", false, "Show only statistics summary")
	fs.StringVar(&outputFormat, "format", "table", "Output format: table, json")

	// silence usage on error from this point forward
	cmd.SilenceUsage = true

	return cmd
}

func displayStats(manifest *inventory.Manifest) error {
	totalSize := manifest.TotalSize()

	fmt.Printf("Manifest Statistics for %s:\n", manifest.CreationTime)
	fmt.Printf("  Source Bucket: %s\n", manifest.SourceBucket)
	fmt.Printf("  Destination Bucket: %s\n", manifest.DestBucket)
	fmt.Printf("  File Format: %s\n", manifest.FileFormat)
	fmt.Printf("  File Schema: %s\n", manifest.FileSchema)
	fmt.Printf("  Version: %s\n", manifest.Version)
	fmt.Printf("  Total Files: %d\n", len(manifest.Files))
	fmt.Printf("  Total Size: %s\n", formatBytes(totalSize))

	return nil
}

func displayJSON(manifest *inventory.Manifest) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(manifest.Files); err != nil {
		return fmt.Errorf("error encoding manifest files: %w", err)
	}

	return nil
}

func displayTable(manifest *inventory.Manifest) error {
	totalSize := manifest.TotalSize()

	fmt.Printf("Manifest Information:\n")
	fmt.Printf("  Creation Time: %s\n", manifest.CreationTime)
	fmt.Printf("  Source Bucket: %s\n", manifest.SourceBucket)
	fmt.Printf("  Destination Bucket: %s\n", manifest.DestBucket)
	fmt.Printf("  File Format: %s\n", manifest.FileFormat)
	fmt.Printf("  File Schema: %s\n", manifest.FileSchema)
	fmt.Printf("  Version: %s\n", manifest.Version)
	fmt.Printf("  Total Files: %d\n", len(manifest.Files))
	fmt.Printf("  Total Size: %s\n", formatBytes(totalSize))
	fmt.Printf("\nFiles:\n")

	for i, file := range manifest.Files {
		fmt.Printf("  %d. %s\n", i+1, file.Key)
		fmt.Printf("     Size: %s\n", formatBytes(file.Size))
		fmt.Printf("     MD5: %s\n", file.MD5Checksum)

		if i < len(manifest.Files)-1 {
			fmt.Println()
		}
	}

	return nil
}

// formatBytes formats byte count in human readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
