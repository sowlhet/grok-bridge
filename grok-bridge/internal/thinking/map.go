// Package thinking maps Claude/OpenAI thinking & reasoning fields to xAI Responses format.
//
// This is a practical subset for Claude Code + Codex bridges — not a full port of CLIProxyAPI's
// multi-provider thinking package.
package thinking

import (
	"encoding/json"
	"strings"
)

// ApplyClaudeToXAI maps Anthropic thinking config into xAI request JSON.
//
// Claude inputs:
//   - thinking.type = "enabled" | "disabled" | "adaptive"
//   - thinking.budget_tokens (token budget when type=enabled)
//   - output_config.effort (adaptive effort: low/medium/high/max)
//
// xAI output: reasoning.effort (none/minimal/low/medium/high/xhigh).
// Claude-only thinking is stripped. Disabled/zero budget removes reasoning.
func ApplyClaudeToXAI(req map[string]any) map[string]any {
	out := shallowCopy(req)
	if out == nil {
		return map[string]any{}
	}

	thinkingObj, _ := out["thinking"].(map[string]any)
	if thinkingObj == nil {
		return out
	}

	typ, _ := thinkingObj["type"].(string)
	typ = strings.ToLower(strings.TrimSpace(typ))

	var effort string
	switch typ {
	case "disabled":
		effort = "none"
	case "adaptive", "auto":
		effort = effortFromOutputConfig(out)
		if effort == "" {
			// Adaptive without explicit effort: leave upstream default (no reasoning field).
			delete(out, "thinking")
			return out
		}
	case "enabled", "":
		if bt, ok := asInt(thinkingObj["budget_tokens"]); ok {
			if bt == 0 {
				effort = "none"
			} else if bt < 0 {
				effort = "medium" // auto
			} else {
				effort = budgetToLevel(bt)
			}
		} else if typ == "enabled" {
			effort = "medium"
		}
	default:
		// Unknown type: strip Claude field only.
		delete(out, "thinking")
		return out
	}

	delete(out, "thinking")
	applyEffort(out, effort)
	return out
}

// ApplyOpenAIToXAI maps OpenAI/Codex reasoning fields into xAI Responses shape.
//
// Handles:
//   - reasoning_effort (chat completions) → reasoning.effort
//   - reasoning.effort (responses/codex) kept / normalized
//   - include[] strips reasoning.encrypted_content (not useful for xAI upstream)
//
// effort "none" strips the reasoning object.
func ApplyOpenAIToXAI(req map[string]any) map[string]any {
	out := shallowCopy(req)
	if out == nil {
		return map[string]any{}
	}

	effort := ""
	if v, ok := out["reasoning_effort"].(string); ok && strings.TrimSpace(v) != "" {
		effort = strings.ToLower(strings.TrimSpace(v))
		delete(out, "reasoning_effort")
	}
	if effort == "" {
		if r, ok := out["reasoning"].(map[string]any); ok {
			if v, ok := r["effort"].(string); ok {
				effort = strings.ToLower(strings.TrimSpace(v))
			}
		}
	}

	if effort != "" {
		applyEffort(out, effort)
	}

	// Drop encrypted reasoning include — xAI executor strips this as well.
	if inc, ok := out["include"].([]any); ok {
		kept := make([]any, 0, len(inc))
		for _, item := range inc {
			s, _ := item.(string)
			if strings.TrimSpace(s) == "" || s == "reasoning.encrypted_content" {
				continue
			}
			kept = append(kept, item)
		}
		out["include"] = kept
	}

	return out
}

// ExtractReasoningFromXAIEvent converts xAI reasoning SSE pieces for Claude thinking /
// OpenAI reasoning streaming.
//
// kind is "delta" for incremental text or "done" for completed summary/content.
// ok is false when the event is not a reasoning event or carries no text.
func ExtractReasoningFromXAIEvent(event map[string]any) (kind string, text string, ok bool) {
	if event == nil {
		return "", "", false
	}
	typ, _ := event["type"].(string)
	typ = strings.TrimSpace(typ)

	switch typ {
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		text, _ = event["delta"].(string)
		if text == "" {
			return "", "", false
		}
		return "delta", text, true
	case "response.reasoning_summary_text.done", "response.reasoning_text.done":
		text, _ = event["text"].(string)
		if text == "" {
			return "", "", false
		}
		return "done", text, true
	case "response.output_item.done", "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		if item == nil {
			return "", "", false
		}
		itemType, _ := item["type"].(string)
		if itemType != "reasoning" {
			return "", "", false
		}
		text = joinReasoningParts(item)
		if text == "" {
			return "", "", false
		}
		return "done", text, true
	default:
		return "", "", false
	}
}

func applyEffort(out map[string]any, effort string) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || effort == "none" {
		delete(out, "reasoning")
		return
	}
	// Map Claude adaptive "max" onto xAI-friendly high/xhigh.
	switch effort {
	case "max":
		effort = "xhigh"
	case "auto":
		effort = "medium"
	}

	r, _ := out["reasoning"].(map[string]any)
	if r == nil {
		r = map[string]any{}
	} else {
		r = shallowCopy(r)
	}
	r["effort"] = effort
	out["reasoning"] = r
}

func effortFromOutputConfig(req map[string]any) string {
	oc, _ := req["output_config"].(map[string]any)
	if oc == nil {
		return ""
	}
	effort, _ := oc["effort"].(string)
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "none" {
		return "none"
	}
	return effort
}

// budgetToLevel maps a Claude budget_tokens value onto discrete xAI effort levels.
// Thresholds align with the practical CPA conversion table.
func budgetToLevel(budget int) string {
	switch {
	case budget <= 0:
		return "none"
	case budget <= 512:
		return "minimal"
	case budget <= 1024:
		return "low"
	case budget <= 8192:
		return "medium"
	case budget <= 24576:
		return "high"
	default:
		return "xhigh"
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func joinReasoningParts(item map[string]any) string {
	var b strings.Builder
	// Prefer summary (public reasoning summary) over internal content.
	if summary, ok := item["summary"].([]any); ok && len(summary) > 0 {
		for _, part := range summary {
			pm, _ := part.(map[string]any)
			if pm == nil {
				continue
			}
			if t, _ := pm["text"].(string); t != "" {
				b.WriteString(t)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	if content, ok := item["content"].([]any); ok {
		for _, part := range content {
			pm, _ := part.(map[string]any)
			if pm == nil {
				continue
			}
			pt, _ := pm["type"].(string)
			if pt != "" && pt != "reasoning_text" && pt != "summary_text" {
				continue
			}
			if t, _ := pm["text"].(string); t != "" {
				b.WriteString(t)
			}
		}
	}
	return b.String()
}

func shallowCopy(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
