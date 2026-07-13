package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wlhet/grok-bridge/internal/thinking"
)

// ChatCompletionsToXAI converts an OpenAI Chat Completions request into an
// xAI Responses request (model + input array + flattened tools).
func ChatCompletionsToXAI(body []byte, model string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("chat request json: %w", err)
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

	// Pass through generation knobs that Responses understands.
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
	if v, ok := req["max_completion_tokens"]; ok {
		if _, exists := out["max_output_tokens"]; !exists {
			out["max_output_tokens"] = v
		}
	}
	if v, ok := req["reasoning_effort"]; ok {
		out["reasoning_effort"] = v
	}
	if v, ok := req["reasoning"]; ok {
		out["reasoning"] = v
	}
	if v, ok := req["include"]; ok {
		out["include"] = v
	}

	// Messages → input
	if msgs, ok := req["messages"].([]any); ok {
		out["input"] = chatMessagesToInput(msgs)
	} else {
		out["input"] = []any{}
	}

	// Tools: flatten Chat Completions nested function schema.
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		out["tools"] = mapChatTools(tools)
	}
	if tc, ok := req["tool_choice"]; ok {
		out["tool_choice"] = mapChatToolChoice(tc)
	}

	// response_format → text.format
	if rf, ok := req["response_format"].(map[string]any); ok {
		out["text"] = mapResponseFormat(rf, req["text"])
	} else if text, ok := req["text"]; ok {
		out["text"] = text
	}

	// Apply OpenAI→xAI reasoning mapping (reasoning_effort, include cleanup).
	out = thinking.ApplyOpenAIToXAI(out)

	return json.Marshal(out)
}

func chatMessagesToInput(msgs []any) []any {
	input := make([]any, 0, len(msgs))
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "tool":
			item := map[string]any{
				"type":    "function_call_output",
				"call_id": asString(m["tool_call_id"]),
				"output":  toolOutputContent(m["content"]),
			}
			input = append(input, item)
		default:
			msgRole := role
			if role == "system" {
				msgRole = "developer"
			}
			contentParts := chatContentToParts(m["content"], role)
			// Emit message when there is content (skip empty assistant with only tool_calls).
			if role != "assistant" || len(contentParts) > 0 {
				item := map[string]any{
					"type":    "message",
					"role":    msgRole,
					"content": contentParts,
				}
				input = append(input, item)
			}
			// Assistant tool_calls → top-level function_call items.
			if role == "assistant" {
				if tcs, ok := m["tool_calls"].([]any); ok {
					for _, tcRaw := range tcs {
						tc, ok := tcRaw.(map[string]any)
						if !ok {
							continue
						}
						fn, _ := tc["function"].(map[string]any)
						if fn == nil {
							fn = map[string]any{}
						}
						input = append(input, map[string]any{
							"type":      "function_call",
							"call_id":   asString(tc["id"]),
							"name":      asString(fn["name"]),
							"arguments": asString(fn["arguments"]),
						})
					}
				}
			}
		}
	}
	return input
}

func chatContentToParts(content any, role string) []any {
	partType := "input_text"
	if role == "assistant" {
		partType = "output_text"
	}
	switch c := content.(type) {
	case nil:
		return nil
	case string:
		if c == "" {
			return nil
		}
		return []any{map[string]any{"type": partType, "text": c}}
	case []any:
		parts := make([]any, 0, len(c))
		for _, raw := range c {
			it, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch asString(it["type"]) {
			case "text", "":
				text := asString(it["text"])
				if text == "" {
					continue
				}
				parts = append(parts, map[string]any{"type": partType, "text": text})
			case "image_url":
				if role != "user" {
					continue
				}
				url := ""
				if iu, ok := it["image_url"].(map[string]any); ok {
					url = asString(iu["url"])
				} else {
					url = asString(it["image_url"])
				}
				if url == "" {
					continue
				}
				parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
			case "input_text", "output_text", "input_image", "input_file", "input_audio":
				// Already Responses-shaped content part.
				parts = append(parts, it)
			}
		}
		return parts
	default:
		// Fallback: stringify unknown content.
		b, err := json.Marshal(c)
		if err != nil {
			return nil
		}
		return []any{map[string]any{"type": partType, "text": string(b)}}
	}
}

