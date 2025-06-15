//! AWS V4 signature implementation for S3 requests

use chrono::Utc;
use std::collections::BTreeMap;
use std::io;

#[derive(Debug, Clone)]
pub struct AwsCredentials {
    pub access_key_id: String,
    pub secret_access_key: String,
    pub session_token: Option<String>,
    pub region: String,
}

impl AwsCredentials {
    pub async fn load_with_region(region: &str) -> io::Result<Self> {
        use aws_credential_types::provider::ProvideCredentials;

        let config = aws_config::load_defaults(aws_config::BehaviorVersion::latest()).await;
        let credentials_provider = config.credentials_provider().ok_or_else(|| {
            io::Error::new(io::ErrorKind::InvalidInput, "No credentials provider found")
        })?;

        let credentials = credentials_provider
            .provide_credentials()
            .await
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidInput, e.to_string()))?;

        Ok(Self {
            access_key_id: credentials.access_key_id().to_string(),
            secret_access_key: credentials.secret_access_key().to_string(),
            session_token: credentials.session_token().map(|t| t.to_string()),
            region: region.to_string(),
        })
    }

    pub async fn load() -> io::Result<Self> {
        Self::load_with_region("us-east-1").await
    }
}

pub fn create_signed_request(
    path: &str,
    credentials: &AwsCredentials,
    hostname: &str,
) -> io::Result<String> {
    use sha2::{Digest, Sha256};

    let now = Utc::now();
    let host = hostname.to_string();
    let service = "s3";
    let region = &credentials.region;

    let date_str = now.format("%Y%m%dT%H%M%SZ").to_string();
    let date_stamp = now.format("%Y%m%d").to_string();

    // Create canonical headers (including x-amz-content-sha256)
    let payload_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
    let mut headers = BTreeMap::new();
    headers.insert("host", host.as_str());
    headers.insert("x-amz-content-sha256", payload_hash);
    headers.insert("x-amz-date", date_str.as_str());

    if let Some(ref token) = credentials.session_token {
        headers.insert("x-amz-security-token", token);
    }

    let canonical_headers = headers
        .iter()
        .map(|(k, v)| format!("{}:{}", k, v))
        .collect::<Vec<_>>()
        .join("\n")
        + "\n";

    let signed_headers = headers.keys().cloned().collect::<Vec<_>>().join(";");

    // Create canonical request (note: empty query string between path and headers)
    let query_string = ""; // Empty query string for GET request
    let canonical_request = format!(
        "GET\n{}\n{}\n{}\n{}\n{}",
        path, query_string, canonical_headers, signed_headers, payload_hash
    );

    // Debug output for signature components
    tracing::debug!("=== AWS Signature Debug ===");
    tracing::debug!("Path: {}", path);
    tracing::debug!("Query string: '{}'", query_string);
    tracing::debug!(
        "Canonical headers: {}",
        canonical_headers.replace('\n', "\\n")
    );
    tracing::debug!("Signed headers: {}", signed_headers);
    tracing::debug!("Payload hash: {}", payload_hash);
    tracing::debug!(
        "Canonical request: {}",
        canonical_request.replace('\n', "\\n")
    );

    // Validate signature components
    validate_signature_components(
        path,
        query_string,
        &canonical_headers,
        &signed_headers,
        payload_hash,
    )?;

    // Create string to sign
    let credential_scope = format!("{}/{}/{}/aws4_request", date_stamp, region, service);
    let canonical_request_hash = hex::encode(Sha256::digest(canonical_request.as_bytes()));
    let string_to_sign = format!(
        "AWS4-HMAC-SHA256\n{}\n{}\n{}",
        date_str, credential_scope, canonical_request_hash
    );

    tracing::debug!("Credential scope: {}", credential_scope);
    tracing::debug!("Canonical request hash: {}", canonical_request_hash);
    tracing::debug!("String to sign: {}", string_to_sign.replace('\n', "\\n"));

    // Calculate signature
    let k_date = hmac_sha256(
        format!("AWS4{}", credentials.secret_access_key).as_bytes(),
        date_stamp.as_bytes(),
    );
    let k_region = hmac_sha256(&k_date, region.as_bytes());
    let k_service = hmac_sha256(&k_region, service.as_bytes());
    let k_signing = hmac_sha256(&k_service, b"aws4_request");
    let signature = hex::encode(hmac_sha256(&k_signing, string_to_sign.as_bytes()));

    // Create authorization header
    let authorization = format!(
        "AWS4-HMAC-SHA256 Credential={}/{}, SignedHeaders={}, Signature={}",
        credentials.access_key_id, credential_scope, signed_headers, signature
    );

    // Build HTTP request
    let mut request = format!("GET {} HTTP/1.1\r\nHost: {}\r\n", path, &host);

    for (name, value) in &headers {
        if *name != "host" {
            request.push_str(&format!("{}: {}\r\n", name, value));
        }
    }

    request.push_str(&format!("Authorization: {}\r\n", authorization));
    request.push_str("\r\n");

    Ok(request)
}

