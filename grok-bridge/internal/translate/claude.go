package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wlhet/grok-bridge/internal/thinking"
)

// ClaudeMessagesToXAI converts an Anthropic Messages request into an xAI
// Responses request (model + instructions + input + flattened tools).
func ClaudeMessagesToXAI(body []byte, model string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("claude request json: %w", err)
	}
	if req == nil {
		req = map[string]any{}
	}

	out := map[string]any{}
	if model != "" {
		out["model"] = model
	} else if m, _ := req["model"].(string); m != "" {
		out["model"] = m
	}

	// Generation knobs.
	for _, k := range []string{
		"temperature", "top_p", "stream", "user",
		"max_output_tokens", "parallel_tool_calls", "store",
	} {
		if v, ok := req[k]; ok {
			out[k] = v
		}
	}
	if v, ok := req["max_tokens"]; ok {
		if _, exists := out["max_output_tokens"]; !exists {
			out["max_output_tokens"] = v
		}
	}

	// Top-level system → instructions (string).
	if instr := claudeSystemToInstructions(req["system"]); instr != "" {
		out["instructions"] = instr
	}

	// Messages → input items.
	if msgs, ok := req["messages"].([]any); ok {
		out["input"] = claudeMessagesToInput(msgs)
	} else {
		out["input"] = []any{}
	}

	// Tools: Claude input_schema → Responses parameters.
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		out["tools"] = mapClaudeTools(tools)
	}
	if tc, ok := req["tool_choice"]; ok {
		out["tool_choice"] = mapClaudeToolChoice(tc)
	}

	// Preserve thinking for ApplyClaudeToXAI.
	if th, ok := req["thinking"]; ok {
		out["thinking"] = th
	}
	if oc, ok := req["output_config"]; ok {
		out["output_config"] = oc
	}

	out = thinking.ApplyClaudeToXAI(out)
	// Strip leftover Claude-only fields that ApplyClaudeToXAI may leave when
	// thinking was absent (output_config is still Claude-shaped).
	delete(out, "thinking")
	delete(out, "output_config")

	return json.Marshal(out)
}

func claudeSystemToInstructions(system any) string {
	switch s := system.(type) {
	case nil:
		return ""
	case string:
		return s
	case []any:
		var b strings.Builder
		for i, raw := range s {
			switch part := raw.(type) {
			case string:
				if part == "" {
					continue
				}
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(part)
			case map[string]any:
				// Prefer type=text blocks; also accept bare text field.
				typ := asString(part["type"])
				if typ != "" && typ != "text" {
					continue
				}
				text := asString(part["text"])
				if text == "" {
					continue
				}
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(text)
				_ = i
			}
		}
		return b.String()
	default:
		return ""
	}
}

