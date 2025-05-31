package http

import (
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/pg"
	"net/http"
	"regexp"
	"strconv"
)

const (
	contentTypeNar = "application/x-nix-nar"
)

var (
	narNameRegex = regexp.MustCompile(`^([a-z0-9]+).nar.(.*)$`)
)

func (s *Server) addNar() {
	s.echo.GET("/nar/:name", s.getNar)
	s.echo.HEAD("/nar/:name", s.getNar)
	s.echo.PUT("/nar/:name", s.putNar)
}

func (s *Server) getNar(c echo.Context) error {
	var name string

	err := echo.PathParamsBinder(c).
		String("name", &name).
		BindError()

	if err != nil {
		return fmt.Errorf("failed to bind path param: %w", err)
	}

	hash, _, err := parseNarName(name)
	if err != nil {
		return fmt.Errorf("failed to parse nar name: %w", err)
	}

	ctx := c.Request().Context()

	conn, err := s.pgPool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := pg.New(conn)
	entry, err := queries.NarExists(ctx, hash)

	if errors.Is(err, pgx.ErrNoRows) {
		return c.NoContent(http.StatusNotFound)
	}

	// todo is there a better echo API for this?
	h := c.Response().Header()
	h.Set("Content-Type", contentTypeNar)
	h.Set("Content-Length", strconv.FormatUint(uint64(entry.Size.Int64), 10))

	if c.Request().Method == http.MethodHead {
		return c.NoContent(http.StatusOK)
	}

	object, err := s.s3Client.GetObject(ctx, entry.Bucket, entry.Path, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to get object from s3: %w", err)
	}

	return c.Stream(http.StatusOK, contentTypeNar, object)
}

func (s *Server) putNar(c echo.Context) error {
	var name string

	err := echo.PathParamsBinder(c).
		String("name", &name).
		BindError()

	if err != nil {
		return fmt.Errorf("failed to bind path param: %w", err)
	}

	hash, _, err := parseNarName(name)
	if err != nil {
		return fmt.Errorf("failed to parse nar name: %w", err)
	}

	ctx := c.Request().Context()

	// TODO what to do if it already exists
	info, err := s.s3Client.PutObject(
		ctx,
		s.cfg.S3.BucketName,
		c.Request().RequestURI,
		c.Request().Body,
		-1,
		minio.PutObjectOptions{
			ContentType:  contentTypeNar,
			AutoChecksum: minio.ChecksumSHA256,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to upload nar to s3: %w", err)
	}

	conn, err := s.pgPool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire db connection: %w", err)
	}
	defer conn.Release()

	queries := pg.New(conn)

	err = queries.PutNar(ctx, pg.PutNarParams{
		Hash:   hash,
		Bucket: info.Bucket,
		Path:   info.Key,
		Size:   pgtype.Int8{Int64: info.Size, Valid: true},
	})

	if err != nil {
		return fmt.Errorf("failed to put nar in db: %w", err)
	}

	return c.NoContent(http.StatusOK)
}

func parseNarName(name string) (hash string, compression string, err error) {
	matches := narNameRegex.FindStringSubmatch(name)
	if matches == nil {
		return "", "", fmt.Errorf("invalid nar name: %s", name)
	}

	return matches[1], matches[2], nil
}
