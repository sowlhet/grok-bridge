package models_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wlhet/grok-bridge/internal/config"
	"github.com/wlhet/grok-bridge/internal/models"
)

func testCatalog() *models.Catalog {
	return &models.Catalog{
		Models: []string{"grok-4.5", "grok-4.3", "grok-3-mini"},
		Aliases: map[string]string{
			"claude-sonnet-4-20250514": "grok-4.5",
			"gpt-5":                   "grok-4.5",
		},
		Unknown: "passthrough",
	}
}

func TestResolveAlias(t *testing.T) {
	c := testCatalog()

	upstream, err := c.Resolve("claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("Resolve alias: %v", err)
	}
	if upstream != "grok-4.5" {
		t.Fatalf("alias upstream = %q, want grok-4.5", upstream)
	}

	upstream, err = c.Resolve("gpt-5")
	if err != nil {
		t.Fatalf("Resolve gpt-5: %v", err)
	}
	if upstream != "grok-4.5" {
		t.Fatalf("gpt-5 upstream = %q, want grok-4.5", upstream)
	}
}

func TestResolveKnownModel(t *testing.T) {
	c := testCatalog()
	upstream, err := c.Resolve("grok-4.3")
	if err != nil {
		t.Fatalf("Resolve known: %v", err)
	}
	if upstream != "grok-4.3" {
		t.Fatalf("upstream = %q, want grok-4.3", upstream)
	}
}

func TestResolvePassthroughUnknown(t *testing.T) {
	c := testCatalog()
	c.Unknown = "passthrough"
	upstream, err := c.Resolve("some-future-model")
	if err != nil {
		t.Fatalf("passthrough should not error: %v", err)
	}
	if upstream != "some-future-model" {
		t.Fatalf("upstream = %q, want some-future-model", upstream)
	}
}

func TestResolveStrictRejectsUnknown(t *testing.T) {
	c := testCatalog()
	c.Unknown = "strict"
	upstream, err := c.Resolve("not-a-model")
	if err == nil {
		t.Fatalf("strict should reject unknown, got upstream %q", upstream)
	}
	if !errors.Is(err, models.ErrUnknownModel) {
		t.Fatalf("err = %v, want ErrUnknownModel", err)
	}
	if !strings.Contains(err.Error(), "not-a-model") {
		t.Fatalf("error should mention model id: %v", err)
	}
}

func TestResolveAliasPreferredOverStrict(t *testing.T) {
	c := testCatalog()
	c.Unknown = "strict"
	// Alias is not in Models list as a key; should still resolve.
	upstream, err := c.Resolve("gpt-5")
	if err != nil {
		t.Fatalf("alias under strict: %v", err)
	}
	if upstream != "grok-4.5" {
		t.Fatalf("upstream = %q, want grok-4.5", upstream)
	}
}

func TestNewFromConfig(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{
			{ID: "grok-4.5"},
			{ID: "grok-3-mini"},
		},
		Aliases: map[string]string{"gpt-5": "grok-4.5"},
		Proxy:   config.ProxyConfig{UnknownModel: "strict"},
	}
	c := models.NewFromConfig(cfg)
	if len(c.Models) != 2 || c.Models[0] != "grok-4.5" || c.Models[1] != "grok-3-mini" {
		t.Fatalf("Models = %#v", c.Models)
	}
	if c.Aliases["gpt-5"] != "grok-4.5" {
		t.Fatalf("Aliases = %#v", c.Aliases)
	}
	if c.Unknown != "strict" {
		t.Fatalf("Unknown = %q", c.Unknown)
	}
	// Mutation isolation
	cfg.Aliases["gpt-5"] = "mutated"
	if c.Aliases["gpt-5"] != "grok-4.5" {
		t.Fatal("catalog aliases should be a copy")
	}
}

func TestListOpenAI(t *testing.T) {
	c := testCatalog()
	raw, err := json.Marshal(c.ListOpenAI())
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" {
		t.Fatalf("object = %q", body.Object)
	}
	if len(body.Data) != 3 {
		t.Fatalf("data len = %d", len(body.Data))
	}
	if body.Data[0].ID != "grok-4.5" || body.Data[0].Object != "model" {
		t.Fatalf("first entry = %#v", body.Data[0])
	}
}

func TestListClaude(t *testing.T) {
	c := testCatalog()
	raw, err := json.Marshal(c.ListClaude())
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 3 {
		t.Fatalf("data len = %d", len(body.Data))
	}
	if body.Data[1].ID != "grok-4.3" || body.Data[1].Type != "model" {
		t.Fatalf("second entry = %#v", body.Data[1])
	}
}
