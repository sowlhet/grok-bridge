package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/account"
	"github.com/wlhet/grok-bridge/internal/api"
	xaiauth "github.com/wlhet/grok-bridge/internal/auth/xai"
	"github.com/wlhet/grok-bridge/internal/config"
	dbpkg "github.com/wlhet/grok-bridge/internal/db"
	xai "github.com/wlhet/grok-bridge/internal/executor/xai"
	"github.com/wlhet/grok-bridge/internal/logging"
	"github.com/wlhet/grok-bridge/internal/models"
	"github.com/wlhet/grok-bridge/internal/httpproxy"
	"github.com/wlhet/grok-bridge/internal/pipeline"
	rt "github.com/wlhet/grok-bridge/internal/runtime"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config YAML")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cfg.ApplyEnv()

	sqlitePath := cfg.Data.SQLitePath
	if sqlitePath == "" {
		sqlitePath = filepath.Join("data", "grok-bridge.db")
	}

	ctx := context.Background()
	db, err := dbpkg.Open(sqlitePath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := dbpkg.Migrate(ctx, db); err != nil {
		log.Fatalf("migrate db: %v", err)
	}

	// Runtime password updated via admin settings (when env not set).
	if os.Getenv("GROK_BRIDGE_ADMIN_PASSWORD") == "" {
		if v, ok := loadSetting(ctx, db, "admin_password"); ok && strings.TrimSpace(v) != "" {
			cfg.Admin.Password = v
		}
	}
	if err := requireStrongAdminPassword(cfg.Admin.Password); err != nil {
		log.Fatal(err)
	}

	// Overlay runtime proxy settings if present.
	if v, ok := loadSetting(ctx, db, "log_bodies"); ok && v != "" {
		cfg.Proxy.LogBodies = v
	}
	if v, ok := loadSetting(ctx, db, "log_retention_days"); ok && v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.Proxy.LogRetentionDays = n
		}
	}
	if v, ok := loadSetting(ctx, db, "http_proxy"); ok {
		cfg.Proxy.HTTPProxy = v
	}
	if v, ok := loadSetting(ctx, db, "scheduling"); ok && v != "" {
		cfg.Proxy.Scheduling = v
	}
	if v, ok := loadSetting(ctx, db, "max_concurrency"); ok && v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.Proxy.MaxConcurrency = n
		}
	}
	if v, ok := loadSetting(ctx, db, "account_concurrency"); ok && v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.Proxy.AccountConcurrency = n
		}
	}
	if v, ok := loadSetting(ctx, db, "max_account_switches"); ok && v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.Proxy.Retry.MaxAccountSwitches = n
		}
	}
	if v, ok := loadSetting(ctx, db, "max_transient_retries"); ok && v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.Proxy.Retry.MaxTransientRetries = n
		}
	}

	accStore := &account.Store{DB: db}
	keyStore := &access.KeyStore{DB: db}
	logStore := &logging.RequestLogStore{DB: db}
	catalog := models.NewFromConfig(cfg)
	picker := &account.Picker{Store: accStore, Scheduling: cfg.Proxy.Scheduling}

	upstreamClient, err := httpproxy.NewClient(cfg.Proxy.HTTPProxy, 120*time.Second)
	if err != nil {
		log.Fatalf("upstream http client: %v", err)
	}
	oauthHTTP, err := httpproxy.NewTimeoutClient(cfg.Proxy.HTTPProxy, 30*time.Second)
	if err != nil {
		log.Fatalf("oauth http client: %v", err)
	}

	oauth := &xaiauth.Client{HTTP: oauthHTTP}
	xaiClient := &xai.Client{HTTP: upstreamClient}
	limiter := rt.NewLimiter(cfg.Proxy.MaxConcurrency, cfg.Proxy.AccountConcurrency)

	p := &pipeline.Pipeline{
		Accounts:     picker,
		AccountStore: accStore,
		XAI:          xaiClient,
		OAuth:        oauth,
		Catalog:      catalog,
		Logs:         logStore,
		Retry:        cfg.Proxy.Retry,
		LogBodies:    cfg.Proxy.LogBodies,
		Limiter:      limiter,
	}

	sessionTTL, err := time.ParseDuration(cfg.Admin.SessionTTL)
	if err != nil {
		sessionTTL = 24 * time.Hour
	}

	applyProxy := func(ps api.ProxySettings) {
		picker.SetScheduling(ps.Scheduling)
		limiter.Configure(ps.MaxConcurrency, ps.AccountConcurrency)
		p.Retry.MaxAccountSwitches = ps.MaxAccountSwitches
		p.Retry.MaxTransientRetries = ps.MaxTransientRetries
		// Rebuild HTTP clients when proxy URL changes.
		up, err := httpproxy.NewClient(ps.HTTPProxy, 120*time.Second)
		if err != nil {
			log.Printf("warn: invalid http_proxy %q: %v", ps.HTTPProxy, err)
			return
		}
		oauthC, err := httpproxy.NewTimeoutClient(ps.HTTPProxy, 30*time.Second)
		if err != nil {
			log.Printf("warn: invalid http_proxy for oauth %q: %v", ps.HTTPProxy, err)
			return
		}
		xaiClient.HTTP = up
		oauth.HTTP = oauthC
		log.Printf("proxy settings applied: scheduling=%s max_concurrency=%d account_concurrency=%d switches=%d http_proxy=%q",
			ps.Scheduling, ps.MaxConcurrency, ps.AccountConcurrency, ps.MaxAccountSwitches, ps.HTTPProxy)
	}

	s := api.NewServer(api.ServerDeps{
		Pipeline:             p,
		Keys:                 keyStore,
		Catalog:              catalog,
		Accounts:             accStore,
		Logs:                 logStore,
		OAuth:                oauth,
		AdminPassword:        cfg.Admin.Password,
		AdminSessionTTL:      sessionTTL,
		LogBodies:            cfg.Proxy.LogBodies,
		LogRetentionDays:     cfg.Proxy.LogRetentionDays,
		HTTPProxy:            cfg.Proxy.HTTPProxy,
		Scheduling:           cfg.Proxy.Scheduling,
		MaxConcurrency:       cfg.Proxy.MaxConcurrency,
		AccountConcurrency:   cfg.Proxy.AccountConcurrency,
		MaxAccountSwitches:   cfg.Proxy.Retry.MaxAccountSwitches,
		MaxTransientRetries:  cfg.Proxy.Retry.MaxTransientRetries,
		OnProxySettings:      applyProxy,
	})

	// Log retention purge: once at start, then hourly.
	go runLogRetentionPurge(logStore, s)

	publicAddr := cfg.Server.Listen
	adminAddr := strings.TrimSpace(cfg.Server.AdminListen)

	if adminAddr == "" {
		log.Printf("listening on %s (sqlite=%s)", publicAddr, sqlitePath)
		if err := http.ListenAndServe(publicAddr, s.Handler()); err != nil {
			log.Printf("server stopped: %v", err)
			os.Exit(1)
		}
		return
	}

	// Split listeners: public API vs admin UI/API.
	log.Printf("public listening on %s; admin listening on %s (sqlite=%s)", publicAddr, adminAddr, sqlitePath)
	errCh := make(chan error, 2)
	go func() {
		errCh <- http.ListenAndServe(publicAddr, s.PublicHandler())
	}()
	go func() {
		errCh <- http.ListenAndServe(adminAddr, s.AdminHandler())
	}()
	if err := <-errCh; err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

func requireStrongAdminPassword(pw string) error {
	pw = strings.TrimSpace(pw)
	if pw == "" {
		return fatalError("admin.password is empty; set admin.password or GROK_BRIDGE_ADMIN_PASSWORD")
	}
	if pw == "change-me" {
		return fatalError(`admin.password is the insecure default "change-me"; set a strong password via config or GROK_BRIDGE_ADMIN_PASSWORD`)
	}
	return nil
}

type fatalError string

func (e fatalError) Error() string { return string(e) }

func loadSetting(ctx context.Context, db *sql.DB, key string) (string, bool) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

func parsePositiveInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}

func runLogRetentionPurge(store *logging.RequestLogStore, s *api.Server) {
	purge := func() {
		days := s.LogRetentionDays()
		if days <= 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		n, err := store.DeleteOlderThanDays(ctx, days)
		if err != nil {
			log.Printf("log retention purge: %v", err)
			return
		}
		if n > 0 {
			log.Printf("log retention purge: deleted %d rows older than %d days", n, days)
		}
	}
	purge()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		purge()
	}
}
