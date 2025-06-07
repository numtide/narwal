package awssdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/go-ini/ini"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type CredentialsConfig struct {
	// Direct credentials (highest priority)
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	// AWS credentials file (second priority)
	File string

	// AWS CLI profile (fallback)
	Profile string
}

type AWSCredentials struct {
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
}

func NewCredentials(ctx context.Context, config CredentialsConfig) (*credentials.Credentials, error) {
	// If direct credentials are provided, use them
	if config.AccessKeyID != "" && config.SecretAccessKey != "" {
		return credentials.NewStaticV4(
			config.AccessKeyID,
			config.SecretAccessKey,
			config.SessionToken,
		), nil
	}

	// Try to load from credentials file if specified
	if config.File != "" {
		return loadFromCredentialsFile(config)
	}

	// Fallback to AWS CLI export credentials
	return exportCredentials(ctx, config.Profile)
}

func exportCredentials(ctx context.Context, profile string) (*credentials.Credentials, error) {
	args := []string{"configure", "export-credentials", "--format", "process"}
	if profile != "" {
		args = append(args, "--profile", profile)
	}

	cmd := exec.CommandContext(ctx, "aws", args...)

	output, err := cmd.Output()
	if err != nil {
		// Try to get stderr for better error message
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("export credentials failed: %w (stderr: %s)", err, string(exitErr.Stderr))
		}

		return nil, fmt.Errorf("export credentials failed: %w", err)
	}

	var creds AWSCredentials
	if err := json.Unmarshal(output, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials failed: %w (output: %s)", err, string(output))
	}

	return credentials.NewStaticV4(
		creds.AccessKeyId,
		creds.SecretAccessKey,
		creds.SessionToken,
	), nil
}

// loadFromCredentialsFile loads AWS credentials from a specified credentials file.
func loadFromCredentialsFile(config CredentialsConfig) (*credentials.Credentials, error) {
	// Only load from file if explicitly specified
	if config.File == "" {
		return nil, errors.New("no credentials file specified")
	}

	// Determine profile name
	profile := config.Profile
	if profile == "" {
		profile = "default"
	}

	// Load credentials from credentials file
	accessKeyID, secretAccessKey, sessionToken, err := loadCredentialsFromFile(config.File, profile)
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials from file: %w", err)
	}

	// If we have the required credentials, return them
	if accessKeyID != "" && secretAccessKey != "" {
		return credentials.NewStaticV4(accessKeyID, secretAccessKey, sessionToken), nil
	}

	return nil, errors.New("no valid credentials found in credentials file")
}

// loadCredentialsFromFile loads credentials from an AWS credentials file.
func loadCredentialsFromFile(filePath, profile string) (string, string, string, error) {
	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", "", "", fmt.Errorf("credentials file does not exist: %s", filePath)
	}

	// Load the INI file
	cfg, err := ini.Load(filePath)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse credentials file: %w", err)
	}

	// Get the section for the profile
	section, err := cfg.GetSection(profile)
	if err != nil {
		return "", "", "", fmt.Errorf("profile '%s' not found in credentials file", profile)
	}

	// Extract credentials
	accessKeyID := section.Key("aws_access_key_id").String()
	secretAccessKey := section.Key("aws_secret_access_key").String()
	sessionToken := section.Key("aws_session_token").String()

	// Validate required fields
	if accessKeyID == "" || secretAccessKey == "" {
		return "", "", "", fmt.Errorf("incomplete credentials for profile '%s'", profile)
	}

	return accessKeyID, secretAccessKey, sessionToken, nil
}
