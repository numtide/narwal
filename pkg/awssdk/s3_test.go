package awssdk_test

import (
	"strings"
	"testing"

	"github.com/numtide/narwal/pkg/awssdk"
)

func TestBucketConfig_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      awssdk.BucketConfig
		credentials awssdk.CredentialsConfig
		wantError   bool
		errorMsg    string
	}{
		{
			name: "valid bucket config with explicit region",
			config: awssdk.BucketConfig{
				Bucket: "test-bucket",
				Region: "us-west-2", // Explicit region to avoid AWS CLI call
				UseSSL: true,
			},
			credentials: awssdk.CredentialsConfig{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			wantError: false,
		},
		{
			name: "valid endpoint config",
			config: awssdk.BucketConfig{
				Bucket:   "test-bucket",
				Endpoint: "localhost:9000",
				UseSSL:   false,
			},
			credentials: awssdk.CredentialsConfig{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			wantError: false,
		},
		{
			name: "both region and endpoint specified",
			config: awssdk.BucketConfig{
				Bucket:   "test-bucket",
				Region:   "us-west-2",
				Endpoint: "localhost:9000",
				UseSSL:   true,
			},
			credentials: awssdk.CredentialsConfig{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			wantError: true,
			errorMsg:  "cannot specify both Region and Endpoint",
		},
		{
			name: "neither region nor endpoint specified - should auto-detect",
			config: awssdk.BucketConfig{
				Bucket: "test-bucket",
				UseSSL: true,
			},
			credentials: awssdk.CredentialsConfig{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			wantError: true, // Will fail because test credentials can't access real AWS
			errorMsg:  "failed to detect bucket region",
		},
		{
			name: "empty bucket name",
			config: awssdk.BucketConfig{
				Region: "us-west-2",
				UseSSL: true,
			},
			credentials: awssdk.CredentialsConfig{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			wantError: true,
			errorMsg:  "bucket name is required",
		},
		{
			name: "nil credentials",
			config: awssdk.BucketConfig{
				Bucket: "test-bucket",
				Region: "us-west-2",
				UseSSL: true,
			},
			credentials: awssdk.CredentialsConfig{
				File: "/nonexistent/path/credentials",
			},
			wantError: true,
			errorMsg:  "credentials file does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create credentials first
			creds, credErr := awssdk.NewCredentials(t.Context(), tt.credentials)
			if credErr != nil {
				// If we expect an error and credentials creation fails, check if it's the expected error
				if tt.wantError && strings.Contains(credErr.Error(), tt.errorMsg) {
					return // This is the expected error
				}
				// If we don't expect an error, this is a failure
				if !tt.wantError {
					t.Fatalf("Failed to create credentials: %v", credErr)
				}
				// If we do expect an error but it's not the right message, log and continue to see if bucket client fails
				t.Logf("Got credential error: %v", credErr)

				return
			}

			_, err := awssdk.NewBucketClient(t.Context(), tt.config, creds)

			if tt.wantError {
				if err == nil {
					t.Errorf("NewBucketClient() expected error but got none")
					return
				}

				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("NewBucketClient() error = %v, want error containing %v", err, tt.errorMsg)
				}
			} else if err != nil {
				t.Errorf("NewBucketClient() unexpected error = %v", err)
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

func TestBucketConfig_AWSMode(t *testing.T) {
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

			creds, err := awssdk.NewCredentials(t.Context(), awssdk.CredentialsConfig{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			})
			if err != nil {
				t.Fatalf("Failed to create credentials: %v", err)
			}

			config := awssdk.BucketConfig{
				Bucket: tt.bucket,
				Region: tt.region,
				UseSSL: true,
			}

			if tt.skipRegionTest {
				t.Skip("Skipping test that requires AWS CLI region detection")
			}

			client, err := awssdk.NewBucketClient(t.Context(), config, creds)
			if err != nil {
				t.Fatalf("NewBucketClient() unexpected error = %v", err)
			}

			if client == nil {
				t.Fatal("NewBucketClient() returned nil client")
			}
		})
	}
}

func TestBucketConfig_CustomEndpoint(t *testing.T) {
	t.Parallel()

	creds, err := awssdk.NewCredentials(t.Context(), awssdk.CredentialsConfig{
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
	})
	if err != nil {
		t.Fatalf("Failed to create credentials: %v", err)
	}

	config := awssdk.BucketConfig{
		Bucket:   "test-bucket",
		Endpoint: "localhost:9000",
		UseSSL:   false,
	}

	client, err := awssdk.NewBucketClient(t.Context(), config, creds)
	if err != nil {
		t.Fatalf("Failed to create credentials: %v", err)
	}

	if client == nil {
		t.Fatal("NewBucketClient() returned nil client")
	}
}