func claudeMessagesToInput(msgs []any) []any {
	input := make([]any, 0, len(msgs))
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		// Mid-conversation system messages are uncommon; skip (top-level system
		// already became instructions).
		if role == "system" {
			continue
		}

		switch content := m["content"].(type) {
		case nil:
			continue
		case string:
			if content == "" {
				continue
			}
			partType := "input_text"
			if role == "assistant" {
				partType = "output_text"
			}
			input = append(input, map[string]any{
				"type": "message",
				"role": role,
				"content": []any{
					map[string]any{"type": partType, "text": content},
				},
			})
		case []any:
			// May mix text / tool_use / tool_result / thinking / image.
			var textParts []any
			flushText := func() {
				if len(textParts) == 0 {
					return
				}
				input = append(input, map[string]any{
					"type":    "message",
					"role":    role,
					"content": textParts,
				})
				textParts = nil
			}
			for _, cRaw := range content {
				part, ok := cRaw.(map[string]any)
				if !ok {
					continue
				}
				switch asString(part["type"]) {
				case "text", "":
					text := asString(part["text"])
					if text == "" {
						continue
					}
					pt := "input_text"
					if role == "assistant" {
						pt = "output_text"
					}
					textParts = append(textParts, map[string]any{"type": pt, "text": text})
				case "image":
					// Best-effort: Claude source → input_image data URL.
					if role != "user" {
						continue
					}
					if src, ok := part["source"].(map[string]any); ok {
						data := asString(src["data"])
						if data == "" {
							data = asString(src["base64"])
						}
						if data == "" {
							continue
						}
						media := asString(src["media_type"])
						if media == "" {
							media = asString(src["mime_type"])
						}
						if media == "" {
							media = "application/octet-stream"
						}
						flushText()
						textParts = append(textParts, map[string]any{
							"type":      "input_image",
							"image_url": fmt.Sprintf("data:%s;base64,%s", media, data),
						})
						// Keep image inside a message block with any following text.
					}
				case "tool_use":
					flushText()
					args := part["input"]
					argStr := "{}"
					switch a := args.(type) {
					case string:
						if a != "" {
							argStr = a
						}
					case nil:
						// keep {}
					default:
						if b, err := json.Marshal(a); err == nil {
							argStr = string(b)
						}
					}
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   asString(part["id"]),
						"name":      asString(part["name"]),
						"arguments": argStr,
					})
				case "tool_result":
					flushText()
					input = append(input, map[string]any{
						"type":    "function_call_output",
						"call_id": asString(part["tool_use_id"]),
						"output":  claudeToolResultOutput(part["content"]),
					})
				case "thinking":
					// Drop assistant thinking blocks for request history; xAI
					// uses encrypted_content which we do not rehydrate here.
					continue
				}
			}
			flushText()
		default:
			// Fallback stringify.
			b, err := json.Marshal(content)
			if err != nil {
				continue
			}
			pt := "input_text"
			if role == "assistant" {
				pt = "output_text"
			}
			input = append(input, map[string]any{
				"type": "message",
				"role": role,
				"content": []any{
					map[string]any{"type": pt, "text": string(b)},
				},
			})
		}
	}
	return input
}

func claudeToolResultOutput(content any) any {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		var b strings.Builder
		onlyText := true
		for _, raw := range c {
			it, ok := raw.(map[string]any)
			if !ok {
				onlyText = false
				break
			}
			typ := asString(it["type"])
			if typ != "" && typ != "text" {
				onlyText = false
				break
			}
			b.WriteString(asString(it["text"]))
		}
		if onlyText {
			return b.String()
		}
		return c
	default:
		bb, err := json.Marshal(c)
		if err != nil {
			return fmt.Sprint(c)
		}
		return string(bb)
	}
}

func mapClaudeTools(tools []any) []any {
	out := make([]any, 0, len(tools))
	for _, raw := range tools {
		t, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := asString(t["type"])
		// Built-in Claude tools (web_search_*, etc.) — pass through lightly.
		if typ != "" && typ != "function" && typ != "custom" {
			item := map[string]any{"type": typ}
			if v, ok := t["name"]; ok {
				item["name"] = v
			}
			out = append(out, item)
			continue
		}
		item := map[string]any{"type": "function"}
		if v, ok := t["name"]; ok {
			item["name"] = v
		}
		if v, ok := t["description"]; ok {
			item["description"] = v
		}
		if v, ok := t["input_schema"]; ok {
			item["parameters"] = v
		} else if v, ok := t["parameters"]; ok {
			item["parameters"] = v
		}
		if v, ok := t["strict"]; ok {
			item["strict"] = v
		}
		out = append(out, item)
	}
	return out
}

func mapClaudeToolChoice(tc any) any {
	switch v := tc.(type) {
	case string:
		switch strings.ToLower(v) {
		case "any":
			return "required"
		case "auto", "none", "required":
			return strings.ToLower(v)
		default:
			return v
		}
	case map[string]any:
		switch asString(v["type"]) {
		case "auto", "":
			return "auto"
		case "any":
			return "required"
		case "none":
			return "none"
		case "tool":
			name := asString(v["name"])
			choice := map[string]any{"type": "function"}
			if name != "" {
				choice["name"] = name
			}
			return choice
		default:
			return v
		}
	default:
		return tc
	}
}

