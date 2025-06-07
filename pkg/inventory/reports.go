package inventory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/minio/minio-go/v7"
)

// ListReports returns a list of available inventory reports, ordered lexicographically.
func (c *Client) ListReports(ctx context.Context) ([]string, error) {
	log.Debug("Listing inventory reports", "bucket", c.bucketClient.BucketName(), "prefix", c.prefix)

	var reports []string

	// List objects with non-recursive mode to get directory-like structure
	// This will only list objects/prefixes at the current level, simulating directory listing
	opts := minio.ListObjectsOptions{
		Prefix:    c.prefix,
		Recursive: false, // This effectively acts like using a delimiter
	}

	// Use a map to track unique directory prefixes we've seen
	seenPrefixes := make(map[string]bool)

	for object := range c.bucketClient.ListObjects(ctx, opts) {
		if object.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", object.Err)
		}

		// Extract the directory part after our prefix
		if strings.HasPrefix(object.Key, c.prefix) {
			remainder := strings.TrimPrefix(object.Key, c.prefix)

			// Find the first slash to identify the directory
			if slashIndex := strings.Index(remainder, "/"); slashIndex >= 0 {
				reportDir := remainder[:slashIndex]

				// Basic validation that this looks like a report directory
				if len(reportDir) > 0 && strings.Contains(reportDir, "T") && !seenPrefixes[reportDir] {
					reports = append(reports, reportDir)
					seenPrefixes[reportDir] = true
				}
			}
		}
	}

	// Sort lexicographically (which works for ISO 8601 date format)
	sort.Strings(reports)

	log.Debug("Found inventory reports", "count", len(reports), "reports", reports)

	return reports, nil
}
