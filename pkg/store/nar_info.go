package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/numtide/narwal/pkg/db"
)

const (
	ContentTypeNarInfo = "text/x-nix-narinfo"
)

//nolint:nonamedreturns
func (s *Store) HasNarInfo(ctx context.Context, hash string) (size uint32, err error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)
	entry, err := queries.HasNarInfo(ctx, hash)

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, fmt.Errorf("failed to check if nar exists: %w", err)
	}

	//nolint:gosec
	return uint32(entry.Size), nil
}

//nolint:nonamedreturns
func (s *Store) GetNarInfo(ctx context.Context, hash string) (body io.Reader, size uint32, err error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)
	entry, err := queries.HasNarInfo(ctx, hash)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}

	object, err := s.s3.GetObject(ctx, entry.Bucket, entry.Path, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get object from s3: %w", err)
	}

	//nolint:gosec
	return object, uint32(entry.Size), nil
}

func (s *Store) PutNarInfo(
	ctx context.Context,
	hash string,
	r io.Reader,
) error {
	// parse nar info
	buf := new(bytes.Buffer)
	teeReader := io.TeeReader(r, buf)

	info, err := narinfo.Parse(teeReader)
	if err != nil {
		return fmt.Errorf("failed to parse narinfo: %w", err)
	}

	object, err := s.s3.PutObject(
		ctx,
		s.bucketName,
		hash+".narinfo", // we want to mimic the existing layout in the S3 bucket
		bytes.NewReader(buf.Bytes()),
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

	err = queries.PutNarInfo(ctx, db.PutNarInfoParams{
		Hash:        hash,
		StorePath:   info.StorePath,
		Compression: db.CompressionType(info.Compression),
		FileHash:    info.FileHash.String(),
		//nolint:gosec
		FileSize: pgtype.Int8{Int64: int64(info.FileSize), Valid: true},
		NarHash:  info.NarHash.String(),
		//nolint:gosec
		NarSize: pgtype.Int8{Int64: int64(info.NarSize), Valid: true},
		Deriver: info.Deriver,
		Bucket:  object.Bucket,
		Path:    object.Key,
		//nolint:gosec
		Size: int32(buf.Len()),
	})
	if err != nil {
		return fmt.Errorf("failed to put nar in db: %w", err)
	}

	// update signatures
	if err = queries.DeleteNarInfoSignatures(ctx, hash); err != nil {
		return fmt.Errorf("failed to delete narinfo signatures: %w", err)
	}

	signatures := make([]db.InsertNarInfoSignaturesParams, len(info.Signatures))
	for idx, sig := range info.Signatures {
		signatures[idx] = db.InsertNarInfoSignaturesParams{
			Hash:      hash,
			Signature: sig.String(),
		}
	}

	if _, err = queries.InsertNarInfoSignatures(ctx, signatures); err != nil {
		return fmt.Errorf("failed to insert nar info signatures into db: %w", err)
	}

	// update references
	references := make([]db.InsertNarInfoReferencesParams, len(info.References))
	for idx, ref := range info.References {
		references[idx] = db.InsertNarInfoReferencesParams{
			Hash:     hash,
			RefersTo: ref[:32],
		}
	}

	if _, err = queries.InsertNarInfoReferences(ctx, references); err != nil {
		return fmt.Errorf("failed to insert nar info references into db: %w", err)
	}

	return nil
}