// XAIResponseToClaudeMessage converts a completed xAI Responses payload into an
// Anthropic Messages response object.
func XAIResponseToClaudeMessage(body []byte) ([]byte, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty xai response body")
	}
	if bytes.HasPrefix(body, []byte("data:")) {
		body = bytes.TrimSpace(bytes.TrimPrefix(body, []byte("data:")))
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("xai response json: %w", err)
	}
	resp := unwrapResponse(root)

	id := asString(resp["id"])
	model := asString(resp["model"])
	content, hasTool := claudeContentFromOutput(resp["output"])

	stop := "end_turn"
	if hasTool {
		stop = "tool_use"
	} else if status := asString(resp["status"]); status == "incomplete" {
		// Honor incomplete → max_tokens when reason suggests length.
		if reason := asString(mapPath(resp, "incomplete_details", "reason")); reason == "max_output_tokens" || reason == "max_tokens" {
			stop = "max_tokens"
		}
	}

	out := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stop,
		"stop_sequence": nil,
	}
	if usage := claudeUsage(resp["usage"]); usage != nil {
		out["usage"] = usage
	} else {
		out["usage"] = map[string]any{
			"input_tokens":  0,
			"output_tokens": 0,
		}
	}
	return json.Marshal(out)
}

func claudeContentFromOutput(output any) (content []any, hasTool bool) {
	arr, ok := output.([]any)
	if !ok {
		return []any{}, false
	}
	content = make([]any, 0, len(arr))
	for _, raw := range arr {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch asString(item["type"]) {
		case "message":
			if parts, ok := item["content"].([]any); ok {
				for _, p := range parts {
					pm, ok := p.(map[string]any)
					if !ok {
						continue
					}
					if asString(pm["type"]) == "output_text" || asString(pm["type"]) == "text" {
						text := asString(pm["text"])
						if text == "" {
							continue
						}
						content = append(content, map[string]any{
							"type": "text",
							"text": text,
						})
					}
				}
			}
		case "reasoning":
			reasoningText := reasoningTextFromItem(item)
			if reasoningText != "" {
				content = append(content, map[string]any{
					"type":     "thinking",
					"thinking": reasoningText,
				})
			}
		case "function_call":
			hasTool = true
			inputObj := map[string]any{}
			args := asString(item["arguments"])
			if args != "" && json.Valid([]byte(args)) {
				_ = json.Unmarshal([]byte(args), &inputObj)
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    asString(item["call_id"]),
				"name":  asString(item["name"]),
				"input": inputObj,
			})
		}
	}
	return content, hasTool
}

func reasoningTextFromItem(item map[string]any) string {
	var b strings.Builder
	if summary, ok := item["summary"].([]any); ok {
		for _, s := range summary {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if t := asString(sm["text"]); t != "" {
				b.WriteString(t)
			}
		}
	}
	if b.Len() == 0 {
		if parts, ok := item["content"].([]any); ok {
			for _, p := range parts {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}
				if t := asString(pm["text"]); t != "" {
					b.WriteString(t)
				}
			}
		}
	}
	return b.String()
}

func claudeUsage(usage any) map[string]any {
	u, ok := usage.(map[string]any)
	if !ok || u == nil {
		return nil
	}
	out := map[string]any{}
	if v, ok := u["input_tokens"]; ok {
		out["input_tokens"] = v
	} else {
		out["input_tokens"] = 0
	}
	if v, ok := u["output_tokens"]; ok {
		out["output_tokens"] = v
	} else {
		out["output_tokens"] = 0
	}
	if details, ok := u["input_tokens_details"].(map[string]any); ok {
		if v, ok := details["cached_tokens"]; ok {
			out["cache_read_input_tokens"] = v
		}
	}
	return out
}

