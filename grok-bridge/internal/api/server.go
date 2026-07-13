package api

import (
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/account"
	"github.com/wlhet/grok-bridge/internal/adminui"
	xaiauth "github.com/wlhet/grok-bridge/internal/auth/xai"
	"github.com/wlhet/grok-bridge/internal/logging"
	"github.com/wlhet/grok-bridge/internal/models"
	"github.com/wlhet/grok-bridge/internal/pipeline"
)

// ServerDeps holds dependencies for the public and admin HTTP server.
type ServerDeps struct {
	Pipeline         *pipeline.Pipeline
	Keys             *access.KeyStore
	Catalog          *models.Catalog
	Accounts         *account.Store
	Logs             *logging.RequestLogStore
	OAuth            *xaiauth.Client
	AdminPassword    string
	AdminSessionTTL  time.Duration
	LogBodies        string
	LogRetentionDays int
}

// Server is the HTTP front-end for grok-bridge.
type Server struct {
	mux              *http.ServeMux
	pipeline         *pipeline.Pipeline
	keys             *access.KeyStore
	catalog          *models.Catalog
	accounts         *account.Store
	logs             *logging.RequestLogStore
	oauth            *xaiauth.Client
	adminPassword    string
	sessions         *sessionStore
	mu               sync.Mutex
	logBodies        string
	logRetentionDays int
}

// NewServer constructs a Server with healthz, public proxy, and admin routes.
func NewServer(deps ServerDeps) *Server {
	ttl := deps.AdminSessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	logBodies := deps.LogBodies
	if logBodies == "" {
		logBodies = "errors_only"
	}
	retention := deps.LogRetentionDays
	if retention == 0 {
		retention = 30
	}
	s := &Server{
		mux:              http.NewServeMux(),
		pipeline:         deps.Pipeline,
		keys:             deps.Keys,
		catalog:          deps.Catalog,
		accounts:         deps.Accounts,
		logs:             deps.Logs,
		oauth:            deps.OAuth,
		adminPassword:    deps.AdminPassword,
		sessions:         newSessionStore(ttl),
		logBodies:        logBodies,
		logRetentionDays: retention,
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.registerPublicRoutes()
	s.registerAdminRoutes()
	s.registerAdminUI()
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

// registerAdminUI serves the embedded SPA at /admin/ and static assets at /admin/static/.
func (s *Server) registerAdminUI() {
	staticRoot, err := fs.Sub(adminui.Static, "static")
	if err != nil {
		// Should not happen with go:embed static/*; surface via empty handler.
		s.mux.HandleFunc("GET /admin/{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "admin ui not available", http.StatusInternalServerError)
		})
		return
	}

	fileServer := http.FileServer(http.FS(staticRoot))

	// Index: GET /admin and GET /admin/
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(staticRoot, "index.html")
		if err != nil {
			http.Error(w, "admin index missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
	s.mux.HandleFunc("GET /admin", serveIndex)
	s.mux.HandleFunc("GET /admin/{$}", serveIndex)
	s.mux.HandleFunc("GET /admin/index.html", serveIndex)

	// Static assets under /admin/static/*
	s.mux.Handle("GET /admin/static/", http.StripPrefix("/admin/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent directory listing; FileServer handles missing files.
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		// no-cache is safer for v1 admin assets.
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})))
}
