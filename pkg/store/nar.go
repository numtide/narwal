package store

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/db"
)

const (
	ContentTypeNar = "application/x-nix-nar"
)

type NarOptions struct {
	Compression string
}

func (o *NarOptions) compression() db.CompressionType {
	if o.Compression != "" {
		return db.CompressionType(o.Compression)
	}

	return db.CompressionTypeNone
}

func (o *NarOptions) objectName(hash string) string {
	result := "nar/" + hash + ".nar"
	if o.Compression != "" {
		result += "." + o.Compression
	}

	return result
}

func (s *Store) HasNar(ctx context.Context, hash string, options NarOptions) (uint64, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)
	entry, err := queries.HasNar(ctx, db.HasNarParams{
		Hash:        hash,
		Compression: options.compression(),
	})

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, fmt.Errorf("failed to check if nar exists: %w", err)
	}

	//nolint:gosec
	return uint64(entry.Size.Int64), nil
}

//nolint:nonamedreturns
func (s *Store) GetNar(ctx context.Context, hash string, options NarOptions) (body io.Reader, size uint64, err error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)
	entry, err := queries.HasNar(ctx, db.HasNarParams{
		Hash:        hash,
		Compression: options.compression(),
	})

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}

	body, err = s.s3.GetObject(ctx, entry.Bucket, entry.Path, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get object from s3: %w", err)
	}

	//nolint:gosec
	return body, uint64(entry.Size.Int64), nil
}

func (s *Store) PutNar(
	ctx context.Context,
	hash string,
	r io.Reader,
	options NarOptions,
) error {
	info, err := s.s3.PutObject(
		ctx,
		s.bucketName,
		options.objectName(hash),
		r,
		-1,
		minio.PutObjectOptions{ContentType: ContentTypeNar},
	)
	if err != nil {
		return fmt.Errorf("failed to upload nar to s3: %w", err)
	}

	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)

	err = queries.PutNar(ctx, db.PutNarParams{
		Hash:        hash,
		Compression: options.compression(),
		Bucket:      info.Bucket,
		Path:        info.Key,
		Size:        pgtype.Int8{Int64: info.Size, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to put nar in db: %w", err)
	}

	return nil
}