// claudeBlock tracks one open Claude content_block in a live SSE session.
type claudeBlock struct {
	index    int
	kind     string // text | thinking | tool_use
	started  bool
	stopped  bool
	streamed bool // true once any delta was emitted for this block
}

// ClaudeSSETranslator converts a sequence of upstream xAI Responses events into
// Claude Messages SSE frames. One instance must be used for an entire SSE
// session so content_block index values increment correctly and
// response.output_item.done does not re-emit text/arguments already streamed
// via deltas.
//
// Pipeline usage: create NewClaudeSSETranslator() when opening the upstream
// stream; call Event for each xAI event; discard after message_stop.
type ClaudeSSETranslator struct {
	nextIndex int
	text      *claudeBlock
	thinking  *claudeBlock
	tool      *claudeBlock
}

// NewClaudeSSETranslator returns a fresh per-stream translator.
func NewClaudeSSETranslator() *ClaudeSSETranslator {
	return &ClaudeSSETranslator{}
}

// XAIEventToClaudeSSE converts one upstream xAI Responses event into zero or
// more Claude Messages SSE frames (event: + data: + blank line).
//
// This convenience wrapper uses a one-shot translator and is fine for isolated
// events or bare completed-response synthesis. For multi-event live streams
// (correct block indices, no duplicate output_item.done payloads), use
// ClaudeSSETranslator.Event on a single instance for the whole session.
func XAIEventToClaudeSSE(eventType string, data []byte) ([][]byte, error) {
	return NewClaudeSSETranslator().Event(eventType, data)
}

