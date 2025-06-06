package store

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/numtide/narwal/pkg/db"
)

var suffixRegex = regexp.MustCompile(
	`\.(nar|narinfo|debug|ls|drv)(\.(br|compress|grzip|gzip|lrzip|lz4|lzip|lzma|lzop|xz|zstd))?$`,
)

type pathAnalysis struct {
	ObjectType  db.ObjectType
	Compression db.CompressionType
}

func AnalyzePath(path string) (*pathAnalysis, error) {
	matches := suffixRegex.FindStringSubmatch(path)

	if len(matches) <= 1 {
		return nil, fmt.Errorf("invalid path: %s", path)
	}

	result := &pathAnalysis{
		Compression: db.CompressionTypeNone,
	}

	// logs are written with the .drv suffix and under the `log/` prefix
	if path[:4] == "log/" && strings.Contains(path, ".drv") {
		result.ObjectType = db.ObjectTypeLog
	} else {
		// otherwise we rely on the suffix to determine the object type
		result.ObjectType = db.ObjectType(matches[1])
	}

	// determine compression
	if len(matches) == 4 && matches[3] != "" {
		result.Compression = db.CompressionType(matches[3])
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
