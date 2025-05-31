package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/numtide/nix-binary-cache/pkg/config"
)

type Server struct {
	log *log.Logger
	cfg *config.Config

	echo *echo.Echo
}

func NewServer(
	cfg *config.Config,
) (*Server, error) {
	srv := &Server{
		cfg: cfg,
		log: log.WithPrefix("http"),
	}

	srv.init()

	return srv, nil
}

func (s *Server) Listen() error {
	addr := s.cfg.HTTP.ListenAddr

	s.log.Info("starting server", "address", addr)

	// start server
	err := s.echo.Start(addr)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("failed to start echo server: %w", err)
	}

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	s.log.Info("shutting down server")

	if err := s.echo.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to stop http server: %w", err)
	}

	s.log.Info("server shutdown")

	return nil
}

func (s *Server) logRequest(_ echo.Context, v middleware.RequestLoggerValues) error {
	if v.Error == nil {
		s.log.Info("http_request", "uri", v.URI, "status", v.Status)
	} else {
		s.log.Error("http_request_error", "uri", v.URI, "status", v.Status, "err", v.Error.Error())
	}

	return nil
}

func (s *Server) init() {
	s.echo = echo.New()
	s.echo.HideBanner = true

	// middleware
	s.echo.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:     true,
		LogURI:        true,
		LogError:      true,
		HandleError:   true,
		LogValuesFunc: s.logRequest,
	}))

	// register routes
	s.initHealth()
}