// Event converts one upstream xAI Responses event into zero or more Claude
// Messages SSE frames, updating translator state.
//
// eventType may be empty; when empty, type is read from data JSON.
func (t *ClaudeSSETranslator) Event(eventType string, data []byte) ([][]byte, error) {
	if t == nil {
		t = NewClaudeSSETranslator()
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	if bytes.HasPrefix(data, []byte("data:")) {
		data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("data:")))
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, nil
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, nil
	}
	typ := eventType
	if typ == "" {
		typ = asString(root["type"])
	}
	if typ == "" {
		// Bare completed response object → synthesize non-stream as SSE lifecycle.
		if root["output"] != nil && asString(root["id"]) != "" {
			return synthesizeClaudeSSEFromResponse(root)
		}
		return nil, nil
	}

	switch typ {
	case "response.created":
		resp, _ := root["response"].(map[string]any)
		if resp == nil {
			resp = root
		}
		msg := map[string]any{
			"id":            firstNonEmpty(asString(resp["id"]), asString(root["id"])),
			"type":          "message",
			"role":          "assistant",
			"model":         firstNonEmpty(asString(resp["model"]), asString(root["model"])),
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		}
		payload := map[string]any{
			"type":    "message_start",
			"message": msg,
		}
		return [][]byte{frameClaudeSSE("message_start", payload)}, nil

	case "response.output_text.delta":
		delta, _ := root["delta"].(string)
		if delta == "" {
			return nil, nil
		}
		frames := t.ensureThinkingStopped(nil)
		frames = t.ensureBlockStart(frames, "text", nil)
		t.markStreamed("text")
		idx := t.blockIndex("text")
		frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{
				"type": "text_delta",
				"text": delta,
			},
		}))
		return frames, nil

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		kind, text, ok := thinking.ExtractReasoningFromXAIEvent(root)
		if !ok || kind != "delta" || text == "" {
			return nil, nil
		}
		frames := t.ensureBlockStart(nil, "thinking", nil)
		t.markStreamed("thinking")
		idx := t.blockIndex("thinking")
		frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": text,
			},
		}))
		return frames, nil

	case "response.function_call_arguments.delta":
		delta, _ := root["delta"].(string)
		if delta == "" {
			return nil, nil
		}
		// Tool block should already be open via output_item.added; if not, open a
		// placeholder so deltas are not lost.
		frames := t.ensureThinkingStopped(nil)
		frames = t.ensureTextStopped(frames)
		frames = t.ensureBlockStart(frames, "tool_use", map[string]any{
			"type":  "tool_use",
			"id":    "",
			"name":  "",
			"input": map[string]any{},
		})
		t.markStreamed("tool_use")
		idx := t.blockIndex("tool_use")
		frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": delta,
			},
		}))
		return frames, nil

	case "response.output_item.added":
		item, _ := root["item"].(map[string]any)
		if item == nil {
			return nil, nil
		}
		switch asString(item["type"]) {
		case "function_call":
			name := asString(item["name"])
			if name == "" {
				return nil, nil
			}
			frames := t.ensureThinkingStopped(nil)
			frames = t.ensureTextStopped(frames)
			// New tool call: close any previous tool block first.
			frames = t.ensureToolStopped(frames)
			frames = t.ensureBlockStart(frames, "tool_use", map[string]any{
				"type":  "tool_use",
				"id":    asString(item["call_id"]),
				"name":  name,
				"input": map[string]any{},
			})
			return frames, nil
		case "message":
			// Text block start usually arrives via content_part.added; keep index
			// allocation there / on first text delta.
			return t.ensureThinkingStopped(nil), nil
		case "reasoning":
			frames := t.ensureBlockStart(nil, "thinking", map[string]any{
				"type":     "thinking",
				"thinking": "",
			})
			return frames, nil
		default:
			return nil, nil
		}

	case "response.content_part.added":
		part, _ := root["part"].(map[string]any)
		if part == nil {
			return nil, nil
		}
		if asString(part["type"]) == "output_text" {
			frames := t.ensureThinkingStopped(nil)
			frames = t.ensureBlockStart(frames, "text", map[string]any{
				"type": "text",
				"text": "",
			})
			return frames, nil
		}
		return nil, nil

	case "response.content_part.done":
		part, _ := root["part"].(map[string]any)
		if part != nil && (asString(part["type"]) == "output_text" || asString(part["type"]) == "text") {
			return t.ensureTextStopped(nil), nil
		}
		return nil, nil

	case "response.output_item.done":
		item, _ := root["item"].(map[string]any)
		if item == nil {
			return nil, nil
		}
		switch asString(item["type"]) {
		case "function_call":
			frames := t.ensureThinkingStopped(nil)
			frames = t.ensureTextStopped(frames)
			// Open tool block if we never saw output_item.added.
			if t.tool == nil || t.tool.stopped {
				frames = t.ensureBlockStart(frames, "tool_use", map[string]any{
					"type":  "tool_use",
					"id":    asString(item["call_id"]),
					"name":  asString(item["name"]),
					"input": map[string]any{},
				})
			}
			// Skip full-args re-emit when function_call_arguments.delta already
			// streamed content (aligns with OpenAI chat translator skipping
			// output_item.done for already-streamed payloads).
			if t.tool != nil && !t.tool.streamed {
				if args := asString(item["arguments"]); args != "" {
					t.markStreamed("tool_use")
					idx := t.blockIndex("tool_use")
					frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": idx,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": args,
						},
					}))
				}
			}
			frames = t.ensureToolStopped(frames)
			return frames, nil

		case "message":
			frames := t.ensureThinkingStopped(nil)
			// If text already streamed via output_text.delta, only stop — do not
			// re-emit the full text (would duplicate client-side concatenation).
			if t.text != nil && t.text.streamed {
				frames = t.ensureTextStopped(frames)
				return frames, nil
			}
			text := messageItemText(item)
			if text == "" {
				// No content and nothing streamed — still close an open empty block.
				frames = t.ensureTextStopped(frames)
				return frames, nil
			}
			frames = t.ensureBlockStart(frames, "text", map[string]any{
				"type": "text",
				"text": "",
			})
			t.markStreamed("text")
			idx := t.blockIndex("text")
			frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": idx,
				"delta": map[string]any{
					"type": "text_delta",
					"text": text,
				},
			}))
			frames = t.ensureTextStopped(frames)
			return frames, nil

		case "reasoning":
			// If reasoning never streamed, emit full text at done.
			frames := [][]byte{}
			if t.thinking == nil || !t.thinking.streamed {
				rt := reasoningTextFromItem(item)
				if rt != "" {
					frames = t.ensureBlockStart(frames, "thinking", map[string]any{
						"type":     "thinking",
						"thinking": "",
					})
					t.markStreamed("thinking")
					idx := t.blockIndex("thinking")
					frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": idx,
						"delta": map[string]any{
							"type":     "thinking_delta",
							"thinking": rt,
						},
					}))
				}
			}
			frames = t.ensureThinkingStopped(frames)
			return frames, nil

		default:
			return nil, nil
		}

	case "response.completed", "response.incomplete":
		resp := unwrapResponse(root)
		frames := t.ensureThinkingStopped(nil)
		frames = t.ensureTextStopped(frames)
		frames = t.ensureToolStopped(frames)

		_, hasTool := claudeContentFromOutput(resp["output"])
		stop := "end_turn"
		if hasTool {
			stop = "tool_use"
		} else if typ == "response.incomplete" {
			if reason := asString(mapPath(resp, "incomplete_details", "reason")); reason == "max_output_tokens" || reason == "max_tokens" {
				stop = "max_tokens"
			}
		}
		usage := claudeUsage(resp["usage"])
		if usage == nil {
			usage = map[string]any{"input_tokens": 0, "output_tokens": 0}
		}
		deltaPayload := map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stop,
				"stop_sequence": nil,
			},
			"usage": usage,
		}
		frames = append(frames,
			frameClaudeSSE("message_delta", deltaPayload),
			frameClaudeSSE("message_stop", map[string]any{"type": "message_stop"}),
		)
		return frames, nil

	case "response.in_progress",
		"response.output_text.done",
		"response.function_call_arguments.done",
		"response.reasoning_summary_part.added",
		"response.reasoning_summary_part.done",
		"response.reasoning_summary_text.done",
		"response.reasoning_text.done":
		return nil, nil

	default:
		return nil, nil
	}
}

