package http

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/numtide/narwal/pkg/store"
)

func (s *Server) addNarRoutes(r *chi.Mux) {
	r.Route("/nar", func(r chi.Router) {
		pattern := `/{hash:\w{52}}.{extension}`

		r.Get(pattern, s.getNar)
		r.Head(pattern, s.hasNar)
		r.Put(pattern, s.putNar)
	})
}

//nolint:nonamedreturns
func getNarParams(r *http.Request) (hash string, compression string, err error) {
	hash = chi.URLParam(r, "hash")
	extension := chi.URLParam(r, "extension")

	if !strings.HasPrefix(extension, "nar") {
		return "", "", fmt.Errorf("invalid extension: %s", extension)
	}

	if strings.HasPrefix(extension, "nar.") && len(extension) > 4 {
		compression = extension[4:]
	}

	return hash, compression, nil
}

func (s *Server) hasNar(w http.ResponseWriter, r *http.Request) {
	hash, compression, err := getNarParams(r)
	if err != nil {
		http.Error(w, "invalid nar url", http.StatusBadRequest)
		return
	}

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
	hash, compression, err := getNarParams(r)
	if err != nil {
		http.Error(w, "invalid nar url", http.StatusBadRequest)
		return
	}

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
	hash, compression, err := getNarParams(r)
	if err != nil {
		http.Error(w, "invalid nar url", http.StatusBadRequest)
		return
	}

	if err := s.store.PutNar(r.Context(), hash, r.Body, store.NarOptions{
		Compression: compression,
	}); err != nil {
		http.Error(w, "failed to put nar in store", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
