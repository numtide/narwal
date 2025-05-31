package http

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/numtide/narwal/pkg/store"
)

func (s *Server) addNarRoutes(r *chi.Mux) {
	pattern := "/nar/{hash:[a-z0-9]+}.nar.{compression:*}"

	r.Get(pattern, s.getNar)
	r.Head(pattern, s.hasNar)
	r.Put(pattern, s.putNar)
}

func (s *Server) hasNar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	compression := chi.URLParam(r, "compression")

	size, err := s.store.HasNar(r.Context(), hash, store.NarOptions{
		Compression: compression,
	})

	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "failed to check for nar in store", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", store.ContentTypeNar)
	h.Set("Content-Length", strconv.FormatUint(size, 10))

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getNar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	compression := chi.URLParam(r, "compression")

	body, size, err := s.store.GetNar(r.Context(), hash, store.NarOptions{
		Compression: compression,
	})

	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "failed to get nar from store", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", store.ContentTypeNar)
	h.Set("Content-Length", strconv.FormatUint(size, 10))

	if _, err = io.Copy(w, body); err != nil {
		http.Error(w, "failed to write nar to response", http.StatusInternalServerError)
	}
}

func (s *Server) putNar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	compression := chi.URLParam(r, "compression")

	if err := s.store.PutNar(r.Context(), hash, r.Body, store.NarOptions{
		Compression: compression,
	}); err != nil {
		http.Error(w, "failed to put nar in store", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
