// Package models provides the configurable model catalog and alias resolution.
package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/wlhet/grok-bridge/internal/config"
)

// ErrUnknownModel is returned by Catalog.Resolve in strict mode for unknown IDs.
var ErrUnknownModel = errors.New("unknown model")

// Catalog holds known model IDs, client→upstream aliases, and unknown-model policy.
type Catalog struct {
	Models  []string
	Aliases map[string]string
	Unknown string // "strict" | "passthrough"
}

// NewFromConfig builds a Catalog from config Models, Aliases, and Proxy.UnknownModel.
// Slices and maps are copied so later config mutation does not affect the catalog.
func NewFromConfig(cfg *config.Config) *Catalog {
	models := make([]string, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		models = append(models, m.ID)
	}
	aliases := make(map[string]string, len(cfg.Aliases))
	for k, v := range cfg.Aliases {
		aliases[k] = v
	}
	unknown := cfg.Proxy.UnknownModel
	if unknown == "" {
		unknown = "passthrough"
	}
	return &Catalog{
		Models:  models,
		Aliases: aliases,
		Unknown: unknown,
	}
}

// Resolve maps a client-requested model ID to the upstream Grok model ID.
// Order: alias → known catalog ID → unknown policy (passthrough or strict error).
func (c *Catalog) Resolve(requested string) (upstream string, err error) {
	if c.Aliases != nil {
		if mapped, ok := c.Aliases[requested]; ok {
			return mapped, nil
		}
	}
	for _, id := range c.Models {
		if id == requested {
			return requested, nil
		}
	}
	if c.Unknown == "strict" {
		return "", fmt.Errorf("%w: %q", ErrUnknownModel, requested)
	}
	// passthrough (default)
	return requested, nil
}

// ListOpenAI returns an OpenAI-compatible models list payload.
func (c *Catalog) ListOpenAI() any {
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	created := time.Now().Unix()
	data := make([]model, 0, len(c.Models))
	for _, id := range c.Models {
		data = append(data, model{
			ID:      id,
			Object:  "model",
			Created: created,
			OwnedBy: "xai",
		})
	}
	return map[string]any{
		"object": "list",
		"data":   data,
	}
}

// ListClaude returns an Anthropic-ish models list payload.
func (c *Catalog) ListClaude() any {
	type model struct {
		Type        string `json:"type"`
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		CreatedAt   string `json:"created_at"`
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)
	data := make([]model, 0, len(c.Models))
	for _, id := range c.Models {
		data = append(data, model{
			Type:        "model",
			ID:          id,
			DisplayName: id,
			CreatedAt:   createdAt,
		})
	}
	return map[string]any{
		"data":     data,
		"has_more": false,
	}
}
