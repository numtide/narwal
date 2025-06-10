package store

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/numtide/narwal/pkg/db"
)

var (
	extensionRegex   = regexp.MustCompile(`\.(narinfo|nar|debug|ls|drv)`)
	compressionRegex = regexp.MustCompile(`(\.(br|bz2|compress|grzip|gzip|lrzip|lz4|lzip|lzma|lzop|xz|zstd))?$`)
)

type PathAnalysis struct {
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

func AnalyzePath(path string) (*PathAnalysis, error) {
	if path == "" {
		return nil, errors.New("path is empty")
	}

	typeExt := typeExtension(path)
	compressionExt := compressionExtension(path)

	result := &PathAnalysis{
		Compression: db.CompressionTypeNone,
	}

	switch {
	case path[:4] == "log/" && strings.Contains(path, ".drv"):
		// logs are written with the .drv suffix and under the `log/` prefix
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

func hashFromPath(path string, objectType db.ObjectType) (string, error) {
	// hash can be 32, 40 or 52 characters depending on the object type
	// it can also be located in different parts of the string
	var hash string

	switch objectType {
	case db.ObjectTypeNar:
		// prefixed with '/nar' and a hash size of 52
		if len(path) < 56 {
			return "", fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hash = path[4:56]
	case db.ObjectTypeDebug:
		// prefixed with 'debuginfo/' and a hash size of 40
		if len(path) < 50 {
			return "", fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hash = path[10:50]
	case db.ObjectTypeLog:
		// 'log/<hash>-<pname>.drv'
		if len(path) < 36 {
			return "", fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hash = path[4:]
	case db.ObjectTypeNarinfo, db.ObjectTypeLs:
		// all other hashes are at the beginning of the path and of size 32
		if len(path) < 32 {
			return "", fmt.Errorf("invalid %v path: %s", objectType, path)
		}

		hash = path[:32]
	}

	return hash, nil
}
