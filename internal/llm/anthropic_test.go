package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseServer simula a Messages API em streaming e captura a requisição.
func sseServer(t *testing.T, events []string, captured *map[string]any, header *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, captured); err != nil {
			t.Errorf("corpo inválido: %v", err)
		}
		*header = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			var m map[string]any
			_ = json.Unmarshal([]byte(e), &m)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m["type"], e)
		}
	}))
}

func TestAnthropicStreamingToolUse(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Preciso ler o arquivo."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Vou ler "}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"main.go."}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"main.go\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`,
		`{"type":"message_stop"}`,
	}
	var body map[string]any
	var hdr http.Header
	srv := sseServer(t, events, &body, &hdr)
	defer srv.Close()

	c := NewAnthropic(AnthropicConfig{APIKey: "test", BaseURL: srv.URL, Model: "claude-opus-5-5", Effort: "high", Fallbacks: true})
	var text, thinking strings.Builder
	var toolStarts []string
	resp, err := c.Complete(context.Background(), Request{
		System:   "sys",
		Messages: []Message{UserText("leia main.go")},
		Tools:    []ToolSpec{{Name: "read_file", Description: "lê", Schema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}},
	}, func(e StreamEvent) {
		switch e.Kind {
		case StreamText:
			text.WriteString(e.Text)
		case StreamThinking:
			thinking.WriteString(e.Text)
		case StreamToolStart:
			toolStarts = append(toolStarts, e.ToolName)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	// Resposta normalizada
	if resp.StopReason != StopToolUse || resp.Message.Text != "Vou ler main.go." || len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("resposta: %+v", resp)
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "read_file" || string(tc.Input) != `{"path":"main.go"}` {
		t.Fatalf("tool call: %+v (%s)", tc, tc.Input)
	}
	if text.String() != "Vou ler main.go." || thinking.String() != "Preciso ler o arquivo." || len(toolStarts) != 1 {
		t.Fatalf("stream: text=%q thinking=%q tools=%v", text.String(), thinking.String(), toolStarts)
	}
	if resp.Usage.OutputTokens != 42 {
		t.Fatalf("usage: %+v", resp.Usage)
	}

	// Requisição enviada
	if body["model"] != "claude-opus-5-5" || body["stream"] != true || body["fallbacks"] != "default" {
		t.Fatalf("corpo: %v", body)
	}
	if th := body["thinking"].(map[string]any); th["type"] != "adaptive" || th["display"] != "summarized" {
		t.Fatalf("thinking: %v", th)
	}
	if oc := body["output_config"].(map[string]any); oc["effort"] != "high" {
		t.Fatalf("output_config: %v", oc)
	}
	if _, ok := body["cache_control"]; !ok {
		t.Fatal("cache_control ausente")
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["eager_input_streaming"] != true || tool["input_schema"].(map[string]any)["required"] == nil {
		t.Fatalf("tool: %v", tool)
	}
	if !strings.Contains(hdr.Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Fatalf("beta header: %v", hdr)
	}

	// O histórico preserva o bloco de thinking para a próxima chamada.
	next := toAnthropicMessages([]Message{UserText("x"), resp.Message, UserToolResults([]ToolResult{{CallID: "toolu_1", Content: "ok"}})})
	raw, _ := json.Marshal(next)
	for _, want := range []string{`"type":"thinking"`, `"signature":"sig"`, `"type":"tool_use"`, `"tool_use_id":"toolu_1"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("histórico sem %s: %s", want, raw)
		}
	}
}

func TestAnthropicRefusal(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":null}},"usage":{"output_tokens":0}}`,
		`{"type":"message_stop"}`,
	}
	var body map[string]any
	var hdr http.Header
	srv := sseServer(t, events, &body, &hdr)
	defer srv.Close()
	c := NewAnthropic(AnthropicConfig{APIKey: "test", BaseURL: srv.URL})
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{UserText("x")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopRefusal {
		t.Fatalf("stop: %+v", resp)
	}
	if _, ok := body["fallbacks"]; ok {
		t.Fatal("fallbacks não deveria ser enviado quando desabilitado")
	}
}