func messageItemText(item map[string]any) string {
	if parts, ok := item["content"].([]any); ok {
		var b strings.Builder
		for _, p := range parts {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if asString(pm["type"]) == "output_text" || asString(pm["type"]) == "text" {
				b.WriteString(asString(pm["text"]))
			}
		}
		return b.String()
	}
	return ""
}

func (t *ClaudeSSETranslator) block(kind string) *claudeBlock {
	switch kind {
	case "text":
		return t.text
	case "thinking":
		return t.thinking
	case "tool_use":
		return t.tool
	default:
		return nil
	}
}

func (t *ClaudeSSETranslator) setBlock(kind string, b *claudeBlock) {
	switch kind {
	case "text":
		t.text = b
	case "thinking":
		t.thinking = b
	case "tool_use":
		t.tool = b
	}
}

func (t *ClaudeSSETranslator) blockIndex(kind string) int {
	if b := t.block(kind); b != nil {
		return b.index
	}
	return 0
}

func (t *ClaudeSSETranslator) markStreamed(kind string) {
	if b := t.block(kind); b != nil {
		b.streamed = true
	}
}

func (t *ClaudeSSETranslator) ensureBlockStart(frames [][]byte, kind string, contentBlock map[string]any) [][]byte {
	b := t.block(kind)
	if b != nil && b.started && !b.stopped {
		return frames
	}
	// Allocate a new block (previous one of same kind already stopped, or first).
	idx := t.nextIndex
	t.nextIndex++
	nb := &claudeBlock{index: idx, kind: kind, started: true}
	t.setBlock(kind, nb)
	if contentBlock == nil {
		switch kind {
		case "text":
			contentBlock = map[string]any{"type": "text", "text": ""}
		case "thinking":
			contentBlock = map[string]any{"type": "thinking", "thinking": ""}
		case "tool_use":
			contentBlock = map[string]any{
				"type":  "tool_use",
				"id":    "",
				"name":  "",
				"input": map[string]any{},
			}
		}
	}
	return append(frames, frameClaudeSSE("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         idx,
		"content_block": contentBlock,
	}))
}

