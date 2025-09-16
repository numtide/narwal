package inventory

import (
	//nolint:gosec
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
	"github.com/numtide/narwal/pkg/config"
)

//nolint:gochecknoglobals
var (
	BadgerPrefixFiles     = "files:"
	BadgerPrefixNarInfo   = "narinfo:"
	BadgerPrefixManifests = "manifests:"

	ErrKeyNotFound = errors.New("key not found")
)

// badgerLogger adapts charmbracelet/log to badger's Logger interface.
type badgerLogger struct {
	logger *log.Logger
}

func (l *badgerLogger) Errorf(format string, args ...interface{}) {
	l.logger.Errorf(format, args...)
}

func (l *badgerLogger) Warningf(format string, args ...interface{}) {
	l.logger.Warnf(format, args...)
}

func (l *badgerLogger) Infof(format string, args ...interface{}) {
	l.logger.Infof(format, args...)
}

func (l *badgerLogger) Debugf(format string, args ...interface{}) {
	l.logger.Debugf(format, args...)
}

func OpenDB(cfg *config.Badger) (*badger.DB, error) {
	logger := &badgerLogger{
		logger: log.WithPrefix("badger"),
	}
	opts := badger.DefaultOptions(cfg.Path).
		WithLogger(logger).
		WithCompression(options.ZSTD).
		WithBlockCacheSize(256 << 20). // 256 MB block cache (increase from default 0)
		WithIndexCacheSize(256 << 20)  // 256 MB index cache

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return db, nil
}

func HasManifest(tx *badger.Txn, key string) (bool, error) {
	_, err := tx.Get([]byte(BadgerPrefixManifests + key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to get manifest item from db: %w", err)
	}

	return true, nil
}

func GetManifest(tx *badger.Txn, key string) (*Manifest, error) {
	item, err := tx.Get([]byte(BadgerPrefixManifests + key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		//nolint:wrapcheck
		return nil, err
	} else if err != nil {
		return nil, fmt.Errorf("failed to get manifest item from db: %w", err)
	}

	var manifest Manifest

	if err = item.Value(func(val []byte) error {
		if err = json.Unmarshal(val, &manifest); err != nil {
			return fmt.Errorf("failed to unmarshal manifest: %w", err)
		}

		return nil
	}); err != nil {
		//nolint:wrapcheck
		return nil, err
	}

	return &manifest, nil
}

func PutManifest(tx *badger.Txn, key string, manifest *Manifest) error {
	buf, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err = tx.Set([]byte(BadgerPrefixManifests+key), buf); err != nil {
		return fmt.Errorf("failed to put manifest in db: %w", err)
	}

	return nil
}

func ListManifests(tx *badger.Txn) ([]string, error) {
	prefix := []byte(BadgerPrefixManifests)

	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		Reverse:        false,
		AllVersions:    false,
		PrefetchSize:   100,
		PrefetchValues: false,
	})

	defer iter.Close()

	iter.Seek(prefix)

	var results []string

	for iter.ValidForPrefix(prefix) {
		item := iter.Item()
		name := string(item.Key()[len(prefix):])

		results = append(results, name)

		iter.Next()
	}

	return results, nil
}

func HasManifestFile(tx *badger.Txn, file *ManifestFile) (bool, error) {
	_, err := tx.Get([]byte(BadgerPrefixFiles + file.Basename()))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to get file item from db: %w", err)
	}

	return true, nil
}

func GetManifestFile(tx *badger.Txn, file *ManifestFile) ([]byte, error) {
	item, err := tx.Get([]byte(BadgerPrefixFiles + file.Basename()))
	if errors.Is(err, badger.ErrKeyNotFound) {
		//nolint:wrapcheck
		return nil, err
	} else if err != nil {
		return nil, fmt.Errorf("failed to get manifest item from db: %w", err)
	}

	buf, err := item.ValueCopy(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to copy item value from db: %w", err)
	}

	return buf, nil
}

func PutManifestFile(tx *badger.Txn, file *ManifestFile) error {
	if file.Data == nil {
		return errors.New("file data is nil")
	}

	if err := tx.Set([]byte(BadgerPrefixFiles+file.Basename()), file.Data); err != nil {
		return fmt.Errorf("failed to put file in db: %w", err)
	}

	return nil
}

func ListManifestFiles(tx *badger.Txn) ([]string, error) {
	prefix := []byte(BadgerPrefixFiles)

	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		Reverse:        false,
		AllVersions:    false,
		PrefetchSize:   100,
		PrefetchValues: false,
	})

	defer iter.Close()

	iter.Seek(prefix)

	var results []string

	for iter.ValidForPrefix(prefix) {
		item := iter.Item()
		name := string(item.Key()[len(prefix):])

		results = append(results, name)

		iter.Next()
	}

	return results, nil
}

func MarkManifestFileAsDownloaded(tx *badger.Txn, file *ManifestFile) error {
	item, err := tx.Get([]byte(BadgerPrefixFiles + file.Basename()))
	if errors.Is(err, badger.ErrKeyNotFound) {
		//nolint:wrapcheck
		return err
	} else if err != nil {
		return fmt.Errorf("failed to get file item from db: %w", err)
	}

	// annoyingly, we need to retrieve the value to update the user meta
	val, err := item.ValueCopy(nil)
	if err != nil {
		return fmt.Errorf("failed to copy file value from db: %w", err)
	}

	entry := badger.NewEntry(item.Key(), val).WithMeta(byte(1))
	if err = tx.SetEntry(entry); err != nil {
		return fmt.Errorf("failed to mark file as downloaded in db: %w", err)
	}

	return nil
}

func HasFileBeenDownloaded(tx *badger.Txn, file ManifestFile) (bool, error) {
	item, err := tx.Get([]byte(BadgerPrefixFiles + file.Basename()))
	if errors.Is(err, badger.ErrKeyNotFound) {
		//nolint:wrapcheck
		return false, err
	} else if err != nil {
		return false, fmt.Errorf("failed to get file item from db: %w", err)
	}

	return item.UserMeta() == 1, nil
}

func HasNarInfo(tx *badger.Txn, key string) (bool, error) {
	_, err := tx.Get([]byte(BadgerPrefixNarInfo + key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to get file item from db: %w", err)
	}

	return true, nil
}

func VerifyNarInfo(tx *badger.Txn, key string, size int, etag string) error {
	item, err := tx.Get([]byte(BadgerPrefixNarInfo + key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return ErrKeyNotFound
	} else if err != nil {
		return fmt.Errorf("failed to get narinfo item from db: %w", err)
	}

	err = item.Value(func(val []byte) error {
		if len(val) != size {
			return fmt.Errorf("narinfo size mismatch: %d != %d", len(val), size)
		}

		//nolint:gosec
		checksum := md5.Sum(val)
		checksumHex := hex.EncodeToString(checksum[:])

		if checksumHex != etag {
			return fmt.Errorf("narinfo checksum mismatch: %s != %s", checksumHex, etag)
		}

		log.Debugf("narinfo %s is valid: size = %d, checksum = %s", key, size, etag)

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to verify narinfo: %w", err)
	}

	return nil
}
