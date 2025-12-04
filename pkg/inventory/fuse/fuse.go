package fuse

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"syscall"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// fuseFile represents a Badger-backed file in the FUSE filesystem.
type fuseFile struct {
	fs.Inode

	db  *badger.DB
	key []byte
}

// Open opens the file for reading.
//
//nolint:ireturn
func (f *fuseFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// enforce a read-only file system
	if flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, 0, syscall.EROFS
	}

	return &fuseFileHandle{db: f.db, key: f.key}, 0, 0
}

// Getattr returns file attributes.
func (f *fuseFile) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// read the entry in the db and get its size
	err := f.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(f.key)
		if err != nil {
			//nolint:wrapcheck
			return err
		}

		// note: item.ValueSize() is an estimate which is why we get the size from the value itself
		return item.Value(func(val []byte) error {
			out.Size = uint64(len(val))

			return nil
		})
	})

	// handle errors
	if errors.Is(err, badger.ErrKeyNotFound) {
		return syscall.ENOENT
	} else if err != nil {
		return syscall.EIO
	}

	// set the file attributes
	out.Mode = syscall.S_IFREG | 0o444
	out.SetTimeout(time.Hour)

	return 0
}

// fuseFileHandle represents an open file handle.
type fuseFileHandle struct {
	db  *badger.DB
	key []byte
}

// Read reads data for the file from the Badger db.
//
//nolint:ireturn
func (fh *fuseFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// a buffer to hold the data read from the db
	var data []byte

	// determine an end length based on the desired offset and the length of the destination array
	end := int(off) + len(dest)

	// read from the db
	err := fh.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(fh.key)
		if err != nil {
			//nolint:wrapcheck
			return err
		}

		return item.Value(func(val []byte) error {
			// if the end offset is greater than the length of the value, truncate it
			if end > len(val) {
				end = len(val)
			}

			// if the offset is within the value, copy the data to the destination array
			if off < int64(len(val)) {
				data = val[off:end]
			}

			return nil
		})
	})

	// handle errors
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, syscall.ENOENT
	} else if err != nil {
		return nil, syscall.EIO
	}

	return fuse.ReadResultData(data), 0
}

// FileSystem implements a FUSE filesystem for the Badger database.
type FileSystem struct {
	fs.Inode

	db *badger.DB
}

// NewFilesystem creates a new FUSE filesystem backed by the Badger database.
func NewFilesystem(db *badger.DB) *FileSystem {
	return &FileSystem{db: db}
}

// OnAdd is called when the filesystem is mounted.
func (r *FileSystem) OnAdd(ctx context.Context) {
	// Create the top-level directory structure
	r.AddChild(
		"manifests",
		r.NewPersistentInode(
			ctx,
			&manifestsDir{db: r.db},
			fs.StableAttr{Mode: syscall.S_IFDIR},
		),
		true,
	)

	r.AddChild(
		"narinfos",
		r.NewPersistentInode(
			ctx,
			newNarinfosDir(r.db),
			fs.StableAttr{Mode: syscall.S_IFDIR},
		),
		true,
	)
}

// MountFS mounts the FUSE filesystem at the specified mountpoint.
func MountFS(db *badger.DB, mountpoint string) (*fuse.Server, error) {
	root := NewFilesystem(db)

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:       "narwal-inventory",
			FsName:     "narwal",
			Debug:      false,
			AllowOther: false,
		},
	}

	server, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to mount FUSE filesystem: %w", err)
	}

	return server, nil
}

func stableInode(key []byte) uint64 {
	h := fnv.New64a()

	_, err := h.Write(key)
	if err != nil {
		// should never happen
		panic("failed to hash key:" + err.Error())
	}

	return h.Sum64()
}
