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
// ProxySettings is a runtime snapshot of scheduling/proxy knobs applied live.
type ProxySettings struct {
	HTTPProxy           string
	Scheduling          string
	MaxConcurrency      int
	AccountConcurrency  int
	MaxAccountSwitches  int
	MaxTransientRetries int
}

// OnProxySettingsFunc is called when admin updates proxy/scheduling/concurrency settings.
type OnProxySettingsFunc func(ProxySettings)

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
	// Runtime proxy/scheduling (optional; defaults applied).
	HTTPProxy           string
	Scheduling          string
	MaxConcurrency      int
	AccountConcurrency  int
	MaxAccountSwitches  int
	MaxTransientRetries int
	OnProxySettings     OnProxySettingsFunc
}

// Server is the HTTP front-end for grok-bridge.
type Server struct {
	// mux is the combined handler (public + admin) used when admin_listen is empty.
	mux *http.ServeMux
	// publicMux serves healthz + proxy only (split-listen public side).
	publicMux *http.ServeMux
	// adminMux serves admin API + UI (and healthz) for split-listen admin side.
	adminMux *http.ServeMux

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

	httpProxy           string
	scheduling          string
	maxConcurrency      int
	accountConcurrency  int
	maxAccountSwitches  int
	maxTransientRetries int
	onProxySettings     OnProxySettingsFunc
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
	scheduling := deps.Scheduling
	if scheduling == "" {
		scheduling = "round_robin"
	}
	maxSwitches := deps.MaxAccountSwitches
	if maxSwitches == 0 {
		maxSwitches = 2
	}
	maxTransient := deps.MaxTransientRetries
	if maxTransient == 0 {
		maxTransient = 2
	}
	s := &Server{
		mux:                 http.NewServeMux(),
		publicMux:           http.NewServeMux(),
		adminMux:            http.NewServeMux(),
		pipeline:            deps.Pipeline,
		keys:                deps.Keys,
		catalog:             deps.Catalog,
		accounts:            deps.Accounts,
		logs:                deps.Logs,
		oauth:               deps.OAuth,
		adminPassword:       deps.AdminPassword,
		sessions:            newSessionStore(ttl),
		logBodies:           logBodies,
		logRetentionDays:    retention,
		httpProxy:           deps.HTTPProxy,
		scheduling:          scheduling,
		maxConcurrency:      deps.MaxConcurrency,
		accountConcurrency:  deps.AccountConcurrency,
		maxAccountSwitches:  maxSwitches,
		maxTransientRetries: maxTransient,
		onProxySettings:     deps.OnProxySettings,
	}
	// Combined mux (single-port mode).
	s.mux.HandleFunc("GET /healthz", healthz)
	s.registerPublicRoutesOn(s.mux)
	s.registerAdminRoutesOn(s.mux)
	s.registerAdminUIOn(s.mux)

	// Split public mux.
	s.publicMux.HandleFunc("GET /healthz", healthz)
	s.registerPublicRoutesOn(s.publicMux)

	// Split admin mux (healthz useful for probes on admin port too).
	s.adminMux.HandleFunc("GET /healthz", healthz)
	s.registerAdminRoutesOn(s.adminMux)
	s.registerAdminUIOn(s.adminMux)
	return s
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Handler returns the combined HTTP handler (public + admin on one port).
func (s *Server) Handler() http.Handler { return s.mux }

// PublicHandler returns the public API handler (no admin routes).
func (s *Server) PublicHandler() http.Handler { return s.publicMux }

// AdminHandler returns the admin API + UI handler.
func (s *Server) AdminHandler() http.Handler { return s.adminMux }

// LogRetentionDays returns the current retention setting (for background purge).
func (s *Server) LogRetentionDays() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logRetentionDays == 0 {
		return 30
	}
	return s.logRetentionDays
}

// SetAdminPassword updates the in-memory admin password (runtime settings).
func (s *Server) SetAdminPassword(pw string) {
	s.mu.Lock()
	s.adminPassword = pw
	s.mu.Unlock()
}

// AdminPassword returns the current admin password (for tests).
func (s *Server) AdminPassword() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adminPassword
}

// registerAdminUIOn serves the embedded SPA at /admin/ and static assets at /admin/static/.
func (s *Server) registerAdminUIOn(mux *http.ServeMux) {
	staticRoot, err := fs.Sub(adminui.Static, "static")
	if err != nil {
		// Should not happen with go:embed static/*; surface via empty handler.
		mux.HandleFunc("GET /admin/{$}", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("GET /admin", serveIndex)
	mux.HandleFunc("GET /admin/{$}", serveIndex)
	mux.HandleFunc("GET /admin/index.html", serveIndex)

	// Static assets under /admin/static/*
	mux.Handle("GET /admin/static/", http.StripPrefix("/admin/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) snapshotProxySettingsLocked() ProxySettings {
	return ProxySettings{
		HTTPProxy:           s.httpProxy,
		Scheduling:          s.scheduling,
		MaxConcurrency:      s.maxConcurrency,
		AccountConcurrency:  s.accountConcurrency,
		MaxAccountSwitches:  s.maxAccountSwitches,
		MaxTransientRetries: s.maxTransientRetries,
	}
}

