package awssdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/go-ini/ini"
	"github.com/numtide/narwal/pkg/config"
)

type awsCredentials struct {
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
}

//nolint:ireturn // AWS SDK uses CredentialsProvider interface
func LoadCredentials(ctx context.Context, cfg config.CredentialsConfig) (aws.CredentialsProvider, error) {
	// If direct credentials are provided, use them
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		return credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			cfg.SessionToken,
		), nil
	}

	// Try to load from credentials file if specified
	if cfg.File != "" {
		return loadFromCredentialsFile(cfg)
	}

	// Fallback to AWS CLI export credentials
	return exportCredentials(ctx, cfg.Profile)
}

//nolint:ireturn // AWS SDK uses CredentialsProvider interface
func exportCredentials(ctx context.Context, profile string) (aws.CredentialsProvider, error) {
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

	var creds awsCredentials
	if err := json.Unmarshal(output, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials failed: %w (output: %s)", err, string(output))
	}

	return credentials.NewStaticCredentialsProvider(
		creds.AccessKeyId,
		creds.SecretAccessKey,
		creds.SessionToken,
	), nil
}

// loadFromCredentialsFile loads AWS credentials from a specified credentials file.
//
//nolint:ireturn // AWS SDK uses CredentialsProvider interface
func loadFromCredentialsFile(cfg config.CredentialsConfig) (aws.CredentialsProvider, error) {
	// Only load from file if explicitly specified
	if cfg.File == "" {
		return nil, errors.New("no credentials file specified")
	}

	// Determine profile name
	profile := cfg.Profile
	if profile == "" {
		profile = "default"
	}

	// Load credentials from credentials file
	accessKeyID, secretAccessKey, sessionToken, err := loadCredentialsFromFile(cfg.File, profile)
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials from file: %w", err)
	}

	// If we have the required credentials, return them
	if accessKeyID != "" && secretAccessKey != "" {
		return credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, sessionToken), nil
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
