package http

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func (s *Server) initHealth() {
	s.echo.GET("/health", s.getHealth)
}

func (s *Server) getHealth(c echo.Context) error {
	//nolint:wrapcheck
	return c.JSONPretty(http.StatusOK, map[string]string{
		"status": "ok",
	}, "  ")
}
