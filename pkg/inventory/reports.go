package inventory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
)

// GetReports returns a list of available inventory reports, ordered lexicographically.
func (c *Client) GetReports(ctx context.Context) ([]string, error) {
	log.Debug("Listing inventory reports", "bucket", c.bucket, "prefix", c.prefix)

	var reports []string

	paginator := s3.NewListObjectsV2Paginator(c.s3Client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(c.bucket),
		Prefix:    aws.String(c.prefix),
		Delimiter: aws.String("/"),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		// Extract report directories from common prefixes
		for _, commonPrefix := range page.CommonPrefixes {
			if commonPrefix.Prefix == nil {
				continue
			}

			// Extract the report ID part from the prefix
			// Example: "nix-cache/nix-cache-inventory/2025-06-03T01-00Z/" -> "2025-06-03T01-00Z"
			prefixStr := *commonPrefix.Prefix
			if strings.HasPrefix(prefixStr, c.prefix) {
				reportDir := strings.TrimPrefix(prefixStr, c.prefix)
				reportDir = strings.TrimSuffix(reportDir, "/")

				// Basic validation that this looks like a report directory
				if len(reportDir) > 0 && strings.Contains(reportDir, "T") {
					reports = append(reports, reportDir)
				}
			}
		}
	}

	// Sort lexicographically (which works for ISO 8601 date format)
	sort.Strings(reports)

	log.Debug("Found inventory reports", "count", len(reports), "reports", reports)

	return reports, nil
}
