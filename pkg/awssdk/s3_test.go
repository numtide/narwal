package awssdk_test

import (
	"strings"
	"testing"

	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
)

func TestNewS3Client_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		awsCfg    *config.AWS
		s3Cfg     *config.S3
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid config with explicit region",
			awsCfg: &config.AWS{
				Region: "us-west-2",
				Credentials: config.CredentialsConfig{
					AccessKeyID:     "test-key",
					SecretAccessKey: "test-secret",
				},
			},
			s3Cfg: &config.S3{
				Bucket: "test-bucket",
			},
			wantError: false,
		},
		{
			name: "valid endpoint config",
			awsCfg: &config.AWS{
				Endpoint: "localhost:9000",
				UseSSL:   false,
				Credentials: config.CredentialsConfig{
					AccessKeyID:     "test-key",
					SecretAccessKey: "test-secret",
				},
			},
			s3Cfg: &config.S3{
				Bucket: "test-bucket",
			},
			wantError: false,
		},
		{
			name: "neither region nor endpoint specified - requires region",
			awsCfg: &config.AWS{
				Credentials: config.CredentialsConfig{
					AccessKeyID:     "test-key",
					SecretAccessKey: "test-secret",
				},
			},
			s3Cfg: &config.S3{
				Bucket: "test-bucket",
			},
			wantError: true,
			errorMsg:  "region is required",
		},
		{
			name: "invalid credentials file",
			awsCfg: &config.AWS{
				Region: "us-west-2",
				Credentials: config.CredentialsConfig{
					File: "/nonexistent/path/credentials",
				},
			},
			s3Cfg: &config.S3{
				Bucket: "test-bucket",
			},
			wantError: true,
			errorMsg:  "credentials file does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := awssdk.NewBucketClientFromConfig(t.Context(), tt.awsCfg, tt.s3Cfg)

			if tt.wantError {
				if err == nil {
					t.Errorf("NewBucketClientFromConfig() expected error but got none")
					return
				}

				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("NewBucketClientFromConfig() error = %v, want error containing %v", err, tt.errorMsg)
				}
			} else if err != nil {
				t.Errorf("NewBucketClientFromConfig() unexpected error = %v", err)
			}
		})
	}
}

func TestDetectBucketRegion_MockAWSCLI(t *testing.T) {
	t.Parallel()
	// Note: This test would require mocking the AWS CLI execution
	// For now, we'll skip it in CI environments where AWS CLI might not be available
	t.Skip("Skipping test that requires AWS CLI - implement with mock in future")
}

func TestNewS3Client_AWSMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		bucket         string
		region         string
		expectedRegion string
		skipRegionTest bool
	}{
		{
			name:           "explicit region",
			bucket:         "test-bucket",
			region:         "us-west-2",
			expectedRegion: "us-west-2",
		},
		{
			name:           "auto-detect region",
			bucket:         "test-bucket",
			region:         "",
			skipRegionTest: true, // Skip because it requires actual AWS CLI call
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.skipRegionTest {
				t.Skip("Skipping test that requires AWS CLI region detection")
			}

			awsCfg := &config.AWS{
				Region: tt.region,
				Credentials: config.CredentialsConfig{
					AccessKeyID:     "test-key",
					SecretAccessKey: "test-secret",
				},
			}
			s3Cfg := &config.S3{
				Bucket: tt.bucket,
			}

			client, err := awssdk.NewBucketClientFromConfig(t.Context(), awsCfg, s3Cfg)
			if err != nil {
				t.Fatalf("NewBucketClientFromConfig() unexpected error = %v", err)
			}

			if client == nil {
				t.Fatal("NewBucketClientFromConfig() returned nil client")
			}
		})
	}
}

func TestNewS3Client_CustomEndpoint(t *testing.T) {
	t.Parallel()

	awsCfg := &config.AWS{
		Endpoint: "localhost:9000",
		UseSSL:   false,
		Credentials: config.CredentialsConfig{
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		},
	}
	s3Cfg := &config.S3{
		Bucket: "test-bucket",
	}

	client, err := awssdk.NewBucketClientFromConfig(t.Context(), awsCfg, s3Cfg)
	if err != nil {
		t.Fatalf("NewBucketClientFromConfig() unexpected error = %v", err)
	}

	if client == nil {
		t.Fatal("NewBucketClientFromConfig() returned nil client")
	}
}
