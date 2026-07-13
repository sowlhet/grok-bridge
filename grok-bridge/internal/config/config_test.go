package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wlhet/grok-bridge/internal/config"
)

func TestLoadMinimalYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \"127.0.0.1:9090\"\nadmin:\n  password: \"secret\"\ndata:\n  sqlite_path: \"./data/test.db\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:9090" {
		t.Fatalf("listen=%q", cfg.Server.Listen)
	}
	if cfg.Admin.Password != "secret" {
		t.Fatalf("password")
	}
	if cfg.Proxy.LogBodies == "" {
		t.Fatal("expected default log_bodies")
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Fatalf("listen default=%q", cfg.Server.Listen)
	}
	if cfg.Proxy.LogBodies != "errors_only" {
		t.Fatalf("log_bodies=%q", cfg.Proxy.LogBodies)
	}
	if cfg.Proxy.LogRetentionDays != 30 {
		t.Fatalf("log_retention_days=%d", cfg.Proxy.LogRetentionDays)
	}
	if cfg.Proxy.Retry.MaxAccountSwitches != 2 {
		t.Fatalf("max_account_switches=%d", cfg.Proxy.Retry.MaxAccountSwitches)
	}
	if cfg.Proxy.Retry.MaxTransientRetries != 2 {
		t.Fatalf("max_transient_retries=%d", cfg.Proxy.Retry.MaxTransientRetries)
	}
	if cfg.Proxy.UnknownModel != "passthrough" {
		t.Fatalf("unknown_model=%q", cfg.Proxy.UnknownModel)
	}
	if cfg.Admin.SessionTTL != "24h" {
		t.Fatalf("session_ttl=%q", cfg.Admin.SessionTTL)
	}
	if len(cfg.Models) != 3 {
		t.Fatalf("models len=%d want 3", len(cfg.Models))
	}
	wantModels := []string{"grok-4.5", "grok-4.3", "grok-3-mini"}
	for i, id := range wantModels {
		if cfg.Models[i].ID != id {
			t.Fatalf("models[%d]=%q want %q", i, cfg.Models[i].ID, id)
		}
	}
}

func TestApplyEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \"127.0.0.1:9090\"\nadmin:\n  password: \"secret\"\ndata:\n  sqlite_path: \"./data/test.db\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GROK_BRIDGE_LISTEN", "0.0.0.0:18080")
	t.Setenv("GROK_BRIDGE_ADMIN_PASSWORD", "from-env")
	t.Setenv("GROK_BRIDGE_SQLITE_PATH", "/tmp/env.db")
	cfg.ApplyEnv()

	if cfg.Server.Listen != "0.0.0.0:18080" {
		t.Fatalf("listen=%q", cfg.Server.Listen)
	}
	if cfg.Admin.Password != "from-env" {
		t.Fatalf("password=%q", cfg.Admin.Password)
	}
	if cfg.Data.SQLitePath != "/tmp/env.db" {
		t.Fatalf("sqlite_path=%q", cfg.Data.SQLitePath)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
