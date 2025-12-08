package inventory

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
	"github.com/numtide/narwal/pkg/config"
)

//nolint:gochecknoglobals
var (
	BadgerPrefixFile     = "f:"
	BadgerPrefixObject   = "o:"
	BadgerPrefixManifest = "m:"

	ErrKeyNotFound      = errors.New("key not found")
	ErrChecksumMismatch = errors.New("checksum mismatch")
)

// badgerLogger adapts charmbracelet/log to badger's Logger interface.
type badgerLogger struct {
	logger *log.Logger
}

func (l *badgerLogger) Errorf(format string, args ...any) {
	l.logger.Errorf(format, args...)
}

func (l *badgerLogger) Warningf(format string, args ...any) {
	l.logger.Warnf(format, args...)
}

func (l *badgerLogger) Infof(format string, args ...any) {
	l.logger.Infof(format, args...)
}

func (l *badgerLogger) Debugf(format string, args ...any) {
	l.logger.Debugf(format, args...)
}

func OpenDB(cfg *config.Badger) (*badger.DB, error) {
	logger := &badgerLogger{
		logger: log.WithPrefix("badger"),
	}
	opts := badger.DefaultOptions(cfg.Path).
		WithLogger(logger).
		WithCompression(options.ZSTD).
		WithBlockCacheSize(1024 << 20).
		WithIndexCacheSize(1024 << 20)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return db, nil
}

func GetManifest(tx *badger.Txn, key string) (*Manifest, error) {
	item, err := tx.Get([]byte(BadgerPrefixManifest + key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, ErrKeyNotFound
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

	if err = tx.Set([]byte(BadgerPrefixManifest+key), buf); err != nil {
		return fmt.Errorf("failed to put manifest in db: %w", err)
	}

	return nil
}

func DeleteManifest(tx *badger.Txn, key string) error {
	if err := tx.Delete([]byte(BadgerPrefixManifest + key)); err != nil {
		return fmt.Errorf("failed to delete manifest from db: %w", err)
	}

	return nil
}

func ListManifests(tx *badger.Txn) ([]string, error) {
	prefix := []byte(BadgerPrefixManifest)

	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		Reverse:        false,
		AllVersions:    false,
		PrefetchSize:   100,
		PrefetchValues: false,
	})

	defer iter.Close()

	var results []string

	for iter.Rewind(); iter.Valid(); iter.Next() {
		item := iter.Item()
		name := string(item.Key()[len(prefix):])

		results = append(results, name)
	}

	return results, nil
}

func HasManifestFile(tx *badger.Txn, file *ManifestFile) (bool, error) {
	_, err := tx.Get([]byte(BadgerPrefixFile + file.UUID()))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to get file item from db: %w", err)
	}

	return true, nil
}

func GetManifestFile(tx *badger.Txn, file *ManifestFile) ([]byte, error) {
	item, err := tx.Get([]byte(BadgerPrefixFile + file.UUID()))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, ErrKeyNotFound
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

	if err := tx.Set([]byte(BadgerPrefixFile+file.UUID()), file.Data); err != nil {
		return fmt.Errorf("failed to put file in db: %w", err)
	}

	return nil
}

func DeleteManifestFile(tx *badger.Txn, file *ManifestFile) error {
	if err := tx.Delete([]byte(BadgerPrefixFile + file.UUID())); err != nil {
		return fmt.Errorf("failed to delete file from db: %w", err)
	}

	return nil
}

func ListManifestFiles(tx *badger.Txn) ([]string, error) {
	prefix := []byte(BadgerPrefixFile)

	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		Reverse:        false,
		AllVersions:    false,
		PrefetchSize:   100,
		PrefetchValues: false,
	})

	defer iter.Close()

	var results []string

	for iter.Rewind(); iter.Valid(); iter.Next() {
		item := iter.Item()
		name := string(item.Key()[len(prefix):])

		results = append(results, name)
	}

	return results, nil
}

func HasFileNarInfosBeenDownloaded(tx *badger.Txn, file ManifestFile) (bool, error) {
	item, err := tx.Get([]byte(BadgerPrefixFile + file.UUID()))
	if errors.Is(err, badger.ErrKeyNotFound) {
		//nolint:wrapcheck
		return false, err
	} else if err != nil {
		return false, fmt.Errorf("failed to get file item from db: %w", err)
	}

	return item.UserMeta() == 1, nil
}

func MarkManifestFileAsDownloaded(tx *badger.Txn, file *ManifestFile) error {
	item, err := tx.Get([]byte(BadgerPrefixFile + file.UUID()))
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

func HasNarInfo(tx *badger.Txn, obj *Object) (bool, error) {
	key := ObjectKey(obj)

	_, err := tx.Get(key)
	if errors.Is(err, badger.ErrKeyNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to get file item from db: %w", err)
	}

	return true, nil
}

func ReadObjectKey(buf []byte) (int64, string, error) {
	buf = buf[len(BadgerPrefixObject):]

	if len(buf) < 9 {
		return 0, "", fmt.Errorf("key is too short: %d", len(buf))
	}

	return DecodeTimestamp(buf[:8]), string(buf[8:]), nil
}

func ObjectKey(obj *Object) []byte {
	ts := TruncateToWeek(obj.LastModifiedDate)

	key := append([]byte(BadgerPrefixObject), EncodeTimestamp(ts)...)
	key = append(key, obj.Key...)

	return key
}

func NewObjectEntry(obj *Object, buf []byte) *badger.Entry {
	return &badger.Entry{
		Key:   ObjectKey(obj),
		Value: buf,
	}
}

func TruncateToWeek(msEpoch int64) int64 {
	t := time.UnixMilli(msEpoch).UTC()

	// Calculate days since Monday
	weekday := int(t.Weekday())
	if weekday == 0 { // Sunday
		weekday = 7
	}

	daysSinceMonday := weekday - 1

	// Truncate to start of day, then subtract days to get to Monday
	truncated := t.Truncate(24*time.Hour).AddDate(0, 0, -daysSinceMonday)

	return truncated.UnixMilli()
}

func EncodeTimestamp(ms int64) []byte {
	if ms < 0 {
		// should never happen
		panic("timestamp cannot be negative")
	}

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(ms))

	return buf
}

func DecodeTimestamp(buf []byte) int64 {
	return int64(binary.BigEndian.Uint64(buf)) //nolint:gosec
}
