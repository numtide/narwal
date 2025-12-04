package server

import (
	"context"
	"fmt"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/awssdk/sqs"
	"github.com/numtide/narwal/pkg/config"
)

const (
	dbConnectTimeout = 10 * time.Second
)

type Server struct {
	s3           *awssdk.BucketClient
	pgPool       *pgxpool.Pool
	uploadEvents *sqs.S3EventQueue
}

func NewServer(cfg *config.Server) (*Server, error) {
	srv := &Server{}

	var err error

	ctx, cancel := context.WithTimeout(context.Background(), dbConnectTimeout)
	defer cancel()

	// Connect to postgres and migrate the database
	if srv.pgPool, err = cfg.Postgres.Connect(ctx, true); err != nil {
		//nolint:wrapcheck
		return nil, err
	}

	// Create s3 client
	if srv.s3, err = awssdk.NewS3Client(ctx, &cfg.AWS, &cfg.S3); err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	// Create SQS subscription for upload events
	sqsClient, err := sqs.NewClient(ctx, &cfg.AWS)
	if err != nil {
		return nil, fmt.Errorf("failed to create SQS client: %w", err)
	}

	if srv.uploadEvents, err = sqs.NewS3EventQueue(ctx, sqsClient, &cfg.SQS); err != nil {
		return nil, fmt.Errorf("failed to create client for S3 upload event queue: %w", err)
	}

	return srv, nil
}

func (s *Server) Run(ctx context.Context) error {
	return s.listenToS3(ctx)
}

// ImportManifestForTest exports importManifest for testing.
func ImportManifestForTest(ctx context.Context, inventoryDB *badger.DB, pgPool *pgxpool.Pool, report string) error {
	return importManifest(ctx, inventoryDB, pgPool, report)
}
