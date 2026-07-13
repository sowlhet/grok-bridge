// Package config loads and validates grok-bridge configuration from YAML and env.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration for grok-bridge.
type Config struct {
	Server  ServerConfig      `yaml:"server"`
	Admin   AdminConfig       `yaml:"admin"`
	Data    DataConfig        `yaml:"data"`
	Proxy   ProxyConfig       `yaml:"proxy"`
	Models  []ModelEntry      `yaml:"models"`
	Aliases map[string]string `yaml:"aliases"`
	XAI     XAIConfig         `yaml:"xai"`
}

// ServerConfig holds HTTP listen settings.
type ServerConfig struct {
	Listen      string `yaml:"listen"`
	AdminListen string `yaml:"admin_listen"`
}

// AdminConfig holds admin auth settings.
type AdminConfig struct {
	Password   string `yaml:"password"`
	SessionTTL string `yaml:"session_ttl"`
}

// DataConfig holds persistence paths.
type DataConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
}

// ProxyConfig holds proxy behavior and logging policy.
type ProxyConfig struct {
	Retry            RetryConfig `yaml:"retry"`
	LogBodies        string      `yaml:"log_bodies"`
	LogRetentionDays int         `yaml:"log_retention_days"`
	UnknownModel     string      `yaml:"unknown_model"`
}

// RetryConfig controls account failover and transient retries.
type RetryConfig struct {
	MaxAccountSwitches  int `yaml:"max_account_switches"`
	MaxTransientRetries int `yaml:"max_transient_retries"`
}

// ModelEntry is a catalog model entry.
type ModelEntry struct {
	ID string `yaml:"id"`
}

// XAIConfig holds optional xAI client settings.
type XAIConfig struct {
	BaseURL string `yaml:"base_url"`
}

// defaultConfig returns a Config with documented defaults applied.
func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Listen: "0.0.0.0:8080",
		},
		Admin: AdminConfig{
			SessionTTL: "24h",
		},
		Proxy: ProxyConfig{
			Retry: RetryConfig{
				MaxAccountSwitches:  2,
				MaxTransientRetries: 2,
			},
			LogBodies:        "errors_only",
			LogRetentionDays: 30,
			UnknownModel:     "passthrough",
		},
		Models: []ModelEntry{
			{ID: "grok-4.5"},
			{ID: "grok-4.3"},
			{ID: "grok-3-mini"},
		},
		Aliases: map[string]string{},
	}
}

// Load reads YAML from path, applies defaults for unset fields, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyDefaults fills zero-valued fields after YAML unmarshal.
// yaml.Unmarshal overwrites the whole Config with zero values for missing keys
// only for nested structs when the parent key is present; for top-level missing
// keys the pre-seeded defaults remain. We still re-apply common defaults here
// so partial nested objects (e.g. proxy: {}) do not leave zeros.
func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = "0.0.0.0:8080"
	}
	if c.Admin.SessionTTL == "" {
		c.Admin.SessionTTL = "24h"
	}
	if c.Proxy.LogBodies == "" {
		c.Proxy.LogBodies = "errors_only"
	}
	if c.Proxy.LogRetentionDays == 0 {
		c.Proxy.LogRetentionDays = 30
	}
	if c.Proxy.Retry.MaxAccountSwitches == 0 {
		c.Proxy.Retry.MaxAccountSwitches = 2
	}
	if c.Proxy.Retry.MaxTransientRetries == 0 {
		c.Proxy.Retry.MaxTransientRetries = 2
	}
	if c.Proxy.UnknownModel == "" {
		c.Proxy.UnknownModel = "passthrough"
	}
	if len(c.Models) == 0 {
		c.Models = []ModelEntry{
			{ID: "grok-4.5"},
			{ID: "grok-4.3"},
			{ID: "grok-3-mini"},
		}
	}
	if c.Aliases == nil {
		c.Aliases = map[string]string{}
	}
}

func (c *Config) validate() error {
	if _, err := time.ParseDuration(c.Admin.SessionTTL); err != nil {
		return fmt.Errorf("admin.session_ttl: %w", err)
	}
	switch c.Proxy.LogBodies {
	case "off", "errors_only", "sample", "all":
	default:
		return fmt.Errorf("proxy.log_bodies: invalid value %q", c.Proxy.LogBodies)
	}
	switch c.Proxy.UnknownModel {
	case "passthrough", "strict":
	default:
		return fmt.Errorf("proxy.unknown_model: invalid value %q", c.Proxy.UnknownModel)
	}
	return nil
}

// ApplyEnv overlays sensitive/runtime settings from environment variables.
// Non-empty env values win over YAML.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("GROK_BRIDGE_LISTEN"); v != "" {
		c.Server.Listen = v
	}
	if v := os.Getenv("GROK_BRIDGE_ADMIN_LISTEN"); v != "" {
		c.Server.AdminListen = v
	}
	if v := os.Getenv("GROK_BRIDGE_ADMIN_PASSWORD"); v != "" {
		c.Admin.Password = v
	}
	if v := os.Getenv("GROK_BRIDGE_SQLITE_PATH"); v != "" {
		c.Data.SQLitePath = v
	}
}