func (t *ClaudeSSETranslator) stopBlock(frames [][]byte, kind string) [][]byte {
	b := t.block(kind)
	if b == nil || !b.started || b.stopped {
		return frames
	}
	b.stopped = true
	return append(frames, frameClaudeSSE("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": b.index,
	}))
}

func (t *ClaudeSSETranslator) ensureTextStopped(frames [][]byte) [][]byte {
	return t.stopBlock(frames, "text")
}

func (t *ClaudeSSETranslator) ensureThinkingStopped(frames [][]byte) [][]byte {
	return t.stopBlock(frames, "thinking")
}

func (t *ClaudeSSETranslator) ensureToolStopped(frames [][]byte) [][]byte {
	return t.stopBlock(frames, "tool_use")
}

func synthesizeClaudeSSEFromResponse(resp map[string]any) ([][]byte, error) {
	frames := make([][]byte, 0, 8)
	// message_start
	msg := map[string]any{
		"id":            asString(resp["id"]),
		"type":          "message",
		"role":          "assistant",
		"model":         asString(resp["model"]),
		"content":       []any{},
		"stop_reason":   nil,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  0,
			"output_tokens": 0,
		},
	}
	frames = append(frames, frameClaudeSSE("message_start", map[string]any{
		"type":    "message_start",
		"message": msg,
	}))

	content, hasTool := claudeContentFromOutput(resp["output"])
	for i, block := range content {
		bm, _ := block.(map[string]any)
		if bm == nil {
			continue
		}
		switch asString(bm["type"]) {
		case "text":
			frames = append(frames, frameClaudeSSE("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": i,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			}))
			frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": i,
				"delta": map[string]any{
					"type": "text_delta",
					"text": asString(bm["text"]),
				},
			}))
			frames = append(frames, frameClaudeSSE("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": i,
			}))
		case "thinking":
			frames = append(frames, frameClaudeSSE("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": i,
				"content_block": map[string]any{
					"type":     "thinking",
					"thinking": "",
				},
			}))
			frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": i,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": asString(bm["thinking"]),
				},
			}))
			frames = append(frames, frameClaudeSSE("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": i,
			}))
		case "tool_use":
			frames = append(frames, frameClaudeSSE("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": i,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    asString(bm["id"]),
					"name":  asString(bm["name"]),
					"input": map[string]any{},
				},
			}))
			// Emit full input as one partial_json.
			argBytes, _ := json.Marshal(bm["input"])
			frames = append(frames, frameClaudeSSE("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": i,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": string(argBytes),
				},
			}))
			frames = append(frames, frameClaudeSSE("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": i,
			}))
		}
	}

	stop := "end_turn"
	if hasTool {
		stop = "tool_use"
	}
	usage := claudeUsage(resp["usage"])
	if usage == nil {
		usage = map[string]any{"input_tokens": 0, "output_tokens": 0}
	}
	frames = append(frames, frameClaudeSSE("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stop,
			"stop_sequence": nil,
		},
		"usage": usage,
	}))
	frames = append(frames, frameClaudeSSE("message_stop", map[string]any{"type": "message_stop"}))
	return frames, nil
}

func frameClaudeSSE(event string, payload map[string]any) []byte {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString("event: ")
	buf.WriteString(event)
	buf.WriteString("\n")
	buf.WriteString("data: ")
	buf.Write(b)
	buf.WriteString("\n\n")
	return buf.Bytes()
}
