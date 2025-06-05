package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	"github.com/spf13/pflag"
)

type Importer struct {
	Date           string `mapstructure:"date"`
	Prefix         string `mapstructure:"prefix"`
	Bucket         string `mapstructure:"bucket"`
	BucketRegion   string `mapstructure:"region"`
	Workdir        string `mapstructure:"workdir"`
	SkipProcessing bool   `mapstructure:"skip-processing"`
}

func (i *Importer) Validate(ctx context.Context, awsCfg aws.Config) error {
	var err error

	if i.Bucket == "" {
		return fmt.Errorf("%w: bucket is required", ErrInvalidConfig)
	}

	if i.BucketRegion == "" {
		// Create an initial S3 client to determine the bucket location
		s3Client := s3.NewFromConfig(awsCfg)

		// Auto-detect bucket region
		if i.BucketRegion, err = getBucketRegion(ctx, s3Client, i.Bucket); err != nil {
			return fmt.Errorf("error getting bucket region: %w", err)
		}

		log.Info("Bucket region auto-detected", "region", i.BucketRegion)
	}

	// Ensure prefix ends with a slash
	if i.Prefix != "" && !strings.HasSuffix(i.Prefix, "/") {
		i.Prefix += "/"
	}

	return nil
}

func SetImporterFlags(fs *pflag.FlagSet) {
	fs.String("bucket", "nix-cache-inventory", "S3 bucket name to import data from")
	fs.String("region", "",
		"AWS region for the inventory bucket (e.g. 'us-east-1', 'eu-west-1'). If empty, auto-detects the region")
	fs.String("prefix", "nix-cache/nix-cache-inventory",
		"Prefix path within the S3 bucket (e.g. 'data/' or 'nix-cache/inventory/')")
	fs.String("date", "2025-06-03T01-00Z",
		"Specific inventory date to process (e.g. '2025-06-03T01-00Z'). If empty, uses the latest available date")
	fs.String("workdir", "./work", "Local directory to cache parquet files (reused across runs for efficiency)")
	fs.Bool("skip-processing", false, "Skip processing parquet file contents, only download files")
}

// getBucketRegion gets the AWS region where the bucket is located.
func getBucketRegion(ctx context.Context, client *s3.Client, bucket string) (string, error) {
	log.Info("Determining bucket region", "bucket", bucket)

	result, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get bucket location: %w", err)
	}

	// Convert the region enum to a string
	var region string

	switch string(result.LocationConstraint) {
	case "EU":
		region = "eu-west-1"
	case "":
		region = "us-east-1"
	default:
		region = string(result.LocationConstraint)
	}

	log.Info("Successfully determined bucket region", "bucket", bucket, "region", region)

	return region, nil
}
