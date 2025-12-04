package awssdk_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
)

func TestNewCredentials_DirectCredentials(t *testing.T) {
	t.Parallel()

	cfg := config.CredentialsConfig{
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
		SessionToken:    "test-session-token",
	}

	creds, err := awssdk.LoadCredentials(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if creds == nil {
		t.Fatal("Expected credentials, got nil")
	}

	// Test that we can retrieve credentials
	value, err := creds.Retrieve(t.Context())
	if err != nil {
		t.Fatalf("Failed to get credential values: %v", err)
	}

	if value.AccessKeyID != "test-access-key" {
		t.Errorf("Expected AccessKeyID 'test-access-key', got '%s'", value.AccessKeyID)
	}

	if value.SecretAccessKey != "test-secret-key" {
		t.Errorf("Expected SecretAccessKey 'test-secret-key', got '%s'", value.SecretAccessKey)
	}

	if value.SessionToken != "test-session-token" {
		t.Errorf("Expected SessionToken 'test-session-token', got '%s'", value.SessionToken)
	}
}

func TestNewCredentials_CredentialsFile(t *testing.T) {
	t.Parallel()

	// Create a temporary credentials file
	//nolint:gosec
	credentialsContent := `[default]
aws_access_key_id = file-access-key
aws_secret_access_key = file-secret-key
aws_session_token = file-session-token

[test-profile]
aws_access_key_id = profile-access-key
aws_secret_access_key = profile-secret-key
`

	tempDir := t.TempDir()
	credentialsFile := filepath.Join(tempDir, "credentials")

	err := os.WriteFile(credentialsFile, []byte(credentialsContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create credentials file: %v", err)
	}

	tests := []struct {
		name           string
		profile        string
		expectedKeyID  string
		expectedSecret string
		expectedToken  string
	}{
		{
			name:           "default profile",
			profile:        "",
			expectedKeyID:  "file-access-key",
			expectedSecret: "file-secret-key",
			expectedToken:  "file-session-token",
		},
		{
			name:           "explicit default profile",
			profile:        "default",
			expectedKeyID:  "file-access-key",
			expectedSecret: "file-secret-key",
			expectedToken:  "file-session-token",
		},
		{
			name:           "test profile",
			profile:        "test-profile",
			expectedKeyID:  "profile-access-key",
			expectedSecret: "profile-secret-key",
			expectedToken:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.CredentialsConfig{
				File:    credentialsFile,
				Profile: tt.profile,
			}

			creds, err := awssdk.LoadCredentials(t.Context(), cfg)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			if creds == nil {
				t.Fatal("Expected credentials, got nil")
			}

			// Test that we can retrieve credentials
			value, err := creds.Retrieve(t.Context())
			if err != nil {
				t.Fatalf("Failed to get credential values: %v", err)
			}

			if value.AccessKeyID != tt.expectedKeyID {
				t.Errorf("Expected AccessKeyID '%s', got '%s'", tt.expectedKeyID, value.AccessKeyID)
			}

			if value.SecretAccessKey != tt.expectedSecret {
				t.Errorf("Expected SecretAccessKey '%s', got '%s'", tt.expectedSecret, value.SecretAccessKey)
			}

			if value.SessionToken != tt.expectedToken {
				t.Errorf("Expected SessionToken '%s', got '%s'", tt.expectedToken, value.SessionToken)
			}
		})
	}
}

func TestNewCredentials_CredentialsFilePriority(t *testing.T) {
	t.Parallel()

	// Create a temporary credentials file
	//nolint:gosec
	credentialsContent := `[default]
aws_access_key_id = file-access-key
aws_secret_access_key = file-secret-key
`

	tempDir := t.TempDir()
	credentialsFile := filepath.Join(tempDir, "credentials")

	err := os.WriteFile(credentialsFile, []byte(credentialsContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create credentials file: %v", err)
	}

	// Direct credentials should take priority over file credentials
	cfg := config.CredentialsConfig{
		AccessKeyID:     "direct-access-key",
		SecretAccessKey: "direct-secret-key",
		File:            credentialsFile,
	}

	creds, err := awssdk.LoadCredentials(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	value, err := creds.Retrieve(t.Context())
	if err != nil {
		t.Fatalf("Failed to get credential values: %v", err)
	}

	// Should use direct credentials, not file credentials
	if value.AccessKeyID != "direct-access-key" {
		t.Errorf("Expected AccessKeyID 'direct-access-key', got '%s'", value.AccessKeyID)
	}

	if value.SecretAccessKey != "direct-secret-key" {
		t.Errorf("Expected SecretAccessKey 'direct-secret-key', got '%s'", value.SecretAccessKey)
	}
}

func TestNewCredentials_CredentialsFileErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		file          string
		profile       string
		fileContent   string
		expectError   bool
		errorContains string
	}{
		{
			name:          "no credentials file specified - falls back to AWS CLI",
			file:          "",
			profile:       "nonexistent-profile", // This will fail at AWS CLI level
			expectError:   true,
			errorContains: "export credentials failed",
		},
		{
			name:          "credentials file does not exist",
			file:          "/nonexistent/credentials",
			expectError:   true,
			errorContains: "credentials file does not exist",
		},
		{
			name:          "profile not found",
			profile:       "nonexistent-profile",
			fileContent:   "[default]\naws_access_key_id = test\naws_secret_access_key = test",
			expectError:   true,
			errorContains: "profile 'nonexistent-profile' not found",
		},
		{
			name:          "incomplete credentials - missing secret key",
			fileContent:   "[default]\naws_access_key_id = test",
			expectError:   true,
			errorContains: "incomplete credentials",
		},
		{
			name:          "incomplete credentials - missing access key",
			fileContent:   "[default]\naws_secret_access_key = test",
			expectError:   true,
			errorContains: "incomplete credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := tt.file

			if tt.fileContent != "" {
				tempDir := t.TempDir()
				file = filepath.Join(tempDir, "credentials")

				err := os.WriteFile(file, []byte(tt.fileContent), 0o600)
				if err != nil {
					t.Fatalf("Failed to create credentials file: %v", err)
				}
			}

			cfg := config.CredentialsConfig{
				File:    file,
				Profile: tt.profile,
			}

			_, err := awssdk.LoadCredentials(t.Context(), cfg)

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got none")
				}

				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorContains, err)
				}
			} else if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestNewCredentials_PartialDirectCredentials(t *testing.T) {
	t.Parallel()

	// Create a temporary credentials file as fallback
	//nolint:gosec
	credentialsContent := `[default]
aws_access_key_id = file-access-key
aws_secret_access_key = file-secret-key
`

	tempDir := t.TempDir()
	credentialsFile := filepath.Join(tempDir, "credentials")

	err := os.WriteFile(credentialsFile, []byte(credentialsContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create credentials file: %v", err)
	}

	// Only provide access key, not secret key - should fall back to file
	cfg := config.CredentialsConfig{
		AccessKeyID: "partial-access-key",
		// SecretAccessKey intentionally omitted
		File: credentialsFile,
	}

	creds, err := awssdk.LoadCredentials(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	value, err := creds.Retrieve(t.Context())
	if err != nil {
		t.Fatalf("Failed to get credential values: %v", err)
	}

	// Should use file credentials since direct credentials are incomplete
	if value.AccessKeyID != "file-access-key" {
		t.Errorf("Expected AccessKeyID 'file-access-key', got '%s'", value.AccessKeyID)
	}

	if value.SecretAccessKey != "file-secret-key" {
		t.Errorf("Expected SecretAccessKey 'file-secret-key', got '%s'", value.SecretAccessKey)
	}
}

func TestNewCredentials_NoCredentialsFile_FallsBackToAWSCLI(t *testing.T) {
	t.Parallel()

	// Test that when no file is specified, it falls back to AWS CLI
	cfg := config.CredentialsConfig{
		Profile: "nonexistent-profile", // This should fail with AWS CLI
	}

	_, err := awssdk.LoadCredentials(t.Context(), cfg)
	// Should get an error from AWS CLI since the profile doesn't exist
	if err == nil {
		t.Error("Expected error when using nonexistent AWS CLI profile, got none")
	}
}
