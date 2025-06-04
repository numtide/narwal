package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/numtide/narwal/pkg/mime"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/db"
)

type Object struct {
	Type        db.ObjectType
	Compression db.CompressionType
	Body        io.Reader
	Size        uint64
}

func (s *Store) HasObject(ctx context.Context, path string) (*Object, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)
	entry, err := queries.HasObject(ctx, path)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed to check if object exists: %w", err)
	}

	//nolint:gosec
	return &Object{
		Type:        entry.ObjectType,
		Compression: entry.CompressionType,
		Size:        uint64(entry.Size),
	}, nil
}

func (s *Store) GetObject(ctx context.Context, path string) (*Object, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)
	entry, err := queries.HasObject(ctx, path)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}

	body, err := s.s3.GetObject(ctx, entry.Bucket, path, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object from s3: %w", err)
	}

	//nolint:gosec
	return &Object{
		Type:        entry.ObjectType,
		Compression: entry.CompressionType,
		Size:        uint64(entry.Size), Body: body,
	}, nil
}

func (s *Store) PutObject(
	ctx context.Context,
	path string,
	body io.Reader,
) error {
	analysis, err := analyzePath(path)
	if err != nil {
		return err
	}

	// when hydra uploads to S3 it compress ls and log files with brotli
	// we should ensure the same to preserve the same storage layout for direct reads against S3

	var compression db.CompressionType
	body, compression = s.compressIfRequired(analysis.ObjectType, body)

	// if we are uploading a narinfo we will parse it and store it in the db
	var narinfoBuf *bytes.Buffer

	if analysis.ObjectType == db.ObjectTypeNarinfo {
		// use a tee reader to retain a copy of the narinfo after we uplaod it to s3
		narinfoBuf = new(bytes.Buffer)
		body = io.TeeReader(body, narinfoBuf)
	}

	// put into S3
	putOptions := minio.PutObjectOptions{
		// set the appropriate content type
		ContentType: mime.For(analysis.ObjectType),
	}

	if compression != db.CompressionTypeNone {
		// set the appropriate content encoding if compressed
		putOptions.ContentEncoding = string(compression)
	}

	// put into S3
	objectInfo, err := s.s3.PutObject(ctx, s.bucketName, path, body, -1, putOptions)
	if err != nil {
		return fmt.Errorf("failed to upload object to s3: %w", err)
	}

	// get a connection to the db
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)

	hash, err := hashFromPath(path, analysis.ObjectType)
	if err != nil {
		return fmt.Errorf("failed to get hash from path: %w", err)
	}

	err = queries.PutObject(ctx, db.PutObjectParams{
		Hash:            hash,
		ObjectType:      analysis.ObjectType,
		CompressionType: compression,
		Bucket:          objectInfo.Bucket,
		Path:            objectInfo.Key,
		Size:            objectInfo.Size,
	})
	if err != nil {
		return fmt.Errorf("failed to put object in db: %w", err)
	}

	if narinfoBuf == nil {
		return nil
	}

	return putNarInfo(ctx, queries, hash, narinfoBuf.Bytes())
}

func putNarInfo(ctx context.Context, queries *db.Queries, hash string, buf []byte) error {
	info, err := narinfo.Parse(bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("failed to parse narinfo: %w", err)
	}

	err = queries.PutNarInfo(ctx, db.PutNarInfoParams{
		Hash:        hash,
		StorePath:   info.StorePath,
		Compression: db.CompressionType(info.Compression),
		FileHash:    info.FileHash.String(),
		//nolint:gosec
		FileSize: int64(info.FileSize),
		NarHash:  info.NarHash.String(),
		//nolint:gosec
		NarSize: int64(info.NarSize),
		Deriver: info.Deriver,
	})
	if err != nil {
		return fmt.Errorf("failed to put narinfo in db: %w", err)
	}

	// update signatures
	if err = queries.DeleteNarInfoSignatures(ctx, hash); err != nil {
		return fmt.Errorf("failed to delete narinfo signatures: %w", err)
	}

	signatures := make([]db.InsertNarInfoSignaturesParams, len(info.Signatures))
	for idx, sig := range info.Signatures {
		signatures[idx] = db.InsertNarInfoSignaturesParams{
			Hash: hash,
			Name: sig.Name,
			Data: base64.StdEncoding.EncodeToString(sig.Data),
		}
	}

	if _, err = queries.InsertNarInfoSignatures(ctx, signatures); err != nil {
		return fmt.Errorf("failed to insert narinfo signatures into db: %w", err)
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
		return fmt.Errorf("failed to insert narinfo references into db: %w", err)
	}

	return nil
}

//nolint:nonamedreturns
func (s *Store) compressIfRequired(objectType db.ObjectType, r io.Reader) (body io.Reader, comp db.CompressionType) {
	// hydra is configured with Brotli compression for ls and logs
	switch objectType {
	case db.ObjectTypeLs, db.ObjectTypeLog:
		pr, pw := io.Pipe()

		// todo check what level nix is using
		bw := brotli.NewWriterLevel(pw, brotli.DefaultCompression)

		// compress in a background task
		s.eg.Go(func() error {
			defer func() {
				_ = bw.Close()
				_ = pw.Close()
			}()

			_, err := io.Copy(bw, r)
			if err != nil {
				_ = pw.CloseWithError(err)
			}

			return nil
		})

		body = pr
		comp = db.CompressionTypeBr

	case db.ObjectTypeNar, db.ObjectTypeNarinfo, db.ObjectTypeDebug:
		body = r
		comp = db.CompressionTypeNone
	}

	return body, comp
}
