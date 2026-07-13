package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wlhet/grok-bridge/internal/thinking"
)

// ResponsesToXAI normalizes an OpenAI Responses request for the xAI upstream.
// The schemas are largely compatible; this sets model, maps tools if nested,
// and applies OpenAI reasoning helpers.
func ResponsesToXAI(body []byte, model string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("responses request json: %w", err)
	}
	if req == nil {
		req = map[string]any{}
	}

	if model != "" {
		req["model"] = model
	}

	// Ensure input exists as array when client sent string input.
	if in, ok := req["input"]; ok {
		switch v := in.(type) {
		case string:
			req["input"] = []any{
				map[string]any{
					"type": "message",
					"role": "user",
					"content": []any{
						map[string]any{"type": "input_text", "text": v},
					},
				},
			}
		case []any:
			// Normalize any nested chat-style tools inside messages if present — leave items as-is.
			_ = v
		}
	} else {
		req["input"] = []any{}
	}

	// Flatten tools if client sent Chat Completions nested function form.
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		req["tools"] = mapChatTools(tools)
	}
	if tc, ok := req["tool_choice"]; ok {
		req["tool_choice"] = mapChatToolChoice(tc)
	}

	req = thinking.ApplyOpenAIToXAI(req)
	return json.Marshal(req)
}

// XAIEventToResponsesSSE converts one upstream xAI SSE line (or raw event JSON)
// into a Responses-compatible SSE frame. Returns nil to skip the line.
func XAIEventToResponsesSSE(line []byte) ([]byte, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	// Keep event: name lines as-is (with trailing blank handled by caller).
	if bytes.HasPrefix(line, []byte("event:")) {
		out := append([]byte{}, line...)
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		return out, nil
	}
	// SSE comments / keep-alives.
	if bytes.HasPrefix(line, []byte(":")) {
		return nil, nil
	}

	payload := line
	if bytes.HasPrefix(line, []byte("data:")) {
		payload = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	}
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return nil, nil
	}

	// Validate JSON event; skip non-JSON.
	if !json.Valid(payload) {
		return nil, nil
	}

	// Optional light normalization of reasoning event types for clients.
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err == nil && root != nil {
		switch asString(root["type"]) {
		case "response.reasoning_text.delta":
			root["type"] = "response.reasoning_summary_text.delta"
			if b, err := json.Marshal(root); err == nil {
				payload = b
			}
		case "response.reasoning_text.done":
			root["type"] = "response.reasoning_summary_text.done"
			if b, err := json.Marshal(root); err == nil {
				payload = b
			}
		}
	}

	var buf bytes.Buffer
	buf.WriteString("data: ")
	buf.Write(payload)
	buf.WriteString("\n\n")
	return buf.Bytes(), nil
}

// XAIResponseToResponses converts a completed xAI payload into an OpenAI
// Responses response object (unwraps response.completed envelopes).
func XAIResponseToResponses(body []byte) ([]byte, error) {
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
	// If still an event envelope without a nested response, error clearly.
	if asString(resp["type"]) != "" && strings.HasPrefix(asString(resp["type"]), "response.") && resp["output"] == nil {
		if nested, ok := resp["response"].(map[string]any); ok {
			resp = nested
		}
	}
	return json.Marshal(resp)
}
