package server

import (
	"context"
	"runtime"
	"time"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/config"
	"golang.org/x/sync/errgroup"
)

const (
	dbConnectTimeout = 10 * time.Second
)

type Server struct {
	log    *log.Logger
	config *config.Server

	pgPool *pgxpool.Pool

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

	return srv, nil
}

func (s *Server) Start(_ context.Context) error {
	s.log.Info("started")

	return nil
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	// wait for background tasks to finish
	if err := s.eg.Wait(); err != nil {
		s.log.Error("failure occurred waiting for background tasks", "err", err)
	}

	s.log.Info("stopped")

	return nil
}
