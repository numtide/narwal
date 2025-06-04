package http

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/numtide/narwal/pkg/db"

	"github.com/go-chi/chi/v5"
	"github.com/numtide/narwal/pkg/mime"
	"github.com/numtide/narwal/pkg/store"
)

func (s *Server) addObjectRoutes(r *chi.Mux) {
	pattern := "/*"

	r.Get(pattern, s.getObject)
	r.Head(pattern, s.hasObject)
	r.Put(pattern, s.putObject)
}

func setObjectResponseHeaders(w http.ResponseWriter, obj *store.Object) {
	h := w.Header()
	h.Set("Content-Type", mime.For(obj.Type))
	h.Set("Content-Length", strconv.FormatUint(obj.Size, 10))

	if obj.Compression != db.CompressionTypeNone {
		h.Set("Content-Encoding", string(obj.Compression))
	}
}

func (s *Server) hasObject(w http.ResponseWriter, r *http.Request) {
	path := r.RequestURI[1:] // strip leading '/'
	obj, err := s.store.HasObject(r.Context(), path)

	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "failed to check for nar in store", http.StatusInternalServerError)
		return
	}

	setObjectResponseHeaders(w, obj)

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getObject(w http.ResponseWriter, r *http.Request) {
	path := r.RequestURI[1:] // strip leading '/'
	obj, err := s.store.GetObject(r.Context(), path)

	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "failed to get object from store", http.StatusInternalServerError)
		return
	}

	setObjectResponseHeaders(w, obj)

	if _, err = io.Copy(w, obj.Body); err != nil {
		http.Error(w, "failed to write nar to response", http.StatusInternalServerError)
	}
}

func (s *Server) putObject(w http.ResponseWriter, r *http.Request) {
	path := r.RequestURI[1:] // strip leading '/'
	if err := s.store.PutObject(r.Context(), path, r.Body); err != nil {
		s.log.Error("failed to put object in store", "path", path, "error", err)
		http.Error(w, "internal failure", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
