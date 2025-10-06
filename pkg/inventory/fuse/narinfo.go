package fuse

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/numtide/narwal/pkg/inventory"
)

type narinfoStream struct {
	tx     *badger.Txn
	iter   *badger.Iterator
	prefix []byte
}

func (m *narinfoStream) HasNext() bool {
	return m.iter.ValidForPrefix(m.prefix)
}

func (m *narinfoStream) Next() (fuse.DirEntry, syscall.Errno) {
	item := m.iter.Item()
	defer m.iter.Next()

	name := string(item.Key()[len(m.prefix)-3:])

	return fuse.DirEntry{
		Name: name,
		Mode: syscall.S_IFREG,
	}, 0
}

func (m *narinfoStream) Close() {
	m.iter.Close()
	m.tx.Discard()
}

type narinfoDir struct {
	fs.Inode
	db     *badger.DB
	prefix string
}

//nolint:ireturn
func (d *narinfoDir) Readdir(_ context.Context) (fs.DirStream, syscall.Errno) {
	tx := d.db.NewTransaction(false)

	prefix := []byte("narinfo:" + d.prefix)

	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		Reverse:        false,
		AllVersions:    false,
		PrefetchSize:   128,
		PrefetchValues: false,
	})

	iter.Seek(prefix)

	return &narinfoStream{
		tx:     tx,
		iter:   iter,
		prefix: prefix,
	}, 0
}

func (d *narinfoDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	key := []byte(inventory.BadgerPrefixObject + name)
	inode := stableInode(key)

	err := d.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			//nolint:wrapcheck
			return err
		} else if err != nil {
			return fmt.Errorf("failed to get file item from db: %w", err)
		}

		return item.Value(func(val []byte) error {
			out.Size = uint64(len(val))

			return nil
		})
	})
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, syscall.ENOENT
	} else if err != nil {
		return nil, syscall.EIO
	}

	out.Mode = syscall.S_IFREG | 0o444
	out.SetAttrTimeout(time.Hour)
	out.SetEntryTimeout(time.Hour)
	out.Ino = inode

	return d.NewPersistentInode(ctx, &fuseFile{
		db:  d.db,
		key: key,
	}, fs.StableAttr{
		Mode: syscall.S_IFREG,
		Ino:  inode,
	}), 0
}

type narinfosDir struct {
	fs.Inode
	db       *badger.DB
	prefixes []fuse.DirEntry
}

//nolint:ireturn
func (d *narinfosDir) Readdir(_ context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream(d.prefixes), 0
}

func (d *narinfosDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	inode := stableInode([]byte(inventory.BadgerPrefixObject + name))

	out.Mode = syscall.S_IFDIR | 0o555
	out.SetAttrTimeout(time.Hour)
	out.SetEntryTimeout(time.Hour)
	out.Ino = inode

	return d.NewPersistentInode(ctx, &narinfoDir{
		db:     d.db,
		prefix: name,
	}, fs.StableAttr{
		Mode: syscall.S_IFDIR,
		Ino:  inode,
	}), 0
}

func newNarinfosDir(db *badger.DB) *narinfosDir {
	prefixes := make([]fuse.DirEntry, 0, 512)

	tx := db.NewTransaction(false)
	defer tx.Discard()

	for i := range 512 {
		name := fmt.Sprintf("%03x", i)
		prefix := []byte(inventory.BadgerPrefixObject + name)

		iter := tx.NewIterator(badger.IteratorOptions{
			Prefix:         prefix,
			Reverse:        false,
			AllVersions:    false,
			PrefetchSize:   128,
			PrefetchValues: false,
		})

		iter.Seek(prefix)

		exists := iter.ValidForPrefix(prefix)

		iter.Close()

		if !exists {
			// no nar infos with this prefix
			continue
		}

		prefixes = append(prefixes, fuse.DirEntry{
			Name: fmt.Sprintf("%03x", i),
			Mode: syscall.S_IFDIR,
		})
	}

	return &narinfosDir{
		db:       db,
		prefixes: prefixes,
	}
}
