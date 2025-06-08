package server

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/awssdk"
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
	config *config.Server

	pgPool   *pgxpool.Pool
	s3Client *awssdk.BucketClient

	http  *http.Server
	store *store.Store

	eg *errgroup.Group // for background tasks
}

func NewServer(cfg *config.Server) (*Server, error) {
	// create an errgroup for background tasks
	// constrain the max number of tasks
	eg := &errgroup.Group{}
	eg.SetLimit(runtime.NumCPU())

	srv := &Server{
		log:    log.WithPrefix("server"),
		config: cfg,
		eg:     eg,
	}

	var err error

	ctx, cancel := context.WithTimeout(context.Background(), dbConnectTimeout)
	defer cancel()

	// connect to postgres and migrate the database
	if srv.pgPool, err = cfg.Postgres.Connect(ctx, true); err != nil {
		//nolint:wrapcheck
		return nil, err
	}

	// connect to s3
	if srv.s3Client, err = cfg.S3.Connect(ctx); err != nil {
		//nolint:wrapcheck
		return nil, err
	}

	// create a store (pass underlying minio client for now)
	if srv.store, err = store.New(cfg, srv.pgPool, srv.s3Client.UnderlyingClient(), srv.eg); err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// create http server
	if srv.http, err = http.NewServer(cfg, srv.store); err != nil {
		return nil, fmt.Errorf("failed to create http server: %w", err)
	}

	return srv, nil
}

func (s *Server) Start(_ context.Context) error {
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

	// close the store
	if err := s.store.Close(); err != nil {
		s.log.Error("failed to close store", "err", err)
	}

	s.log.Info("stopped")

	return nil
}
