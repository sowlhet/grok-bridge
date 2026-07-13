package api

import (
	"net/http"

	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/models"
	"github.com/wlhet/grok-bridge/internal/pipeline"
)

// ServerDeps holds dependencies for the public (and later admin) HTTP server.
type ServerDeps struct {
	Pipeline *pipeline.Pipeline
	Keys     *access.KeyStore
	Catalog  *models.Catalog
}

// Server is the HTTP front-end for grok-bridge.
type Server struct {
	mux      *http.ServeMux
	pipeline *pipeline.Pipeline
	keys     *access.KeyStore
	catalog  *models.Catalog
}

// NewServer constructs a Server with healthz and public proxy routes.
func NewServer(deps ServerDeps) *Server {
	s := &Server{
		mux:      http.NewServeMux(),
		pipeline: deps.Pipeline,
		keys:     deps.Keys,
		catalog:  deps.Catalog,
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.registerPublicRoutes()
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }
