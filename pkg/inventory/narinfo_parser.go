package inventory

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
)

// parseNarinfoToRecord parses a narinfo byte slice directly into a NarInfoRecord.
// This is a lightweight parser optimized for export, avoiding the allocations
// of the go-nix parser (no bufio.Scanner, no intermediate NarInfo struct).
func parseNarinfoToRecord(data []byte) (NarInfoRecord, error) {
	var record NarInfoRecord

	// Pre-scan to count references for pre-allocation
	refCount := countReferences(data)
	if refCount > 0 {
		record.References = make([][20]byte, 0, refCount)
	}

	// Parse line by line without allocating a scanner
	for len(data) > 0 {
		// Find end of line
		lineEnd := bytes.IndexByte(data, '\n')

		var line []byte

		if lineEnd == -1 {
			line = data
			data = nil
		} else {
			line = data[:lineEnd]
			data = data[lineEnd+1:]
		}

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		// Handle Windows line endings
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}

		// Find ": " separator
		sepIdx := bytes.Index(line, []byte(": "))
		if sepIdx == -1 {
			continue // Skip malformed lines
		}

		key := line[:sepIdx]
		value := line[sepIdx+2:]

		if err := parseNarinfoField(&record, key, value); err != nil {
			return NarInfoRecord{}, err
		}
	}

	// Default compression if not specified
	if record.Compression == "" {
		record.Compression = "bzip2"
	}

	return record, nil
}

// countReferences quickly counts space-separated references in the data.
func countReferences(data []byte) int {
	// Find "References: " line
	refPrefix := []byte("References: ")
	idx := bytes.Index(data, refPrefix)

	if idx == -1 {
		return 0
	}

	// Find end of line
	start := idx + len(refPrefix)
	end := bytes.IndexByte(data[start:], '\n')

	var refLine []byte
	if end == -1 {
		refLine = data[start:]
	} else {
		refLine = data[start : start+end]
	}

	if len(refLine) == 0 {
		return 0
	}

	// Count spaces + 1 = number of references
	count := 1

	for _, b := range refLine {
		if b == ' ' {
			count++
		}
	}

	return count
}

// parseNarinfoField parses a single narinfo field into the record.
//
//nolint:cyclop
func parseNarinfoField(record *NarInfoRecord, key, value []byte) error {
	// Fast path: check first byte to quickly dispatch
	if len(key) == 0 {
		return nil
	}

	switch key[0] {
	case 'S':
		if bytes.Equal(key, []byte("StorePath")) {
			return parseStorePath2(record, value)
		}

		if bytes.Equal(key, []byte("Sig")) {
			return parseSignature(record, value)
		}

		if bytes.Equal(key, []byte("System")) {
			record.System = string(value)
			return nil
		}

	case 'C':
		if bytes.Equal(key, []byte("Compression")) {
			record.Compression = string(value)
			return nil
		}

		if bytes.Equal(key, []byte("CA")) {
			return parseCA(record, value)
		}

	case 'F':
		if bytes.Equal(key, []byte("FileHash")) {
			return parseSHA256Hash(value, &record.FileHash, "FileHash")
		}

		if bytes.Equal(key, []byte("FileSize")) {
			size, err := strconv.ParseUint(string(value), 10, 64)
			if err != nil {
				return fmt.Errorf("invalid FileSize: %w", err)
			}

			record.FileSize = size

			return nil
		}

	case 'N':
		if bytes.Equal(key, []byte("NarHash")) {
			return parseSHA256Hash(value, &record.NarHash, "NarHash")
		}

		if bytes.Equal(key, []byte("NarSize")) {
			size, err := strconv.ParseUint(string(value), 10, 64)
			if err != nil {
				return fmt.Errorf("invalid NarSize: %w", err)
			}

			record.NarSize = size

			return nil
		}

	case 'R':
		if bytes.Equal(key, []byte("References")) {
			return parseReferences(record, value)
		}

	case 'D':
		if bytes.Equal(key, []byte("Deriver")) {
			return parseDeriver(record, value)
		}
	}

	// Unknown keys are ignored (unlike go-nix which errors)
	return nil
}

// parseStorePath2 parses the StorePath field and extracts hash and pname into the record.
func parseStorePath2(record *NarInfoRecord, value []byte) error {
	// Format: /nix/store/<32-char-hash>-<pname>
	// Minimum length: 11 (prefix) + 32 (hash) + 1 (-) + 1 (pname) = 45
	const (
		prefixLen = 11 // len("/nix/store/")
		hashLen   = 32 // nixbase32 encoded hash
		minLen    = 45 // prefix + hash + "-" + at least 1 char pname
		hashEnd   = prefixLen + hashLen
	)

	if len(value) < minLen {
		return fmt.Errorf("store path too short: %s", value)
	}

	// Verify prefix
	if !bytes.Equal(value[:prefixLen], []byte("/nix/store/")) {
		return fmt.Errorf("invalid store path prefix: %s", value)
	}

	// Verify separator
	if value[hashEnd] != '-' {
		return fmt.Errorf("invalid store path format: %s", value)
	}

	// Decode nixbase32 hash to binary (32 chars -> 20 bytes)
	if err := decodeNixbase32Into(&record.Hash, value[prefixLen:hashEnd]); err != nil {
		return fmt.Errorf("invalid store path hash: %w", err)
	}

	record.Pname = string(value[hashEnd+1:])

	return nil
}

// parseReferences parses space-separated reference hashes.
// References are sorted by hash to improve parquet compression.
// Sets QuirkReferencesOutOfOrder if the original order wasn't canonical
// (lexicographic by full basename).
func parseReferences(record *NarInfoRecord, value []byte) error {
	if len(value) == 0 {
		return nil
	}

	// Track previous reference for order checking
	var prevRef []byte

	// Parse space-separated references
	// Format: <32-char-hash>-<pname> <32-char-hash>-<pname> ...
	for len(value) > 0 {
		// Find end of this reference (space or end of value)
		refEnd := bytes.IndexByte(value, ' ')

		var ref []byte
		if refEnd == -1 {
			ref = value
			value = nil
		} else {
			ref = value[:refEnd]
			value = value[refEnd+1:]
		}

		// Skip empty refs (multiple spaces)
		if len(ref) == 0 {
			continue
		}

		// Each reference is: <32-char-hash>-<pname>
		// Minimum: 32 + 1 + 1 = 34 chars
		if len(ref) < 34 {
			continue // Skip invalid references
		}

		if ref[32] != '-' {
			continue // Skip invalid format
		}

		// Check canonical order: lexicographic by full basename
		if prevRef != nil && bytes.Compare(prevRef, ref) >= 0 {
			record.QuirkReferencesOutOfOrder = true
		}

		prevRef = ref

		// Decode nixbase32 hash to binary (32 chars -> 20 bytes)
		var hash [20]byte
		if err := decodeNixbase32Into(&hash, ref[:32]); err != nil {
			continue // Skip invalid hashes
		}

		record.References = append(record.References, hash)
	}

	return nil
}

// parseSHA256Hash parses a "sha256:<hash>" string into binary.
// The hash can be either nixbase32 (52 chars) or hex (64 chars).
// Returns an error if the hash type is not sha256.
func parseSHA256Hash(value []byte, dest *[32]byte, fieldName string) error {
	const sha256Prefix = "sha256:"

	if !bytes.HasPrefix(value, []byte(sha256Prefix)) {
		return fmt.Errorf("%s: unsupported hash type (expected sha256): %s", fieldName, value)
	}

	hashValue := value[len(sha256Prefix):]

	switch len(hashValue) {
	case 52:
		// nixbase32 encoding (52 chars -> 32 bytes)
		if err := decodeNixbase32SHA256Into(dest, hashValue); err != nil {
			return fmt.Errorf("%s: invalid nixbase32 encoding: %w", fieldName, err)
		}
	case 64:
		// hex encoding (64 chars -> 32 bytes)
		decoded, err := hex.DecodeString(string(hashValue))
		if err != nil {
			return fmt.Errorf("%s: invalid hex encoding: %w", fieldName, err)
		}

		copy(dest[:], decoded)
	default:
		return fmt.Errorf("%s: unexpected hash length %d (expected 52 for nixbase32 or 64 for hex): %s",
			fieldName, len(hashValue), value)
	}

	return nil
}

// parseDeriver parses a deriver path and extracts the decoded hash.
// Format: <32-char-hash>-<name>.drv or "unknown-deriver".
func parseDeriver(record *NarInfoRecord, value []byte) error {
	// "unknown-deriver" results in zero-value hash
	if bytes.Equal(value, []byte("unknown-deriver")) {
		return nil
	}

	// Format: <32-char-hash>-<name>.drv
	// Minimum: 32 + 1 + 1 = 34 chars
	if len(value) < 34 {
		return fmt.Errorf("deriver path too short: %s", value)
	}

	if value[32] != '-' {
		return fmt.Errorf("invalid deriver format (expected '-' at position 32): %s", value)
	}

	// Decode nixbase32 hash to binary (32 chars -> 20 bytes)
	if err := decodeNixbase32Into(&record.Deriver, value[:32]); err != nil {
		return fmt.Errorf("invalid deriver hash: %w", err)
	}

	// Extract pname: everything after the hash+dash, minus the .drv suffix
	// Format: <32-char-hash>-<name>.drv
	pnameWithDrv := value[33:] // skip hash + dash
	if len(pnameWithDrv) >= 4 && bytes.HasSuffix(pnameWithDrv, []byte(".drv")) {
		record.DeriverPname = string(pnameWithDrv[:len(pnameWithDrv)-4])
	} else {
		record.DeriverPname = string(pnameWithDrv)
	}

	return nil
}

