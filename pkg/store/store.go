package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/numtide/narwal/pkg/config"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db         *pgxpool.Pool
	s3         *minio.Client
	bucketName string
}

func New(cfg *config.Config, pgPool *pgxpool.Pool, s3 *minio.Client) *Store {
	return &Store{
		db:         pgPool,
		s3:         s3,
		bucketName: cfg.S3.BucketName,
	}
}
