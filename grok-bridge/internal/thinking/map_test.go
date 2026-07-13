package thinking_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/wlhet/grok-bridge/internal/thinking"
)

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}
	return m
}

func TestApplyClaudeToXAI(t *testing.T) {
	tests := []struct {
		name       string
		in         map[string]any
		wantEffort any // string effort, or nil if reasoning should be absent
		wantStrip  []string
	}{
		{
			name: "enabled with budget medium",
			in: map[string]any{
				"model": "grok-4.5",
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": float64(8192),
				},
			},
			wantEffort: "medium",
			wantStrip:  []string{"thinking"},
		},
		{
			name: "enabled with high budget",
			in: map[string]any{
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": float64(20000),
				},
			},
			wantEffort: "high",
			wantStrip:  []string{"thinking"},
		},
		{
			name: "enabled without budget defaults to medium",
			in: map[string]any{
				"thinking": map[string]any{
					"type": "enabled",
				},
			},
			wantEffort: "medium",
			wantStrip:  []string{"thinking"},
		},
		{
			name: "disabled strips reasoning",
			in: map[string]any{
				"thinking": map[string]any{
					"type": "disabled",
				},
				"reasoning": map[string]any{
					"effort": "high",
				},
			},
			wantEffort: nil,
			wantStrip:  []string{"thinking", "reasoning"},
		},
		{
			name: "budget zero strips",
			in: map[string]any{
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": float64(0),
				},
			},
			wantEffort: nil,
			wantStrip:  []string{"thinking", "reasoning"},
		},
		{
			name: "adaptive effort high",
			in: map[string]any{
				"thinking": map[string]any{
					"type": "adaptive",
				},
				"output_config": map[string]any{
					"effort": "high",
				},
			},
			wantEffort: "high",
			wantStrip:  []string{"thinking"},
		},
		{
			name: "no thinking passthrough",
			in: map[string]any{
				"model": "grok-4.5",
				"input": []any{"hi"},
			},
			wantEffort: nil,
			wantStrip:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := cloneMap(tt.in)
			got := thinking.ApplyClaudeToXAI(cloneMap(tt.in))

			// Input must not be mutated when we pass a clone; also check original fixture equality.
			if !reflect.DeepEqual(orig, tt.in) {
				t.Fatalf("fixture mutated")
			}

			for _, key := range tt.wantStrip {
				if _, ok := got[key]; ok && key == "thinking" {
					t.Fatalf("expected %q stripped, still present: %#v", key, got[key])
				}
				if key == "reasoning" {
					if _, ok := got["reasoning"]; ok {
						t.Fatalf("expected reasoning stripped, got %#v", got["reasoning"])
					}
				}
			}

			if tt.wantEffort == nil {
				if r, ok := got["reasoning"]; ok {
					// allow empty/absent effort object only if nil effort expected → must be gone
					t.Fatalf("expected no reasoning, got %#v", r)
				}
				return
			}

			rm := asMap(t, got["reasoning"])
			if rm["effort"] != tt.wantEffort {
				t.Fatalf("reasoning.effort = %#v, want %#v; full=%#v", rm["effort"], tt.wantEffort, got)
			}
		})
	}
}

func TestApplyOpenAIToXAI(t *testing.T) {
	tests := []struct {
		name       string
		in         map[string]any
		wantEffort any
		wantStrip  []string
		checkInc   bool
		wantInc    []any
	}{
		{
			name: "reasoning_effort to nested",
			in: map[string]any{
				"model":            "grok-4.5",
				"reasoning_effort": "high",
			},
			wantEffort: "high",
			wantStrip:  []string{"reasoning_effort"},
		},
		{
			name: "existing reasoning.effort kept",
			in: map[string]any{
				"reasoning": map[string]any{
					"effort": "low",
				},
			},
			wantEffort: "low",
		},
		{
			name: "none strips reasoning",
			in: map[string]any{
				"reasoning_effort": "none",
				"reasoning": map[string]any{
					"effort": "high",
				},
			},
			wantEffort: nil,
			wantStrip:  []string{"reasoning_effort", "reasoning"},
		},
		{
			name: "nested none strips",
			in: map[string]any{
				"reasoning": map[string]any{
					"effort": "none",
				},
			},
			wantEffort: nil,
			wantStrip:  []string{"reasoning"},
		},
		{
			name: "strip encrypted_content include",
			in: map[string]any{
				"reasoning_effort": "medium",
				"include": []any{
					"reasoning.encrypted_content",
					"file_search_call.results",
				},
			},
			wantEffort: "medium",
			wantStrip:  []string{"reasoning_effort"},
			checkInc:   true,
			wantInc:    []any{"file_search_call.results"},
		},
		{
			name: "no reasoning fields passthrough",
			in: map[string]any{
				"model": "grok-4.5",
			},
			wantEffort: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := thinking.ApplyOpenAIToXAI(cloneMap(tt.in))

			for _, key := range tt.wantStrip {
				if key == "reasoning_effort" {
					if _, ok := got["reasoning_effort"]; ok {
						t.Fatalf("expected reasoning_effort stripped")
					}
				}
				if key == "reasoning" {
					if _, ok := got["reasoning"]; ok {
						t.Fatalf("expected reasoning stripped, got %#v", got["reasoning"])
					}
				}
			}

			if tt.wantEffort == nil {
				if r, ok := got["reasoning"]; ok {
					t.Fatalf("expected no reasoning, got %#v", r)
				}
			} else {
				rm := asMap(t, got["reasoning"])
				if rm["effort"] != tt.wantEffort {
					t.Fatalf("reasoning.effort = %#v, want %#v", rm["effort"], tt.wantEffort)
				}
			}

			if tt.checkInc {
				inc, ok := got["include"].([]any)
				if !ok {
					t.Fatalf("include type = %T, want []any", got["include"])
				}
				if !reflect.DeepEqual(inc, tt.wantInc) {
					t.Fatalf("include = %#v, want %#v", inc, tt.wantInc)
				}
			}
		})
	}
}

func TestExtractReasoningFromXAIEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    map[string]any
		wantKind string
		wantText string
		wantOK   bool
	}{
		{
			name: "summary text delta",
			event: map[string]any{
				"type":  "response.reasoning_summary_text.delta",
				"delta": "step 1",
			},
			wantKind: "delta",
			wantText: "step 1",
			wantOK:   true,
		},
		{
			name: "reasoning text delta",
			event: map[string]any{
				"type":  "response.reasoning_text.delta",
				"delta": "think",
			},
			wantKind: "delta",
			wantText: "think",
			wantOK:   true,
		},
		{
			name: "summary text done",
			event: map[string]any{
				"type": "response.reasoning_summary_text.done",
				"text": "full summary",
			},
			wantKind: "done",
			wantText: "full summary",
			wantOK:   true,
		},
		{
			name: "output item done reasoning with summary",
			event: map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type": "reasoning",
					"summary": []any{
						map[string]any{"type": "summary_text", "text": "a"},
						map[string]any{"type": "summary_text", "text": "b"},
					},
				},
			},
			wantKind: "done",
			wantText: "ab",
			wantOK:   true,
		},
		{
			name: "output item done reasoning with content",
			event: map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type": "reasoning",
					"content": []any{
						map[string]any{"type": "reasoning_text", "text": "internal"},
					},
				},
			},
			wantKind: "done",
			wantText: "internal",
			wantOK:   true,
		},
		{
			name: "non-reasoning event",
			event: map[string]any{
				"type":  "response.output_text.delta",
				"delta": "hello",
			},
			wantOK: false,
		},
		{
			name: "empty delta ignored",
			event: map[string]any{
				"type":  "response.reasoning_summary_text.delta",
				"delta": "",
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, text, ok := thinking.ExtractReasoningFromXAIEvent(tt.event)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (kind=%q text=%q)", ok, tt.wantOK, kind, text)
			}
			if !tt.wantOK {
				return
			}
			if kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", kind, tt.wantKind)
			}
			if text != tt.wantText {
				t.Fatalf("text = %q, want %q", text, tt.wantText)
			}
		})
	}
}
