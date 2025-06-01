package http

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/numtide/narwal/pkg/store"
)

func (s *Server) addNarInfoRoutes(r *chi.Mux) {
	pattern := "/{hash:[a-z0-9]+}.narinfo"

	r.Get(pattern, s.getNarInfo)
	r.Head(pattern, s.hasNarInfo)
	r.Put(pattern, s.putNarInfo)
}

func (s *Server) hasNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	size, err := s.store.HasNarInfo(r.Context(), hash, store.NarInfoOptions{
		UseCache: true,
	})

	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "failed to check for nar info in store", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", store.ContentTypeNarInfo)
	h.Set("Content-Length", strconv.FormatUint(uint64(size), 10))

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	body, size, err := s.store.GetNarInfo(r.Context(), hash, store.NarInfoOptions{
		UseCache: true,
	})

	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "failed to get nar info from store", http.StatusInternalServerError)
	}

	h := w.Header()
	h.Set("Content-Type", store.ContentTypeNarInfo)
	h.Set("Content-Length", strconv.FormatUint(uint64(size), 10))

	if _, err = io.Copy(w, body); err != nil {
		http.Error(w, "failed to write nar info to response", http.StatusInternalServerError)
		return
	}
}

func (s *Server) putNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	if err := s.store.PutNarInfo(r.Context(), hash, r.Body); err != nil {
		http.Error(w, "failed to put nar info in store", http.StatusInternalServerError)
	}

	w.WriteHeader(http.StatusNoContent)
}
