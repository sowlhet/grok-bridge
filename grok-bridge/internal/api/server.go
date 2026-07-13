package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/account"
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
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }
