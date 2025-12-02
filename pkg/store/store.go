package store

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/eko/gocache/lib/v4/cache"

	"golang.org/x/sync/errgroup"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/config"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db       *pgxpool.Pool
	s3       *s3.Client
	uploader *manager.Uploader
	log      *log.Logger

	eg *errgroup.Group

	bucketName string

	cache *cache.LoadableCache[[]byte]
}

func (s *Store) Close() error {
	return nil
}

func New(
	cfg *config.Server,
	pgPool *pgxpool.Pool,
	s3Client *s3.Client,
	eg *errgroup.Group,
) (*Store, error) {
	result := &Store{
		db:         pgPool,
		s3:         s3Client,
		uploader:   manager.NewUploader(s3Client),
		eg:         eg,
		log:        log.WithPrefix("store"),
		bucketName: cfg.S3.Bucket,
	}

	if err := result.initObjectCache(); err != nil {
		return nil, fmt.Errorf("failed to initialize object cache: %w", err)
	}

	return result, nil
}
