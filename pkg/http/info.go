package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

const (
	contentTypeNixCacheInfo = "text/x-nix-cache-info"

	// hardcoded for now.
	cacheInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`
)

func (s *Server) addInfoRoutes(r *chi.Mux) {
	r.Get("/nix-cache-info", s.getInfo)
}

func (s *Server) getInfo(w http.ResponseWriter, r *http.Request) {
	_, err := w.Write([]byte(cacheInfo))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", contentTypeNixCacheInfo)
}
