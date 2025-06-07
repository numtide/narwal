// Package awssdk provides a lightweight replacement for the AWS SDK for Go.
//
// This package offers simplified AWS credential management and S3 client creation
// without the overhead of the full AWS SDK. It supports both AWS S3 and
// S3-compatible services like MinIO through a unified interface.
//
// This package depends on the `aws` CLI binary to get `aws sso` credentials
// and deduce bucket regions.
package awssdk
