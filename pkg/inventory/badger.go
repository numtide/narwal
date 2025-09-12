package inventory

import (
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
	BadgerPrefixManifests = "manifests:"
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
		WithCompression(options.ZSTD)

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

func HasFile(tx *badger.Txn, key string) (bool, error) {
	_, err := tx.Get([]byte(BadgerPrefixFiles + key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to get file item from db: %w", err)
	}

	return true, nil
}

func GetFile(tx *badger.Txn, key string) ([]byte, error) {
	item, err := tx.Get([]byte(BadgerPrefixFiles + key))
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

func PutFile(tx *badger.Txn, key string, buf []byte) error {
	if err := tx.Set([]byte(BadgerPrefixFiles+key), buf); err != nil {
		return fmt.Errorf("failed to put file in db: %w", err)
	}

	return nil
}
