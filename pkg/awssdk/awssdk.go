package awssdk

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/numtide/narwal/pkg/config"
)

// defaultRegion is the default AWS region used when none is specified,
// particularly for custom endpoints like LocalStack or MinIO.
const defaultRegion = "us-east-1"

func LoadSDKConfig(ctx context.Context, cfg *config.AWS) (*aws.Config, error) {
	// Load credentials
	creds, err := LoadCredentials(ctx, cfg.Credentials)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS credentials: %w", err)
	}

	var (
		region   string
		useSSL   bool
		endpoint string
	)

	// Determine endpoint mode
	if cfg.Endpoint != "" {
		// Custom endpoint mode (LocalStack, etc.)
		endpoint = cfg.Endpoint
		useSSL = cfg.UseSSL

		region = cfg.Region
		if region == "" {
			region = defaultRegion
		}
	} else {
		// AWS SQS mode
		useSSL = true
		region = cfg.Region

		if region == "" {
			return nil, errors.New("region is required for AWS SQS")
		}
	}

	// Create a custom HTTP client optimised for high-volume operations
	httpClient := &http.Client{
		Transport: createHTTPTransport(useSSL),
	}

	// Build config options
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
		awsconfig.WithHTTPClient(httpClient),
		awsconfig.WithCredentialsProvider(creds),
	}

	// Only set base endpoint for custom endpoints (LocalStack, MinIO, etc.)
	if endpoint != "" {
		scheme := "http"
		if useSSL {
			scheme = "https"
		}

		opts = append(opts, awsconfig.WithBaseEndpoint(fmt.Sprintf("%s://%s", scheme, endpoint)))
	}

	// Load AWS config
	result, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &result, nil
}

// createHTTPTransport creates an HTTP transport optimized for S3 operations.
func createHTTPTransport(useSSL bool) *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   2048, // High limit for concurrent narinfo downloads
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if useSSL {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	return transport
}

// GetObject retrieves an object from the bucket.
func (bc *BucketClient) GetObject(
	ctx context.Context,
	key string,
) (*s3.GetObjectOutput, error) {
	output, err := bc.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bc.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get object %s: %w", key, err)
	}

	return output, nil
}