// parseCA parses the CA (content address) field.
// Format: <type>:<hash_algo>:<hash> or <type>:r:<hash_algo>:<hash>
// Examples:
//   - fixed:sha256:<nixbase32-or-hex>
//   - fixed:r:sha256:<nixbase32-or-hex>
//   - text:sha256:<nixbase32-or-hex>
//
//nolint:cyclop
func parseCA(record *NarInfoRecord, value []byte) error {
	// Find first colon (after type)
	firstColon := bytes.IndexByte(value, ':')
	if firstColon == -1 {
		return fmt.Errorf("invalid CA format (no colon): %s", value)
	}

	caType := value[:firstColon]
	rest := value[firstColon+1:]

	// Build algo prefix based on type
	var algoPrefix string

	switch {
	case bytes.Equal(caType, []byte("fixed")):
		algoPrefix = "fixed:"
	case bytes.Equal(caType, []byte("text")):
		algoPrefix = "text:"
	default:
		return fmt.Errorf("unknown CA type: %s", caType)
	}

	// Check for recursive marker 'r'
	if len(rest) > 2 && rest[0] == 'r' && rest[1] == ':' {
		algoPrefix += "r:"
		rest = rest[2:]
	}

	// Find second colon (after hash algo)
	secondColon := bytes.IndexByte(rest, ':')
	if secondColon == -1 {
		return fmt.Errorf("invalid CA format (missing hash): %s", value)
	}

	hashAlgo := rest[:secondColon]
	hashValue := rest[secondColon+1:]

	// Validate hash algorithm and determine expected sizes
	var expectedNixbase32Len, expectedHexLen, expectedBytes int

	switch {
	case bytes.Equal(hashAlgo, []byte("md5")):
		expectedNixbase32Len = 26
		expectedHexLen = 32
		expectedBytes = 16
	case bytes.Equal(hashAlgo, []byte("sha1")):
		expectedNixbase32Len = 32
		expectedHexLen = 40
		expectedBytes = 20
	case bytes.Equal(hashAlgo, []byte("sha256")):
		expectedNixbase32Len = 52
		expectedHexLen = 64
		expectedBytes = 32
	case bytes.Equal(hashAlgo, []byte("sha512")):
		expectedNixbase32Len = 103
		expectedHexLen = 128
		expectedBytes = 64
	default:
		return fmt.Errorf("unknown CA hash algorithm: %s", hashAlgo)
	}

	// Decode the hash
	var (
		decoded []byte
		err     error
	)

	switch len(hashValue) {
	case expectedNixbase32Len:
		decoded, err = decodeNixbase32(hashValue)
		if err != nil {
			return fmt.Errorf("invalid CA nixbase32 hash: %w", err)
		}
	case expectedHexLen:
		decoded, err = hex.DecodeString(string(hashValue))
		if err != nil {
			return fmt.Errorf("invalid CA hex hash: %w", err)
		}
	default:
		return fmt.Errorf("unexpected CA hash length %d (expected %d for nixbase32 or %d for hex): %s",
			len(hashValue), expectedNixbase32Len, expectedHexLen, value)
	}

	if len(decoded) != expectedBytes {
		return fmt.Errorf("decoded CA hash has wrong length: got %d, want %d", len(decoded), expectedBytes)
	}

	record.CAAlgo = algoPrefix + string(hashAlgo)
	record.CAHash = &decoded

	return nil
}

// parseSignature splits a signature into domain and decoded value.
// Format: domain:base64value (e.g., "cache.nixos.org-1:ABC123...").
// The value is decoded from base64 into a 64-byte Ed25519 signature.
func parseSignature(record *NarInfoRecord, value []byte) error {
	colonIdx := bytes.IndexByte(value, ':')
	if colonIdx == -1 {
		return fmt.Errorf("invalid signature format (no colon): %s", value)
	}

	// Decode base64 signature value
	sigBase64 := value[colonIdx+1:]

	decoded, err := base64.StdEncoding.DecodeString(string(sigBase64))
	if err != nil {
		return fmt.Errorf("invalid signature base64: %w", err)
	}

	if len(decoded) != 64 {
		return fmt.Errorf("invalid signature length: got %d, want 64", len(decoded))
	}

	var sig [64]byte
	copy(sig[:], decoded)

	record.SignatureDomains = append(record.SignatureDomains, string(value[:colonIdx]))
	record.SignatureValues = append(record.SignatureValues, sig)

	return nil
}