func toolOutputContent(content any) any {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		// Prefer concatenated text parts; otherwise pass array through.
		var b strings.Builder
		onlyText := true
		for _, raw := range c {
			it, ok := raw.(map[string]any)
			if !ok {
				onlyText = false
				break
			}
			typ := asString(it["type"])
			if typ != "" && typ != "text" && typ != "output_text" && typ != "input_text" {
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
		b, err := json.Marshal(c)
		if err != nil {
			return fmt.Sprint(c)
		}
		return string(b)
	}
}

func mapChatTools(tools []any) []any {
	out := make([]any, 0, len(tools))
	for _, raw := range tools {
		t, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := asString(t["type"])
		if typ != "" && typ != "function" {
			// Built-in tools already Responses-shaped.
			out = append(out, t)
			continue
		}
		item := map[string]any{"type": "function"}
		if fn, ok := t["function"].(map[string]any); ok {
			if v, ok := fn["name"]; ok {
				item["name"] = v
			}
			if v, ok := fn["description"]; ok {
				item["description"] = v
			}
			if v, ok := fn["parameters"]; ok {
				item["parameters"] = v
			}
			if v, ok := fn["strict"]; ok {
				item["strict"] = v
			}
		} else {
			// Already flat.
			if v, ok := t["name"]; ok {
				item["name"] = v
			}
			if v, ok := t["description"]; ok {
				item["description"] = v
			}
			if v, ok := t["parameters"]; ok {
				item["parameters"] = v
			}
			if v, ok := t["strict"]; ok {
				item["strict"] = v
			}
		}
		out = append(out, item)
	}
	return out
}

func mapChatToolChoice(tc any) any {
	switch v := tc.(type) {
	case string:
		return v
	case map[string]any:
		if asString(v["type"]) == "function" {
			name := ""
			if fn, ok := v["function"].(map[string]any); ok {
				name = asString(fn["name"])
			}
			if name == "" {
				name = asString(v["name"])
			}
			choice := map[string]any{"type": "function"}
			if name != "" {
				choice["name"] = name
			}
			return choice
		}
		return v
	default:
		return tc
	}
}

func mapResponseFormat(rf map[string]any, text any) map[string]any {
	out := map[string]any{}
	if tm, ok := text.(map[string]any); ok {
		for k, v := range tm {
			out[k] = v
		}
	}
	format := map[string]any{}
	switch asString(rf["type"]) {
	case "text":
		format["type"] = "text"
	case "json_schema":
		format["type"] = "json_schema"
		if js, ok := rf["json_schema"].(map[string]any); ok {
			if v, ok := js["name"]; ok {
				format["name"] = v
			}
			if v, ok := js["strict"]; ok {
				format["strict"] = v
			}
			if v, ok := js["schema"]; ok {
				format["schema"] = v
			}
		}
	case "json_object":
		format["type"] = "json_object"
	default:
		if t := asString(rf["type"]); t != "" {
			format["type"] = t
		}
	}
	if len(format) > 0 {
		out["format"] = format
	}
	return out
}

// XAIResponseToChatCompletions converts an xAI Responses payload to OpenAI
// Chat Completions. When stream is false, body is a completed response (or
// response.completed envelope) and a single chat.completion JSON is returned.
// When stream is true, body is one xAI SSE event JSON (or data: line) and a
// chat.completion.chunk SSE frame is returned; skippable events yield empty
// bytes; response.completed appends data: [DONE].
func XAIResponseToChatCompletions(body []byte, stream bool) ([]byte, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty xai response body")
	}
	// Accept SSE-framed input.
	if bytes.HasPrefix(body, []byte("data:")) {
		payload := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("data:")))
		if bytes.Equal(payload, []byte("[DONE]")) {
			if stream {
				return []byte("data: [DONE]\n\n"), nil
			}
			return nil, fmt.Errorf("unexpected [DONE] for non-stream conversion")
		}
		body = payload
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("xai response json: %w", err)
	}

	if stream {
		return xaiEventToChatSSE(root)
	}
	return xaiToChatCompletion(root)
}

