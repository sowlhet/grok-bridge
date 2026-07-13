package translate_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wlhet/grok-bridge/internal/translate"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func asMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", string(b), err)
	}
	return m
}

func TestChatCompletionsToXAI_basicUserMessage(t *testing.T) {
	in := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"stream": false,
	})

	out, err := translate.ChatCompletionsToXAI(in, "grok-4.5")
	if err != nil {
		t.Fatalf("ChatCompletionsToXAI: %v", err)
	}
	m := asMap(t, out)

	if m["model"] != "grok-4.5" {
		t.Fatalf("model: got %v want grok-4.5", m["model"])
	}
	input, ok := m["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("expected 1 input item, got %#v", m["input"])
	}
	item := input[0].(map[string]any)
	if item["type"] != "message" {
		t.Fatalf("input type: %v", item["type"])
	}
	if item["role"] != "user" {
		t.Fatalf("input role: %v", item["role"])
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content: %#v", item["content"])
	}
	part := content[0].(map[string]any)
	if part["type"] != "input_text" {
		t.Fatalf("part type: %v", part["type"])
	}
	if part["text"] != "Hello" {
		t.Fatalf("part text: %v", part["text"])
	}
	// Chat-only fields should not leak as top-level messages.
	if _, ok := m["messages"]; ok {
		t.Fatalf("messages should be stripped, got %#v", m["messages"])
	}
}

func TestChatCompletionsToXAI_toolsAndSystemAndToolCalls(t *testing.T) {
	in := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{"role": "system", "content": "Be helpful."},
			map[string]any{"role": "user", "content": "Weather in Paris?"},
			map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []any{
					map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "get_weather",
							"arguments": `{"city":"Paris"}`,
						},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call_1",
				"content":      "sunny, 22C",
			},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "Get weather for a city",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"tool_choice":      "auto",
		"reasoning_effort": "high",
	})

	out, err := translate.ChatCompletionsToXAI(in, "grok-4.5")
	if err != nil {
		t.Fatalf("ChatCompletionsToXAI: %v", err)
	}
	m := asMap(t, out)

	if _, ok := m["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be mapped into reasoning.effort")
	}
	reasoning, _ := m["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "high" {
		t.Fatalf("reasoning.effort: %#v", m["reasoning"])
	}

	input, ok := m["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("expected 4 input items, got %#v", m["input"])
	}
	// system -> developer message
	sys := input[0].(map[string]any)
	if sys["role"] != "developer" {
		t.Fatalf("system role mapped to: %v", sys["role"])
	}
	// function_call top-level
	fc := input[2].(map[string]any)
	if fc["type"] != "function_call" {
		t.Fatalf("item 2 type: %v", fc["type"])
	}
	if fc["call_id"] != "call_1" || fc["name"] != "get_weather" {
		t.Fatalf("function_call: %#v", fc)
	}
	// function_call_output
	fco := input[3].(map[string]any)
	if fco["type"] != "function_call_output" || fco["output"] != "sunny, 22C" {
		t.Fatalf("function_call_output: %#v", fco)
	}

	tools, ok := m["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools: %#v", m["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("tool type: %v", tool["type"])
	}
	if tool["name"] != "get_weather" {
		t.Fatalf("tool name should be flattened, got %#v", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Fatalf("tools must not nest function object for xAI: %#v", tool)
	}
	if m["tool_choice"] != "auto" {
		t.Fatalf("tool_choice: %v", m["tool_choice"])
	}
}

func TestXAIResponseToChatCompletions_nonStream(t *testing.T) {
	// Accept either bare response object or response.completed wrapper.
	body := mustJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         "resp_123",
			"created_at": float64(1700000000),
			"model":      "grok-4.5",
			"status":     "completed",
			"usage": map[string]any{
				"input_tokens":  float64(10),
				"output_tokens": float64(5),
				"total_tokens":  float64(15),
			},
			"output": []any{
				map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []any{
						map[string]any{"type": "output_text", "text": "Hello back"},
					},
				},
			},
		},
	})

	out, err := translate.XAIResponseToChatCompletions(body, false)
	if err != nil {
		t.Fatalf("XAIResponseToChatCompletions: %v", err)
	}
	m := asMap(t, out)
	if m["object"] != "chat.completion" {
		t.Fatalf("object: %v", m["object"])
	}
	if m["id"] != "resp_123" {
		t.Fatalf("id: %v", m["id"])
	}
	if m["model"] != "grok-4.5" {
		t.Fatalf("model: %v", m["model"])
	}
	choices, ok := m["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices: %#v", m["choices"])
	}
	ch := choices[0].(map[string]any)
	msg := ch["message"].(map[string]any)
	if msg["role"] != "assistant" {
		t.Fatalf("role: %v", msg["role"])
	}
	if msg["content"] != "Hello back" {
		t.Fatalf("content: %v", msg["content"])
	}
	if ch["finish_reason"] != "stop" {
		t.Fatalf("finish_reason: %v", ch["finish_reason"])
	}
	usage := m["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(10) || usage["completion_tokens"] != float64(5) {
		t.Fatalf("usage: %#v", usage)
	}
}

