package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/numtide/narwal/pkg/db"
	"github.com/numtide/narwal/pkg/store"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
	"github.com/numtide/narwal/pkg/http"
	"golang.org/x/sync/errgroup"
)

const (
	dbConnectTimeout = 10 * time.Second
)

type Server struct {
	log    *log.Logger
	config *config.Config

	pgPool   *pgxpool.Pool
	s3Client *minio.Client

	http  *http.Server
	store *store.Store

	eg *errgroup.Group // for background tasks
}

func NewServer(cfg *config.Config) (*Server, error) {
	srv := &Server{
		log:    log.WithPrefix("server"),
		config: cfg,
	}

	var err error

	ctx, cancel := context.WithTimeout(context.Background(), dbConnectTimeout)
	defer cancel()

	// connect to postgres
	if srv.pgPool, err = db.Connect(ctx, cfg.Postgres.URL); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// connect to s3
	if srv.s3Client, err = minio.New(cfg.S3.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3.AccessKey, cfg.S3.SecretKey, ""),
		Secure: cfg.S3.SSLEnabled,
	}); err != nil {
		return nil, fmt.Errorf("failed to connect to s3: %w", err)
	}

	// create a store
	srv.store = store.New(cfg, srv.pgPool, srv.s3Client)

	// create http server
	if srv.http, err = http.NewServer(cfg, srv.store); err != nil {
		return nil, fmt.Errorf("failed to create http server: %w", err)
	}

	return srv, nil
}

func (s *Server) Start(_ context.Context) error {
	s.eg = &errgroup.Group{}

	// start the http server
	s.eg.Go(s.http.Listen)

	s.log.Info("started")

	return nil
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	// stop http server
	if err := s.http.Stop(ctx); err != nil {
		s.log.Error("failed to stop http server", "err", err)
	}

	// wait for background tasks to finish
	if err := s.eg.Wait(); err != nil {
		s.log.Error("failure occurred waiting for background tasks", "err", err)
	}

	s.log.Info("stopped")

	return nil
}
