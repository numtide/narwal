package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/numtide/narwal/pkg/store"
	"golang.org/x/sync/errgroup"

	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
)

type Server struct {
	log *log.Logger
	cfg *config.Server

	store *store.Store

	eg  *errgroup.Group
	srv http.Server
}

func NewServer(
	cfg *config.Server,
	store *store.Store,
) (*Server, error) {
	srv := &Server{
		cfg:   cfg,
		store: store,
		log:   log.WithPrefix("http"),
	}

	return srv, nil
}

func (s *Server) Listen() error {
	// configure router
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(middleware.Timeout(60 * time.Second))

	s.addInfoRoutes(r)
	s.addObjectRoutes(r)

	// start the server
	addr := s.cfg.HTTP.ListenAddr

	s.eg = &errgroup.Group{}

	s.srv = http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	s.log.Info("starting listener", "address", addr)

	s.eg.Go(func() error {
		err := s.srv.ListenAndServe()

		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}

		return err
	})

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	s.log.Info("stopping")

	err := s.srv.Shutdown(ctx)

	if errors.Is(err, http.ErrServerClosed) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to shutdown http server: %w", err)
	}

	if err = s.eg.Wait(); err != nil {
		return fmt.Errorf("http server failure: %w", err)
	}

	s.log.Info("stopped")

	return nil
}
