package server

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/pkg/db"
)

var (
	extensionRegex   = regexp.MustCompile(`\.(narinfo|nar|debug|ls|drv)`)
	compressionRegex = regexp.MustCompile(`(\.(br|bz2|compress|grzip|gzip|lrzip|lz4|lzip|lzma|lzop|xz|zstd))?$`)
)

// pathAnalysis contains the results of analysing a file path.
type pathAnalysis struct {
	ObjectType  db.ObjectType
	Compression db.CompressionType
}

func typeExtension(path string) string {
	matches := extensionRegex.FindStringSubmatch(path)
	if len(matches) == 2 {
		return matches[1]
	}

	return ""
}

func compressionExtension(path string) string {
	matches := compressionRegex.FindStringSubmatch(path)
	if len(matches) == 3 && matches[2] != "" {
		return matches[2]
	}

	return ""
}

func examinePath(path string) (*pathAnalysis, error) {
	if path == "" {
		return nil, errors.New("path is empty")
	}

	typeExt := typeExtension(path)
	compressionExt := compressionExtension(path)

	result := &pathAnalysis{
		Compression: db.CompressionTypeNone,
	}

	switch {
	case path[:4] == "log/":
		result.ObjectType = db.ObjectTypeLog

	case path[:10] == "debuginfo/":
		// historical debug files did not have the .debug suffix, so we use the prefix to ensure we catch them all
		result.ObjectType = db.ObjectTypeDebug

	default:
		// otherwise we rely on the type extension to determine the object type
		if typeExt == "" {
			return nil, fmt.Errorf("could not determine object type for path: %s", path)
		}

		result.ObjectType = db.ObjectType(typeExt)
	}

	// determine compression
	if compressionExt != "" {
		result.Compression = db.CompressionType(compressionExt)
	}

	return result, nil
}

// hashFromPath extracts and decodes the hash from a path given the object type.
// Returns decoded bytes: 32 bytes for nar (52-char nixbase32), 20 bytes for others.
func hashFromPath(path string, objectType db.ObjectType) ([]byte, error) {
	// hash can be 32, 40 or 52 characters depending on the object type
	// it can also be located in different parts of the string
	var hashStr string

	switch objectType {
	case db.ObjectTypeNar:
		// prefixed with 'nar/' and a hash size of 52 (nixbase32, decodes to 32 bytes)
		if len(path) < 56 {
			return nil, fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hashStr = path[4:56]

		hash, err := nixbase32.DecodeString(hashStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode nixbase32 hash %s: %w", hashStr, err)
		}

		return hash, nil
	case db.ObjectTypeDebug:
		// prefixed with 'debuginfo/' and a hash size of 40 (hex, decodes to 20 bytes)
		// historically there are some entries with less characters and no .debug extension
		if len(path) < 50 {
			return nil, fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hashStr = path[10:50]

		hash, err := hex.DecodeString(hashStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode hex hash %s: %w", hashStr, err)
		}

		return hash, nil
	case db.ObjectTypeLog:
		// 'log/<hash>-<pname>.drv' (32-char nixbase32, decodes to 20 bytes)
		if len(path) < 36 {
			return nil, fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hashStr = path[4:36]

		hash, err := nixbase32.DecodeString(hashStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode nixbase32 hash %s: %w", hashStr, err)
		}

		return hash, nil
	case db.ObjectTypeNarinfo, db.ObjectTypeLs:
		// hashes at the beginning of the path, size 32 (nixbase32, decodes to 20 bytes)
		if len(path) < 32 {
			return nil, fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hashStr = path[:32]

		hash, err := nixbase32.DecodeString(hashStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode nixbase32 hash %s: %w", hashStr, err)
		}

		return hash, nil
	}

	return nil, fmt.Errorf("unknown object type: %v", objectType)
}
