package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/workarea"
	"github.com/spf13/pflag"
)

type Inventory struct {
	ReportID     string                   `mapstructure:"report"`
	Prefix       string                   `mapstructure:"prefix"`
	Bucket       string                   `mapstructure:"bucket"`
	BucketRegion string                   `mapstructure:"region"`
	Endpoint     string                   `mapstructure:"endpoint"`
	UseSSL       bool                     `mapstructure:"use_ssl"`
	Credentials  awssdk.CredentialsConfig `mapstructure:"credentials"`
	Workarea     *workarea.WorkArea       `mapstructure:"-"`
}

func (i *Inventory) Validate(ctx context.Context, creds *awssdk.CredentialsConfig, workareaPath string) error {
	var err error

	if i.Bucket == "" {
		return fmt.Errorf("%w: bucket is required", ErrInvalidConfig)
	}

	// Use the credentials from the parameter if provided, otherwise use internal credentials
	var credConfig awssdk.CredentialsConfig
	if creds != nil {
		credConfig = *creds
	} else {
		credConfig = i.Credentials
	}

	if i.BucketRegion == "" {
		// Auto-detect bucket region using awssdk
		awsCreds, err := awssdk.NewCredentials(ctx, credConfig)
		if err != nil {
			return fmt.Errorf("failed to get AWS credentials: %w", err)
		}

		if i.BucketRegion, err = awssdk.DetectBucketRegion(ctx, i.Bucket, awsCreds); err != nil {
			return fmt.Errorf("error getting bucket region: %w", err)
		}

		log.Info("Bucket region auto-detected", "region", i.BucketRegion)
	}

	// Ensure prefix ends with a slash
	if i.Prefix != "" && !strings.HasSuffix(i.Prefix, "/") {
		i.Prefix += "/"
	}

	// Create workarea if workareaPath is specified
	if workareaPath != "" {
		if i.Workarea, err = workarea.New(workareaPath); err != nil {
			return fmt.Errorf("failed to create workarea: %w", err)
		}
	}

	return nil
}

func SetInventoryFlags(fs *pflag.FlagSet) {
	fs.String("bucket", "nix-cache-inventory", "S3 bucket name containing inventory data")
	fs.String("region", "",
		"AWS region for the inventory bucket (e.g. 'us-east-1', 'eu-west-1'). If empty, auto-detects the region")
	fs.String("endpoint", "", "S3 endpoint URL (for custom S3-compatible services like MinIO)")
	fs.Bool("use_ssl", false, "Use SSL when connecting to S3")
	fs.String("prefix", "nix-cache/nix-cache-inventory",
		"Prefix path within the S3 bucket (e.g. 'data/' or 'nix-cache/inventory/')")
	fs.String("report", "",
		"Specific inventory report ID (e.g. '2025-06-03T01-00Z'). Required for get-manifest and download commands")
}
