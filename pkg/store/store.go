package store

import (
	"errors"

	"golang.org/x/sync/errgroup"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/config"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db  *pgxpool.Pool
	s3  *minio.Client
	log *log.Logger

	eg *errgroup.Group

	bucketName string
}

func (s *Store) Close() error {
	return nil
}

func New(
	cfg *config.Server,
	pgPool *pgxpool.Pool,
	s3 *minio.Client,
	eg *errgroup.Group,
) (*Store, error) {
	result := &Store{
		db:         pgPool,
		s3:         s3,
		eg:         eg,
		log:        log.WithPrefix("store"),
		bucketName: cfg.S3.BucketName,
	}

	return result, nil
}
