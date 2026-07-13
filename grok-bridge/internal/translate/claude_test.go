package translate_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wlhet/grok-bridge/internal/translate"
)

func TestClaudeMessagesToXAI_systemUserTools(t *testing.T) {
	in := mustJSON(t, map[string]any{
		"model": "claude-sonnet-4-20250514",
		"system": "You are a helpful assistant.",
		"messages": []any{
			map[string]any{"role": "user", "content": "Weather in Paris?"},
		},
		"tools": []any{
			map[string]any{
				"name":         "get_weather",
				"description":  "Get weather for a city",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []any{"city"},
				},
			},
		},
		"tool_choice": map[string]any{"type": "auto"},
		"max_tokens":  1024,
		"stream":      false,
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 2048,
		},
	})

	out, err := translate.ClaudeMessagesToXAI(in, "grok-4.5")
	if err != nil {
		t.Fatalf("ClaudeMessagesToXAI: %v", err)
	}
	m := asMap(t, out)

	if m["model"] != "grok-4.5" {
		t.Fatalf("model: got %v want grok-4.5", m["model"])
	}
	if m["instructions"] != "You are a helpful assistant." {
		t.Fatalf("instructions: got %#v", m["instructions"])
	}
	// Claude-only fields must not leak.
	if _, ok := m["system"]; ok {
		t.Fatalf("system should be stripped: %#v", m["system"])
	}
	if _, ok := m["messages"]; ok {
		t.Fatalf("messages should be stripped: %#v", m["messages"])
	}
	if _, ok := m["thinking"]; ok {
		t.Fatalf("thinking should be mapped: %#v", m["thinking"])
	}

	input, ok := m["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("expected 1 input item, got %#v", m["input"])
	}
	item := input[0].(map[string]any)
	if item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("input item: %#v", item)
	}
	content := item["content"].([]any)
	part := content[0].(map[string]any)
	if part["type"] != "input_text" || part["text"] != "Weather in Paris?" {
		t.Fatalf("content part: %#v", part)
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
		t.Fatalf("tool name: %v", tool["name"])
	}
	if _, nested := tool["input_schema"]; nested {
		t.Fatalf("input_schema should become parameters: %#v", tool)
	}
	params, ok := tool["parameters"].(map[string]any)
	if !ok || params["type"] != "object" {
		t.Fatalf("parameters: %#v", tool["parameters"])
	}
	if m["tool_choice"] != "auto" {
		t.Fatalf("tool_choice: %#v", m["tool_choice"])
	}
	if m["max_output_tokens"] != float64(1024) {
		t.Fatalf("max_output_tokens: %#v", m["max_output_tokens"])
	}
	reasoning, _ := m["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "medium" {
		t.Fatalf("reasoning.effort from thinking.budget_tokens: %#v", m["reasoning"])
	}
}

func TestClaudeMessagesToXAI_arraySystemAndToolUseRoundTrip(t *testing.T) {
	in := mustJSON(t, map[string]any{
		"model": "claude-sonnet-4",
		"system": []any{
			map[string]any{"type": "text", "text": "Rule A"},
			map[string]any{"type": "text", "text": "Rule B"},
		},
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "call tool"},
				},
			},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "get_weather",
						"input": map[string]any{"city": "Paris"},
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_1",
						"content":     "sunny, 22C",
					},
				},
			},
		},
		"tools": []any{
			map[string]any{
				"name":         "get_weather",
				"description":  "weather",
				"input_schema": map[string]any{"type": "object"},
			},
		},
		"tool_choice": map[string]any{"type": "any"},
	})

	out, err := translate.ClaudeMessagesToXAI(in, "grok-4.5")
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := asMap(t, out)

	instr, _ := m["instructions"].(string)
	if !strings.Contains(instr, "Rule A") || !strings.Contains(instr, "Rule B") {
		t.Fatalf("array system → instructions: %q", instr)
	}

	input := m["input"].([]any)
	// user text, function_call, function_call_output
	if len(input) < 3 {
		t.Fatalf("expected >=3 input items, got %#v", input)
	}
	fc := input[1].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "toolu_1" || fc["name"] != "get_weather" {
		t.Fatalf("function_call: %#v", fc)
	}
	args, _ := fc["arguments"].(string)
	if !strings.Contains(args, "Paris") {
		t.Fatalf("arguments: %q", args)
	}
	fco := input[2].(map[string]any)
	if fco["type"] != "function_call_output" || fco["call_id"] != "toolu_1" {
		t.Fatalf("function_call_output: %#v", fco)
	}
	if fco["output"] != "sunny, 22C" {
		t.Fatalf("output: %#v", fco["output"])
	}
	if m["tool_choice"] != "required" {
		t.Fatalf("any → required, got %#v", m["tool_choice"])
	}
}

