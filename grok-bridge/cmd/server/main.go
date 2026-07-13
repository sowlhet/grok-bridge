package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/wlhet/grok-bridge/internal/pipeline"
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

	accStore := &account.Store{DB: db}
	keyStore := &access.KeyStore{DB: db}
	logStore := &logging.RequestLogStore{DB: db}
	catalog := models.NewFromConfig(cfg)
	picker := &account.Picker{Store: accStore}

	httpClient := &http.Client{}
	oauth := &xaiauth.Client{HTTP: httpClient}
	xaiClient := &xai.Client{HTTP: httpClient}

	p := &pipeline.Pipeline{
		Accounts:     picker,
		AccountStore: accStore,
		XAI:          xaiClient,
		OAuth:        oauth,
		Catalog:      catalog,
		Logs:         logStore,
		Retry:        cfg.Proxy.Retry,
		LogBodies:    cfg.Proxy.LogBodies,
	}

	sessionTTL, err := time.ParseDuration(cfg.Admin.SessionTTL)
	if err != nil {
		sessionTTL = 24 * time.Hour
	}

	s := api.NewServer(api.ServerDeps{
		Pipeline:         p,
		Keys:             keyStore,
		Catalog:          catalog,
		Accounts:         accStore,
		Logs:             logStore,
		OAuth:            oauth,
		AdminPassword:    cfg.Admin.Password,
		AdminSessionTTL:  sessionTTL,
		LogBodies:        cfg.Proxy.LogBodies,
		LogRetentionDays: cfg.Proxy.LogRetentionDays,
	})

	log.Printf("listening on %s (sqlite=%s)", cfg.Server.Listen, sqlitePath)
	if err := http.ListenAndServe(cfg.Server.Listen, s.Handler()); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
