package server

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/numtide/nix-binary-cache/pkg/config"
	"github.com/numtide/nix-binary-cache/pkg/http"
	"golang.org/x/sync/errgroup"
)

type Server struct {
	log    *log.Logger
	config *config.Config

	http *http.Server

	eg *errgroup.Group // for background tasks
}

func NewServer(cfg *config.Config) (*Server, error) {
	srv := &Server{
		log:    log.WithPrefix("server"),
		config: cfg,
	}

	var err error

	if srv.http, err = http.NewServer(cfg); err != nil {
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
