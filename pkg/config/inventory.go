package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/workarea"
	"github.com/spf13/pflag"
)

type Inventory struct {
	ReportID     string             `mapstructure:"report"`
	Prefix       string             `mapstructure:"prefix"`
	Bucket       string             `mapstructure:"bucket"`
	BucketRegion string             `mapstructure:"region"`
	Workdir      string             `mapstructure:"workdir"`
	Workarea     *workarea.WorkArea `mapstructure:"-"`
}

func (i *Inventory) Validate(ctx context.Context, awsCfg aws.Config) error {
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

	// Create workarea if workdir is specified
	if i.Workdir != "" {
		if i.Workarea, err = workarea.New(i.Workdir); err != nil {
			return fmt.Errorf("failed to create workarea: %w", err)
		}
	}

	return nil
}

func SetInventoryFlags(fs *pflag.FlagSet) {
	fs.String("bucket", "nix-cache-inventory", "S3 bucket name containing inventory data")
	fs.String("region", "",
		"AWS region for the inventory bucket (e.g. 'us-east-1', 'eu-west-1'). If empty, auto-detects the region")
	fs.String("prefix", "nix-cache/nix-cache-inventory",
		"Prefix path within the S3 bucket (e.g. 'data/' or 'nix-cache/inventory/')")
	fs.String("report", "",
		"Specific inventory report ID (e.g. '2025-06-03T01-00Z'). Required for get-manifest and download commands")
	fs.String("workdir", "./work", "Local directory to cache files and manifests (reused across runs for efficiency)")
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
