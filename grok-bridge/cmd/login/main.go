// Command login runs the xAI OAuth device flow and stores the account in SQLite.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/wlhet/grok-bridge/internal/account"
	"github.com/wlhet/grok-bridge/internal/auth/xai"
	"github.com/wlhet/grok-bridge/internal/config"
	"github.com/wlhet/grok-bridge/internal/db"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config YAML")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cfg.ApplyEnv()

	if cfg.Data.SQLitePath == "" {
		log.Fatal("data.sqlite_path is required (set in config or GROK_BRIDGE_SQLITE_PATH)")
	}

	ctx := context.Background()
	sqlDB, err := db.Open(cfg.Data.SQLitePath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	if err := db.Migrate(ctx, sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	client := &xai.Client{}
	fmt.Fprintln(os.Stderr, "Starting xAI OAuth device flow...")
	dc, err := client.StartDeviceFlow(ctx)
	if err != nil {
		log.Fatalf("start device flow: %v", err)
	}

	verifyURL := dc.VerificationURIComplete
	if verifyURL == "" {
		verifyURL = dc.VerificationURI
	}
	fmt.Fprintf(os.Stderr, "\nOpen this URL in a browser and enter the code:\n\n  %s\n\n  Code: %s\n\nWaiting for authorization...\n", verifyURL, dc.UserCode)

	token, err := client.PollToken(ctx, dc)
	if err != nil {
		log.Fatalf("poll token: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	baseURL := cfg.XAI.BaseURL
	if baseURL == "" {
		baseURL = xai.DefaultAPIBaseURL
	}

	payload := map[string]any{
		"type":           "xai",
		"auth_kind":      "oauth",
		"access_token":   token.AccessToken,
		"refresh_token":  token.RefreshToken,
		"id_token":       token.IDToken,
		"token_type":     token.TokenType,
		"expires_in":     token.ExpiresIn,
		"expired":        token.Expire,
		"last_refresh":   now,
		"email":          token.Email,
		"sub":            token.Subject,
		"base_url":       baseURL,
		"token_endpoint": dc.TokenEndpoint,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Fatalf("marshal oauth json: %v", err)
	}

	store := &account.Store{DB: sqlDB}
	acc, err := store.UpsertFromOAuthJSON(ctx, raw, true)
	if err != nil {
		log.Fatalf("upsert account: %v", err)
	}

	label := acc.Email
	if label == "" {
		label = acc.Subject
	}
	fmt.Printf("Authenticated as %s (id=%s status=%s)\n", label, acc.ID, acc.Status)
}