fn hmac_sha256(key: &[u8], data: &[u8]) -> Vec<u8> {
    use hmac::{Hmac, Mac};
    use sha2::Sha256;
    let mut mac = Hmac::<Sha256>::new_from_slice(key).unwrap();
    mac.update(data);
    mac.finalize().into_bytes().to_vec()
}

/// Validate signature components according to AWS SigV4 specification
fn validate_signature_components(
    path: &str,
    query_string: &str,
    canonical_headers: &str,
    signed_headers: &str,
    payload_hash: &str,
) -> io::Result<()> {
    // Validate path
    if !path.starts_with('/') {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "Path must start with '/'",
        ));
    }

    // Path should be URL-encoded (but our path is already simple ASCII)
    tracing::debug!("✓ Path validation passed");

    // Validate query string (should be empty for our case)
    if !query_string.is_empty() {
        tracing::warn!("Query string is not empty: '{}'", query_string);
    }
    tracing::debug!("✓ Query string validation passed");

    // Validate canonical headers format
    if !canonical_headers.ends_with('\n') {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "Canonical headers must end with newline",
        ));
    }

    // Check for required headers
    if !canonical_headers.contains("host:") {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "Missing required 'host' header",
        ));
    }

    if !canonical_headers.contains("x-amz-date:") {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "Missing required 'x-amz-date' header",
        ));
    }

    tracing::debug!("✓ Canonical headers validation passed");

    // Validate signed headers
    let expected_headers = if canonical_headers.contains("x-amz-security-token:") {
        "host;x-amz-content-sha256;x-amz-date;x-amz-security-token"
    } else {
        "host;x-amz-content-sha256;x-amz-date"
    };

    if signed_headers != expected_headers {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            format!(
                "Signed headers mismatch. Expected: '{}', Got: '{}'",
                expected_headers, signed_headers
            ),
        ));
    }

    tracing::debug!("✓ Signed headers validation passed");

    // Validate payload hash (should be SHA256 of empty string)
    let expected_empty_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
    if payload_hash != expected_empty_hash {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            format!(
                "Invalid payload hash. Expected: '{}', Got: '{}'",
                expected_empty_hash, payload_hash
            ),
        ));
    }

    tracing::debug!("✓ Payload hash validation passed");
    tracing::debug!("✓ All signature component validations passed");

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_hmac_sha256() {
        let key = b"key";
        let data = b"The quick brown fox jumps over the lazy dog";
        let result = hmac_sha256(key, data);
        let expected = "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8";
        assert_eq!(hex::encode(result), expected);
    }

    #[test]
    fn test_signature_with_test_credentials() {
        let creds = AwsCredentials {
            access_key_id: "AKIAIOSFODNN7EXAMPLE".to_string(),
            secret_access_key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY".to_string(),
            session_token: None,
            region: "us-east-1".to_string(),
        };

        let result = create_signed_request("/test.txt", &creds, "nix-cache.s3.amazonaws.com");
        assert!(result.is_ok());

        let request = result.unwrap();
        assert!(request.contains("GET /test.txt HTTP/1.1"));
        assert!(request.contains("Host: nix-cache.s3.amazonaws.com"));
        assert!(request.contains("Authorization: AWS4-HMAC-SHA256"));
    }
}