func xaiToChatCompletion(root map[string]any) ([]byte, error) {
	resp := unwrapResponse(root)

	id := asString(resp["id"])
	model := asString(resp["model"])
	created := int64From(resp["created_at"])
	if created == 0 {
		created = time.Now().Unix()
	}

	contentText, reasoningText, toolCalls := extractOutput(resp["output"])

	msg := map[string]any{"role": "assistant"}
	if contentText != "" {
		msg["content"] = contentText
	} else {
		msg["content"] = nil
	}
	if reasoningText != "" {
		msg["reasoning_content"] = reasoningText
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}

	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	// Honor incomplete status when present.
	if status := asString(resp["status"]); status == "incomplete" || status == "failed" {
		finish = "stop"
	}

	choice := map[string]any{
		"index":         0,
		"message":       msg,
		"finish_reason": finish,
	}

	out := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{choice},
	}
	if usage := mapUsage(resp["usage"]); usage != nil {
		out["usage"] = usage
	}
	return json.Marshal(out)
}

func xaiEventToChatSSE(root map[string]any) ([]byte, error) {
	typ := asString(root["type"])

	// Full response object without event type → synthesize stream from content.
	if typ == "" && root["output"] != nil {
		return synthesizeChatSSEFromResponse(root)
	}

	base := map[string]any{
		"id":      firstNonEmpty(asString(root["id"]), asString(mapPath(root, "response", "id"))),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   firstNonEmpty(asString(root["model"]), asString(mapPath(root, "response", "model"))),
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": nil,
			},
		},
	}
	if created := int64From(mapPath(root, "response", "created_at")); created != 0 {
		base["created"] = created
	}

	switch typ {
	case "response.created", "response.in_progress", "response.output_item.added",
		"response.output_item.done", "response.content_part.added", "response.content_part.done",
		"response.output_text.done", "response.function_call_arguments.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
		"response.reasoning_summary_text.done", "response.reasoning_text.done":
		return []byte{}, nil

	case "response.output_text.delta":
		delta, _ := root["delta"].(string)
		if delta == "" {
			return []byte{}, nil
		}
		setChatDelta(base, map[string]any{"role": "assistant", "content": delta})
		return frameChatSSE(base)

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		delta, _ := root["delta"].(string)
		if delta == "" {
			return []byte{}, nil
		}
		setChatDelta(base, map[string]any{"role": "assistant", "reasoning_content": delta})
		return frameChatSSE(base)

	case "response.function_call_arguments.delta":
		delta, _ := root["delta"].(string)
		if delta == "" {
			return []byte{}, nil
		}
		// Minimal tool-call args delta (index 0).
		tc := map[string]any{
			"index": 0,
			"function": map[string]any{
				"arguments": delta,
			},
		}
		setChatDelta(base, map[string]any{"tool_calls": []any{tc}})
		return frameChatSSE(base)

	case "response.completed":
		resp := unwrapResponse(root)
		_, _, toolCalls := extractOutput(resp["output"])
		finish := "stop"
		if len(toolCalls) > 0 {
			finish = "tool_calls"
		}
		if usage := mapUsage(resp["usage"]); usage != nil {
			base["usage"] = usage
		}
		if id := asString(resp["id"]); id != "" {
			base["id"] = id
		}
		if model := asString(resp["model"]); model != "" {
			base["model"] = model
		}
		choices := base["choices"].([]any)
		ch := choices[0].(map[string]any)
		ch["delta"] = map[string]any{}
		ch["finish_reason"] = finish
		frame, err := frameChatSSE(base)
		if err != nil {
			return nil, err
		}
		return append(frame, []byte("data: [DONE]\n\n")...), nil

	default:
		// Unknown event types are skipped.
		return []byte{}, nil
	}
}

func synthesizeChatSSEFromResponse(resp map[string]any) ([]byte, error) {
	content, reasoning, toolCalls := extractOutput(resp["output"])
	id := asString(resp["id"])
	model := asString(resp["model"])
	created := int64From(resp["created_at"])
	if created == 0 {
		created = time.Now().Unix()
	}

	var buf bytes.Buffer
	writeChunk := func(delta map[string]any, finish any) error {
		chunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []any{
				map[string]any{
					"index":         0,
					"delta":         delta,
					"finish_reason": finish,
				},
			},
		}
		b, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		buf.WriteString("data: ")
		buf.Write(b)
		buf.WriteString("\n\n")
		return nil
	}

	if err := writeChunk(map[string]any{"role": "assistant"}, nil); err != nil {
		return nil, err
	}
	if reasoning != "" {
		if err := writeChunk(map[string]any{"reasoning_content": reasoning}, nil); err != nil {
			return nil, err
		}
	}
	if content != "" {
		if err := writeChunk(map[string]any{"content": content}, nil); err != nil {
			return nil, err
		}
	}
	if len(toolCalls) > 0 {
		if err := writeChunk(map[string]any{"tool_calls": toolCalls}, nil); err != nil {
			return nil, err
		}
	}
	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	if err := writeChunk(map[string]any{}, finish); err != nil {
		return nil, err
	}
	buf.WriteString("data: [DONE]\n\n")
	return buf.Bytes(), nil
}

