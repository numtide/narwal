package store_test

import (
	"testing"

	"github.com/numtide/narwal/pkg/store"

	"github.com/numtide/narwal/pkg/db"
)

func TestAnalyzePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		expectedType db.ObjectType
		expectedComp db.CompressionType
		expectError  bool
	}{
		{
			name:         "nar file uncompressed",
			path:         "nar/1234567890123456789012345678901234567890123456789012.nar",
			expectedType: db.ObjectTypeNar,
			expectedComp: db.CompressionTypeNone,
			expectError:  false,
		},
		{
			name:         "nar file with gzip compression",
			path:         "nar/1234567890123456789012345678901234567890123456789012.nar.gzip",
			expectedType: db.ObjectTypeNar,
			expectedComp: db.CompressionType("gzip"),
			expectError:  false,
		},
		{
			name:         "narinfo file",
			path:         "12345678901234567890123456789012.narinfo",
			expectedType: db.ObjectTypeNarinfo,
			expectedComp: db.CompressionTypeNone,
			expectError:  false,
		},
		{
			name:         "narinfo file with xz compression",
			path:         "12345678901234567890123456789012.narinfo.xz",
			expectedType: db.ObjectTypeNarinfo,
			expectedComp: db.CompressionType("xz"),
			expectError:  false,
		},
		{
			name:         "debug file",
			path:         "debuginfo/1234567890123456789012345678901234567890.debug",
			expectedType: db.ObjectTypeDebug,
			expectedComp: db.CompressionTypeNone,
			expectError:  false,
		},
		{
			name:         "debug file with brotli compression",
			path:         "debuginfo/1234567890123456789012345678901234567890.debug.br",
			expectedType: db.ObjectTypeDebug,
			expectedComp: db.CompressionType("br"),
			expectError:  false,
		},
		{
			name:         "ls file",
			path:         "12345678901234567890123456789012.ls",
			expectedType: db.ObjectTypeLs,
			expectedComp: db.CompressionTypeNone,
			expectError:  false,
		},
		{
			name:         "ls file with lz4 compression",
			path:         "12345678901234567890123456789012.ls.lz4",
			expectedType: db.ObjectTypeLs,
			expectedComp: db.CompressionType("lz4"),
			expectError:  false,
		},
		{
			name:         "log file",
			path:         "log/12345678901234567890123456789012.drv",
			expectedType: db.ObjectTypeLog,
			expectedComp: db.CompressionTypeNone,
			expectError:  false,
		},
		{
			name:         "log file with zstd compression",
			path:         "log/12345678901234567890123456789012.drv.zstd",
			expectedType: db.ObjectTypeLog,
			expectedComp: db.CompressionType("zstd"),
			expectError:  false,
		},
		{
			name:        "invalid path without extension",
			path:        "invalid/path/without/extension",
			expectError: true,
		},
		{
			name:        "invalid path with wrong extension",
			path:        "invalid/path.txt",
			expectError: true,
		},
		{
			name:        "empty path",
			path:        "",
			expectError: true,
		},
		{
			name:         "Debug without .debug suffix",
			path:         "debuginfo/e8926a7b0c39a8e846ae02d54fbc596369c2bce1",
			expectedType: db.ObjectTypeDebug,
			expectedComp: db.CompressionTypeNone,
			expectError:  false,
		},
		{
			name:         "Debug without .debug suffix and compressed", // not sure if this occurs but checking anyway
			path:         "debuginfo/e8926a7b0c39a8e846ae02d54fbc596369c2bce1.zstd",
			expectedType: db.ObjectTypeDebug,
			expectedComp: db.CompressionTypeZstd,
			expectError:  false,
		},
		{
			name:         "Nar with bz2 compression",
			path:         "nar/0sgrcbypviy83aswidi86vprqm6rq5rikld4pbd9ripsk88n2xzf.nar.bz2",
			expectedType: db.ObjectTypeNar,
			expectedComp: db.CompressionTypeBz2,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := store.AnalyzePath(tt.path)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for path %s, but got none", tt.path)
				}

				return
			}

			if err != nil {
				t.Errorf("Unexpected error for path %s: %v", tt.path, err)
				return
			}

			if result == nil {
				t.Errorf("Expected result for path %s, but got nil", tt.path)
				return
			}

			if result.ObjectType != tt.expectedType {
				t.Errorf("Expected object type %s for path %s, but got %s", tt.expectedType, tt.path, result.ObjectType)
			}

			if result.Compression != tt.expectedComp {
				t.Errorf("Expected compression %s for path %s, but got %s", tt.expectedComp, tt.path, result.Compression)
			}
		})
	}
}

func TestAnalyzePathCompressionTypes(t *testing.T) {
	t.Parallel()

	compressionTypes := []string{"br", "compress", "grzip", "gzip", "lrzip", "lz4", "lzip", "lzma", "lzop", "xz", "zstd"}

	for _, comp := range compressionTypes {
		t.Run("compression_"+comp, func(t *testing.T) {
			t.Parallel()

			path := "12345678901234567890123456789012.narinfo." + comp

			result, err := store.AnalyzePath(path)
			if err != nil {
				t.Errorf("Unexpected error for compression %s: %v", comp, err)
				return
			}

			if result.ObjectType != db.ObjectTypeNarinfo {
				t.Errorf("Expected ObjectTypeNarinfo, got %s", result.ObjectType)
			}

			if result.Compression != db.CompressionType(comp) {
				t.Errorf("Expected compression %s, got %s", comp, result.Compression)
			}
		})
	}
}