func TestXAIResponseToChatCompletions_nonStreamToolCalls(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"id":         "resp_tc",
		"created_at": float64(1700000001),
		"model":      "grok-4.5",
		"status":     "completed",
		"output": []any{
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_9",
				"name":      "get_weather",
				"arguments": `{"city":"Paris"}`,
			},
		},
	})

	out, err := translate.XAIResponseToChatCompletions(body, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := asMap(t, out)
	ch := m["choices"].([]any)[0].(map[string]any)
	if ch["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason: %v", ch["finish_reason"])
	}
	msg := ch["message"].(map[string]any)
	tcs := msg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls: %#v", tcs)
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_9" {
		t.Fatalf("id: %v", tc["id"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Fatalf("name: %v", fn["name"])
	}
}

func TestXAIResponseToChatCompletions_streamDeltas(t *testing.T) {
	// Single output_text.delta event → one chat chunk.
	deltaEvent := mustJSON(t, map[string]any{
		"type":  "response.output_text.delta",
		"delta": "Hi",
	})
	chunk, err := translate.XAIResponseToChatCompletions(deltaEvent, true)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if !bytes.HasPrefix(chunk, []byte("data: ")) {
		t.Fatalf("expected SSE data prefix, got %q", chunk)
	}
	payload := bytes.TrimSpace(bytes.TrimPrefix(chunk, []byte("data: ")))
	// strip trailing blank lines / [DONE] if present
	if i := bytes.Index(payload, []byte("\n")); i >= 0 {
		payload = payload[:i]
	}
	m := asMap(t, payload)
	if m["object"] != "chat.completion.chunk" {
		t.Fatalf("object: %v", m["object"])
	}
	ch := m["choices"].([]any)[0].(map[string]any)
	delta := ch["delta"].(map[string]any)
	if delta["content"] != "Hi" {
		t.Fatalf("delta content: %#v", delta)
	}

	// completed → finish chunk + [DONE]
	completed := mustJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_s",
			"model":  "grok-4.5",
			"status": "completed",
			"output": []any{},
		},
	})
	doneChunk, err := translate.XAIResponseToChatCompletions(completed, true)
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	s := string(doneChunk)
	if !strings.Contains(s, `"finish_reason"`) {
		t.Fatalf("expected finish_reason in stream end: %s", s)
	}
	if !strings.Contains(s, "data: [DONE]") {
		t.Fatalf("expected [DONE]: %s", s)
	}

	// skippable event → empty
	skip, err := translate.XAIResponseToChatCompletions(mustJSON(t, map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": "resp_s"},
	}), true)
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if len(bytes.TrimSpace(skip)) != 0 {
		t.Fatalf("expected empty for skippable event, got %q", skip)
	}
}