func setChatDelta(base map[string]any, delta map[string]any) {
	choices := base["choices"].([]any)
	ch := choices[0].(map[string]any)
	ch["delta"] = delta
}

func frameChatSSE(base map[string]any) ([]byte, error) {
	b, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	return append([]byte("data: "), append(b, []byte("\n\n")...)...), nil
}

func unwrapResponse(root map[string]any) map[string]any {
	if root == nil {
		return map[string]any{}
	}
	if asString(root["type"]) == "response.completed" {
		if resp, ok := root["response"].(map[string]any); ok && resp != nil {
			return resp
		}
	}
	if resp, ok := root["response"].(map[string]any); ok && resp != nil && root["output"] == nil {
		// Some envelopes nest response without a type.
		if _, hasOutput := resp["output"]; hasOutput || asString(resp["id"]) != "" {
			return resp
		}
	}
	return root
}

func extractOutput(output any) (content string, reasoning string, toolCalls []any) {
	arr, ok := output.([]any)
	if !ok {
		return "", "", nil
	}
	var contentB, reasoningB strings.Builder
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
						contentB.WriteString(asString(pm["text"]))
					}
				}
			}
		case "reasoning":
			if summary, ok := item["summary"].([]any); ok {
				for _, s := range summary {
					sm, ok := s.(map[string]any)
					if !ok {
						continue
					}
					if t := asString(sm["text"]); t != "" {
						reasoningB.WriteString(t)
					}
				}
			}
			if reasoningB.Len() == 0 {
				if parts, ok := item["content"].([]any); ok {
					for _, p := range parts {
						pm, ok := p.(map[string]any)
						if !ok {
							continue
						}
						if t := asString(pm["text"]); t != "" {
							reasoningB.WriteString(t)
						}
					}
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, map[string]any{
				"id":   asString(item["call_id"]),
				"type": "function",
				"function": map[string]any{
					"name":      asString(item["name"]),
					"arguments": asString(item["arguments"]),
				},
			})
		}
	}
	return contentB.String(), reasoningB.String(), toolCalls
}

func mapUsage(usage any) map[string]any {
	u, ok := usage.(map[string]any)
	if !ok || u == nil {
		return nil
	}
	out := map[string]any{}
	if v, ok := u["input_tokens"]; ok {
		out["prompt_tokens"] = v
	}
	if v, ok := u["output_tokens"]; ok {
		out["completion_tokens"] = v
	}
	if v, ok := u["total_tokens"]; ok {
		out["total_tokens"] = v
	} else {
		// Best-effort sum.
		pt, _ := asFloat(out["prompt_tokens"])
		ct, _ := asFloat(out["completion_tokens"])
		if pt != 0 || ct != 0 {
			out["total_tokens"] = pt + ct
		}
	}
	if details, ok := u["input_tokens_details"].(map[string]any); ok {
		pt := map[string]any{}
		if v, ok := details["cached_tokens"]; ok {
			pt["cached_tokens"] = v
		}
		if len(pt) > 0 {
			out["prompt_tokens_details"] = pt
		}
	}
	if details, ok := u["output_tokens_details"].(map[string]any); ok {
		ct := map[string]any{}
		if v, ok := details["reasoning_tokens"]; ok {
			ct["reasoning_tokens"] = v
		}
		if len(ct) > 0 {
			out["completion_tokens_details"] = ct
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	case nil:
		return ""
	default:
		b, err := json.Marshal(s)
		if err != nil {
			return fmt.Sprint(s)
		}
		// Avoid quoting plain strings twice — only for non-string types.
		return string(b)
	}
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func int64From(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return i
	default:
		return 0
	}
}

func mapPath(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
