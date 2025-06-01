package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/allegro/bigcache/v3"
	"github.com/charmbracelet/log"
	"github.com/eko/gocache/lib/v4/cache"
	bc_store "github.com/eko/gocache/store/bigcache/v4"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/config"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db  *pgxpool.Pool
	s3  *minio.Client
	log *log.Logger

	bucketName string

	narInfoCache *cache.LoadableCache[[]byte]
}

func (s *Store) Close() error {
	if err := s.narInfoCache.Close(); err != nil {
		return fmt.Errorf("failed to close cache: %w", err)
	}

	return nil
}

func New(
	cfg *config.Config,
	pgPool *pgxpool.Pool,
	s3 *minio.Client,
) (*Store, error) {
	result := &Store{
		db:         pgPool,
		s3:         s3,
		log:        log.WithPrefix("store"),
		bucketName: cfg.S3.BucketName,
	}

	// create a nar info cache
	// this is a quick poc
	bcClient, err := bigcache.New(context.Background(), bigcache.DefaultConfig(24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("failed to create bigcache: %w", err)
	}

	bcStore := bc_store.NewBigcache(bcClient)

	result.narInfoCache = cache.NewLoadable[[]byte](result.loadNarInfo, cache.New[[]byte](bcStore))

	return result, nil
}
