package http

import (
	"bufio"
	"bytes"
	"net/http"

	"github.com/labstack/echo/v4"
)

const (
	contentTypeNixCacheInfo = "text/x-nix-cache-info"

	// hardcoded for now
	cacheInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`
)

func (s *Server) addInfo() {
	s.echo.GET("/nix-cache-info", s.getInfo)
}

func (s *Server) getInfo(c echo.Context) error {
	return c.Stream(http.StatusOK, contentTypeNixCacheInfo, bufio.NewReader(bytes.NewReader([]byte(cacheInfo))))
}
