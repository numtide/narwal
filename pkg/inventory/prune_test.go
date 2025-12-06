package inventory_test

import (
	"fmt"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/inventory"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*badger.DB, *config.Badger) {
	t.Helper()

	tmpDir := t.TempDir()
	cfg := &config.Badger{Path: tmpDir}

	db, err := inventory.OpenDB(cfg)
	require.NoError(t, err)

	return db, cfg
}

func createTestManifest(id string, fileUUIDs []string) *inventory.Manifest {
	files := make([]inventory.ManifestFile, len(fileUUIDs))
	for i, uuid := range fileUUIDs {
		files[i] = inventory.ManifestFile{
			Key:         "some/path/" + uuid,
			Size:        100,
			MD5Checksum: "abc123",
		}
	}

	return &inventory.Manifest{
		Files:        files,
		SourceBucket: "test-bucket",
		DestBucket:   "test-dest",
		Version:      "1.0",
		CreationTime: id,
		FileFormat:   "Parquet",
		FileSchema:   "test-schema",
	}
}

func storeManifests(tx *badger.Txn, manifests ...*inventory.Manifest) error {
	for _, m := range manifests {
		if err := inventory.PutManifest(tx, m.CreationTime, m); err != nil {
			return fmt.Errorf("failed to put manifest %s: %w", m.CreationTime, err)
		}

		for i := range m.Files {
			m.Files[i].Data = []byte("test data")

			if err := inventory.PutManifestFile(tx, &m.Files[i]); err != nil {
				return fmt.Errorf("failed to put file %s: %w", m.Files[i].Key, err)
			}
		}
	}

	return nil
}

func TestPrune_RemovesUnlistedManifests(t *testing.T) {
	t.Parallel()

	db, cfg := setupTestDB(t)

	// Create test manifests with files
	manifest1 := createTestManifest("2024-01-01T00-00Z", []string{"file1.parquet", "file2.parquet"})
	manifest2 := createTestManifest("2024-01-02T00-00Z", []string{"file3.parquet", "file4.parquet"})
	manifest3 := createTestManifest("2024-01-03T00-00Z", []string{"file5.parquet"})

	// Store manifests and their files
	err := db.Update(func(tx *badger.Txn) error {
		return storeManifests(tx, manifest1, manifest2, manifest3)
	})
	require.NoError(t, err)

	// Close DB before pruning (Prune opens its own connection)
	require.NoError(t, db.Close())

	// Prune, keeping only manifest1 and manifest3
	result, err := inventory.Prune(cfg, []string{"2024-01-01T00-00Z", "2024-01-03T00-00Z"})
	require.NoError(t, err)

	require.Equal(t, 1, result.ManifestsDeleted)
	require.Equal(t, 2, result.FilesDeleted) // manifest2 had 2 files

	// Reopen DB and verify state
	db, err = inventory.OpenDB(cfg)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, db.Close())
	}()

	err = db.View(func(tx *badger.Txn) error {
		// Verify manifest1 still exists
		m1, err := inventory.GetManifest(tx, "2024-01-01T00-00Z")
		require.NoError(t, err)
		require.NotNil(t, m1)

		// Verify manifest2 is deleted
		_, err = inventory.GetManifest(tx, "2024-01-02T00-00Z")
		require.ErrorIs(t, err, inventory.ErrKeyNotFound)

		// Verify manifest3 still exists
		m3, err := inventory.GetManifest(tx, "2024-01-03T00-00Z")
		require.NoError(t, err)
		require.NotNil(t, m3)

		// Verify files for manifest1 still exist
		for i := range manifest1.Files {
			exists, err := inventory.HasManifestFile(tx, &manifest1.Files[i])
			require.NoError(t, err)
			require.True(t, exists, "file %s should exist", manifest1.Files[i].Key)
		}

		// Verify files for manifest2 are deleted
		for i := range manifest2.Files {
			exists, err := inventory.HasManifestFile(tx, &manifest2.Files[i])
			require.NoError(t, err)
			require.False(t, exists, "file %s should be deleted", manifest2.Files[i].Key)
		}

		// Verify files for manifest3 still exist
		for i := range manifest3.Files {
			exists, err := inventory.HasManifestFile(tx, &manifest3.Files[i])
			require.NoError(t, err)
			require.True(t, exists, "file %s should exist", manifest3.Files[i].Key)
		}

		return nil
	})
	require.NoError(t, err)
}

func TestPrune_KeepsAllWhenAllListed(t *testing.T) {
	t.Parallel()

	db, cfg := setupTestDB(t)

	manifest1 := createTestManifest("2024-01-01T00-00Z", []string{"file1.parquet"})
	manifest2 := createTestManifest("2024-01-02T00-00Z", []string{"file2.parquet"})

	err := db.Update(func(tx *badger.Txn) error {
		return storeManifests(tx, manifest1, manifest2)
	})
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Keep all manifests
	result, err := inventory.Prune(cfg, []string{"2024-01-01T00-00Z", "2024-01-02T00-00Z"})
	require.NoError(t, err)

	require.Equal(t, 0, result.ManifestsDeleted)
	require.Equal(t, 0, result.FilesDeleted)
}

func TestPrune_RemovesAllWhenEmptyKeepList(t *testing.T) {
	t.Parallel()

	db, cfg := setupTestDB(t)

	manifest1 := createTestManifest("2024-01-01T00-00Z", []string{"file1.parquet"})

	err := db.Update(func(tx *badger.Txn) error {
		return storeManifests(tx, manifest1)
	})
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Empty keep list - should remove everything
	result, err := inventory.Prune(cfg, []string{})
	require.NoError(t, err)

	require.Equal(t, 1, result.ManifestsDeleted)
	require.Equal(t, 1, result.FilesDeleted)
}

func TestPrune_HandlesEmptyDatabase(t *testing.T) {
	t.Parallel()

	db, cfg := setupTestDB(t)
	require.NoError(t, db.Close())

	result, err := inventory.Prune(cfg, []string{"2024-01-01T00-00Z"})
	require.NoError(t, err)

	require.Equal(t, 0, result.ManifestsDeleted)
	require.Equal(t, 0, result.FilesDeleted)
}

func TestPrune_HandlesNonExistentKeepIDs(t *testing.T) {
	t.Parallel()

	db, cfg := setupTestDB(t)

	manifest1 := createTestManifest("2024-01-01T00-00Z", []string{"file1.parquet"})

	err := db.Update(func(tx *badger.Txn) error {
		return storeManifests(tx, manifest1)
	})
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Keep list includes non-existent ID and the existing one
	result, err := inventory.Prune(cfg, []string{"2024-01-01T00-00Z", "non-existent-id"})
	require.NoError(t, err)

	require.Equal(t, 0, result.ManifestsDeleted)
	require.Equal(t, 0, result.FilesDeleted)

	// Verify manifest still exists
	db, err = inventory.OpenDB(cfg)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, db.Close())
	}()

	err = db.View(func(tx *badger.Txn) error {
		m, err := inventory.GetManifest(tx, "2024-01-01T00-00Z")
		require.NoError(t, err)
		require.NotNil(t, m)

		return nil
	})
	require.NoError(t, err)
}