func TestResponsesToXAI_passthroughWithModelAndThinking(t *testing.T) {
	in := mustJSON(t, map[string]any{
		"model": "gpt-5",
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "hi"},
				},
			},
		},
		"tools": []any{
			map[string]any{
				"type":        "function",
				"name":        "lookup",
				"description": "Lookup",
				"parameters":  map[string]any{"type": "object"},
			},
		},
		"reasoning_effort": "low",
		"include":          []any{"reasoning.encrypted_content", "file_search_call.results"},
	})

	out, err := translate.ResponsesToXAI(in, "grok-4.5")
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := asMap(t, out)
	if m["model"] != "grok-4.5" {
		t.Fatalf("model: %v", m["model"])
	}
	if _, ok := m["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be stripped")
	}
	reasoning, _ := m["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "low" {
		t.Fatalf("reasoning: %#v", m["reasoning"])
	}
	inc, _ := m["include"].([]any)
	for _, v := range inc {
		if v == "reasoning.encrypted_content" {
			t.Fatalf("include should drop encrypted_content: %#v", inc)
		}
	}
	tools := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools: %#v", tools)
	}
	if tools[0].(map[string]any)["name"] != "lookup" {
		t.Fatalf("tool passthrough: %#v", tools[0])
	}
}

func TestXAIEventToResponsesSSE(t *testing.T) {
	// Full data line passthrough (normalized to SSE frame).
	line := []byte(`data: {"type":"response.output_text.delta","delta":"x"}`)
	out, err := translate.XAIEventToResponsesSSE(line)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !bytes.Contains(out, []byte(`"delta":"x"`)) {
		t.Fatalf("unexpected: %q", out)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(out), []byte("data:")) {
		t.Fatalf("want data: prefix, got %q", out)
	}

	// Raw JSON event without data: prefix.
	raw := []byte(`{"type":"response.output_text.delta","delta":"y"}`)
	out, err = translate.XAIEventToResponsesSSE(raw)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !bytes.Contains(out, []byte(`"delta":"y"`)) {
		t.Fatalf("unexpected: %q", out)
	}

	// Comment / empty → nil skip
	skip, err := translate.XAIEventToResponsesSSE([]byte(": ping"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if skip != nil {
		t.Fatalf("expected nil skip, got %q", skip)
	}
	skip, err = translate.XAIEventToResponsesSSE([]byte(""))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if skip != nil {
		t.Fatalf("expected nil for empty")
	}

	// [DONE] is OpenAI chat-style; skip for Responses clients.
	skip, err = translate.XAIEventToResponsesSSE([]byte("data: [DONE]"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if skip != nil {
		t.Fatalf("expected nil for [DONE]")
	}
}

func TestXAIResponseToResponses(t *testing.T) {
	// Unwrap response.completed
	body := mustJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_1",
			"model":  "grok-4.5",
			"status": "completed",
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{"type": "output_text", "text": "ok"},
					},
				},
			},
		},
	})
	out, err := translate.XAIResponseToResponses(body)
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := asMap(t, out)
	if m["id"] != "resp_1" {
		t.Fatalf("id: %v", m["id"])
	}
	if _, ok := m["type"]; ok {
		t.Fatalf("should unwrap completed envelope, still has type: %#v", m)
	}
	if m["status"] != "completed" {
		t.Fatalf("status: %v", m["status"])
	}

	// Bare response object passthrough
	bare := mustJSON(t, map[string]any{
		"id":     "resp_2",
		"status": "completed",
		"output": []any{},
	})
	out, err = translate.XAIResponseToResponses(bare)
	if err != nil {
		t.Fatalf("%v", err)
	}
	m = asMap(t, out)
	if m["id"] != "resp_2" {
		t.Fatalf("bare id: %v", m["id"])
	}
}
