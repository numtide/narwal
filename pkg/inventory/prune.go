package inventory

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/numtide/narwal/pkg/config"
)

// PruneResult contains statistics about the prune operation.
type PruneResult struct {
	ManifestsDeleted int
	FilesDeleted     int
	GCRuns           int
	VlogSizeBefore   int64
	VlogSizeAfter    int64
}

// Prune removes manifests and their associated files from the database
// that are not in the provided keep list.
func Prune(cfg *config.Badger, keepReports []string) (*PruneResult, error) {
	// Build a set of reports to keep for fast lookup
	keepSet := make(map[string]struct{}, len(keepReports))
	for _, r := range keepReports {
		keepSet[r] = struct{}{}
	}

	db, err := OpenDB(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Errorf("failed to close db: %s", closeErr)
		}
	}()

	result := &PruneResult{}

	// Get list of all manifests
	var allManifests []string

	if err = db.View(func(tx *badger.Txn) error {
		allManifests, err = ListManifests(tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("failed to list manifests: %w", err)
	}

	// Find manifests to delete
	for _, manifestID := range allManifests {
		if _, keep := keepSet[manifestID]; keep {
			log.Infof("keeping manifest %s", manifestID)
			continue
		}

		log.Infof("pruning manifest %s", manifestID)

		// Get the manifest to find its files
		var manifest *Manifest

		if err = db.View(func(tx *badger.Txn) error {
			manifest, err = GetManifest(tx, manifestID)
			return err
		}); err != nil {
			return nil, fmt.Errorf("failed to get manifest %s: %w", manifestID, err)
		}

		// Delete the manifest and its files in a single transaction
		if err = db.Update(func(tx *badger.Txn) error {
			// Delete associated files
			for _, file := range manifest.Files {
				if delErr := DeleteManifestFile(tx, &file); delErr != nil {
					return fmt.Errorf("failed to delete file %s: %w", file.Key, delErr)
				}

				result.FilesDeleted++
			}

			// Delete the manifest itself
			if delErr := DeleteManifest(tx, manifestID); delErr != nil {
				return fmt.Errorf("failed to delete manifest %s: %w", manifestID, delErr)
			}

			result.ManifestsDeleted++

			return nil
		}); err != nil {
			return nil, fmt.Errorf("failed to update db: %w", err)
		}
	}

	// Run value log garbage collection to reclaim disk space
	_, result.VlogSizeBefore = db.Size()
	log.Infof("running garbage collection (vlog size: %d bytes)", result.VlogSizeBefore)

	for {
		err := db.RunValueLogGC(0.01) // reclaim if >1% of vlog is garbage
		if errors.Is(err, badger.ErrNoRewrite) {
			break // no more GC needed
		}

		if err != nil {
			return nil, fmt.Errorf("failed to run GC: %w", err)
		}

		result.GCRuns++
		log.Infof("GC cycle %d completed", result.GCRuns)
	}

	_, result.VlogSizeAfter = db.Size()

	return result, nil
}
