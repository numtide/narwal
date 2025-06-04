package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/andybalholm/brotli"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/numtide/narwal/pkg/mime"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/db"
)

var objectTypeRegex = regexp.MustCompile(`\.(nar|narinfo|debug|ls|)(\.(xz|bzip2|gzip|zstd))?$`)

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
	typeAndCompression, err := parseObjectTypeAndCompression(path)
	if err != nil {
		return err
	}

	compression := typeAndCompression.Compression

	body, compression = s.compressIfRequired(typeAndCompression.Type, body)

	var buf *bytes.Buffer

	if typeAndCompression.Type == db.ObjectTypeNarinfo {
		// retain a copy of the narinfo after uplaod to s3
		buf = new(bytes.Buffer)
		body = io.TeeReader(body, buf)
	}

	putOptions := minio.PutObjectOptions{
		ContentType: mime.For(typeAndCompression.Type),
	}

	if typeAndCompression.Compression != db.CompressionTypeNone {
		putOptions.ContentEncoding = string(compression)
	}

	objectInfo, err := s.s3.PutObject(
		ctx,
		s.bucketName,
		path,
		body,
		-1,
		putOptions,
	)
	if err != nil {
		return fmt.Errorf("failed to upload object to s3: %w", err)
	}

	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := db.New(conn)

	var hash string

	switch typeAndCompression.Type {
	case db.ObjectTypeNar:
		// path is prefixed with /nar
		hash = path[4:56]
	case db.ObjectTypeDebug:
		hash = path[10:50]
	default:
		hash = path[:32]
	}

	err = queries.PutObject(ctx, db.PutObjectParams{
		Hash:            hash,
		ObjectType:      typeAndCompression.Type,
		CompressionType: compression,
		Bucket:          objectInfo.Bucket,
		Path:            objectInfo.Key,
		Size:            objectInfo.Size,
	})
	if err != nil {
		return fmt.Errorf("failed to put nar in db: %w", err)
	}

	if buf != nil {
		info, err := narinfo.Parse(bytes.NewReader(buf.Bytes()))
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
			return fmt.Errorf("failed to put nar in db: %w", err)
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
	}

	return nil
}

func (s *Store) compressIfRequired(objectType db.ObjectType, r io.Reader) (body io.Reader, compression db.CompressionType) {
	// hydra is configured with Brotli compression for ls and logs

	switch objectType {
	case db.ObjectTypeLs, db.ObjectTypeLog:

		pr, pw := io.Pipe()

		// todo check what level nix is using
		bw := brotli.NewWriterLevel(pw, brotli.DefaultCompression)

		// compress in a background task
		s.eg.Go(func() error {
			defer pw.Close()
			defer bw.Close()

			_, err := io.Copy(bw, r)
			if err != nil {
				_ = pw.CloseWithError(err)
			}

			return nil
		})

		body = pr
		compression = db.CompressionTypeBr

	default:
		body = r
		compression = db.CompressionTypeNone
	}

	return body, compression
}

func parseObjectTypeAndCompression(path string) (*Object, error) {
	matches := objectTypeRegex.FindStringSubmatch(path)

	if len(matches) <= 1 {
		return nil, fmt.Errorf("could not parse object type: %s", path)
	}

	result := &Object{
		Compression: db.CompressionTypeNone,
	}

	if path[:4] == "log/" {
		// logs are written with the .drv extension and under the `log/` prefix
		result.Type = db.ObjectTypeLog
	} else if path[:10] == "debuginfo/" {
		// some entries have the `.debug` suffix, others don't, so we force the type based on the prefix
		result.Type = db.ObjectTypeDebug
	} else {
		result.Type = db.ObjectType(matches[1])
	}

	if len(matches) == 4 && matches[3] != "" {
		result.Compression = db.CompressionType(matches[3])
	}

	return result, nil
}