func TestXAIResponseToClaudeMessage_textAndUsage(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_123",
			"model":  "grok-4.5",
			"status": "completed",
			"usage": map[string]any{
				"input_tokens":  float64(10),
				"output_tokens": float64(5),
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

	out, err := translate.XAIResponseToClaudeMessage(body)
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := asMap(t, out)
	if m["type"] != "message" {
		t.Fatalf("type: %v", m["type"])
	}
	if m["role"] != "assistant" {
		t.Fatalf("role: %v", m["role"])
	}
	if m["id"] != "resp_123" {
		t.Fatalf("id: %v", m["id"])
	}
	if m["model"] != "grok-4.5" {
		t.Fatalf("model: %v", m["model"])
	}
	if m["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason: %v", m["stop_reason"])
	}
	content := m["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content: %#v", content)
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "Hello back" {
		t.Fatalf("block: %#v", block)
	}
	usage := m["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) {
		t.Fatalf("usage: %#v", usage)
	}
}

func TestXAIResponseToClaudeMessage_toolUse(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"id":     "resp_tc",
		"model":  "grok-4.5",
		"status": "completed",
		"output": []any{
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_9",
				"name":      "get_weather",
				"arguments": `{"city":"Paris"}`,
			},
		},
	})

	out, err := translate.XAIResponseToClaudeMessage(body)
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := asMap(t, out)
	if m["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason: %v", m["stop_reason"])
	}
	block := m["content"].([]any)[0].(map[string]any)
	if block["type"] != "tool_use" {
		t.Fatalf("type: %v", block["type"])
	}
	if block["id"] != "call_9" || block["name"] != "get_weather" {
		t.Fatalf("tool_use: %#v", block)
	}
	input, ok := block["input"].(map[string]any)
	if !ok || input["city"] != "Paris" {
		t.Fatalf("input: %#v", block["input"])
	}
}

func TestXAIEventToClaudeSSE_streamLifecycle(t *testing.T) {
	// response.created → message_start
	created := mustJSON(t, map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":    "resp_s",
			"model": "grok-4.5",
		},
	})
	frames, err := translate.XAIEventToClaudeSSE("response.created", created)
	if err != nil {
		t.Fatalf("created: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("created frames: %d", len(frames))
	}
	assertClaudeSSE(t, frames[0], "message_start", func(m map[string]any) {
		if m["type"] != "message_start" {
			t.Fatalf("type: %v", m["type"])
		}
		msg := m["message"].(map[string]any)
		if msg["id"] != "resp_s" || msg["role"] != "assistant" {
			t.Fatalf("message: %#v", msg)
		}
	})

	// output_text.delta → content_block_delta
	delta := mustJSON(t, map[string]any{
		"type":  "response.output_text.delta",
		"delta": "Hi",
	})
	frames, err = translate.XAIEventToClaudeSSE("response.output_text.delta", delta)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("expected content_block_delta frame(s)")
	}
	// Last (or only) frame should be text delta.
	foundDelta := false
	for _, f := range frames {
		if !bytes.Contains(f, []byte("content_block_delta")) {
			continue
		}
		assertClaudeSSE(t, f, "content_block_delta", func(m map[string]any) {
			d := m["delta"].(map[string]any)
			if d["type"] != "text_delta" || d["text"] != "Hi" {
				t.Fatalf("delta: %#v", d)
			}
			foundDelta = true
		})
	}
	if !foundDelta {
		t.Fatalf("no text_delta among frames: %q", frames)
	}

	// completed → message_delta + message_stop
	completed := mustJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_s",
			"model":  "grok-4.5",
			"status": "completed",
			"usage": map[string]any{
				"input_tokens":  float64(3),
				"output_tokens": float64(2),
			},
			"output": []any{},
		},
	})
	frames, err = translate.XAIEventToClaudeSSE("response.completed", completed)
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if len(frames) < 2 {
		t.Fatalf("want message_delta + message_stop, got %d frames: %q", len(frames), frames)
	}
	assertClaudeSSE(t, frames[0], "message_delta", func(m map[string]any) {
		d := m["delta"].(map[string]any)
		if d["stop_reason"] != "end_turn" {
			t.Fatalf("stop_reason: %#v", d)
		}
		usage := m["usage"].(map[string]any)
		if usage["input_tokens"] != float64(3) {
			t.Fatalf("usage: %#v", usage)
		}
	})
	assertClaudeSSE(t, frames[len(frames)-1], "message_stop", func(m map[string]any) {
		if m["type"] != "message_stop" {
			t.Fatalf("type: %v", m["type"])
		}
	})

	// skippable event → zero frames
	frames, err = translate.XAIEventToClaudeSSE("response.in_progress", mustJSON(t, map[string]any{
		"type": "response.in_progress",
	}))
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("expected zero frames, got %q", frames)
	}
}

func assertClaudeSSE(t *testing.T, frame []byte, wantEvent string, check func(map[string]any)) {
	t.Helper()
	s := string(frame)
	if !strings.Contains(s, "event: "+wantEvent) {
		t.Fatalf("want event %q in %q", wantEvent, s)
	}
	// Extract data line.
	var data []byte
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			data = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			break
		}
	}
	if len(data) == 0 {
		t.Fatalf("no data line in %q", frame)
	}
	m := asMap(t, data)
	check(m)
}
