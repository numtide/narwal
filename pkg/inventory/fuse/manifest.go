//nolint:ireturn
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

const (
	manifestFileName = "manifest.json"
)

type manifestDirStream struct {
	idx      int
	manifest *inventory.Manifest
}

func (m *manifestDirStream) HasNext() bool {
	return m.idx < len(m.manifest.Files)
}

func (m *manifestDirStream) Next() (fuse.DirEntry, syscall.Errno) {
	var entry fuse.DirEntry

	if m.idx == -1 {
		// output the manifest JSON file itself
		entry = fuse.DirEntry{
			Name: manifestFileName,
			Mode: syscall.S_IFREG,
		}
	} else {
		entry = fuse.DirEntry{
			Name: m.manifest.Files[m.idx].UUID(),
			Mode: syscall.S_IFREG,
		}
	}

	// increment the index
	m.idx++

	return entry, 0
}

func (m *manifestDirStream) Close() {
	// nothing to do
}

type manifestDir struct {
	fs.Inode

	db *badger.DB

	name     string
	manifest *inventory.Manifest
}

func (d *manifestDir) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	tx := d.db.NewTransaction(false)
	defer tx.Discard()

	return &manifestDirStream{
		idx:      -1,
		manifest: d.manifest,
	}, 0
}

func (d *manifestDir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0o555
	out.SetTimeout(time.Hour)

	return 0
}

func (d *manifestDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	tx := d.db.NewTransaction(false)
	defer tx.Discard()

	var key []byte

	if name == manifestFileName {
		key = []byte(inventory.BadgerPrefixManifest + d.name)
	} else {
		key = []byte(inventory.BadgerPrefixFile + name)
	}

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
	out.Ino = stableInode(key)

	fileNode := &fuseFile{
		db:  d.db,
		key: key,
	}

	return d.NewPersistentInode(ctx, fileNode, fs.StableAttr{
		Mode: syscall.S_IFREG,
		Ino:  stableInode(key),
	}), 0
}

type manifestsDir struct {
	fs.Inode

	db *badger.DB
}

func (d *manifestsDir) Readdir(_ context.Context) (fs.DirStream, syscall.Errno) {
	tx := d.db.NewTransaction(false)
	defer tx.Discard()

	manifests, err := inventory.ListManifests(tx)
	if err != nil {
		return nil, syscall.EIO
	}

	entries := make([]fuse.DirEntry, len(manifests))
	for i, manifest := range manifests {
		entries[i] = fuse.DirEntry{
			Name: manifest,
			Mode: syscall.S_IFDIR,
		}
	}

	return fs.NewListDirStream(entries), 0
}

func (d *manifestsDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	tx := d.db.NewTransaction(false)
	defer tx.Discard()

	manifest, err := inventory.GetManifest(tx, name)
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, syscall.ENOENT
	} else if err != nil {
		return nil, syscall.EIO
	}

	key := []byte(inventory.BadgerPrefixManifest + name)

	out.Mode = syscall.S_IFDIR | 0o555
	out.SetAttrTimeout(time.Hour)
	out.SetEntryTimeout(time.Hour)
	out.Ino = stableInode(key)

	return d.NewPersistentInode(ctx, &manifestDir{
		db:       d.db,
		name:     name,
		manifest: manifest,
	}, fs.StableAttr{
		Mode: syscall.S_IFDIR,
		Ino:  stableInode(key),
	}), 0
}
