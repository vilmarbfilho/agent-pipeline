package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func orServer(t *testing.T, chunks []string, captured *map[string]any, header *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, captured); err != nil {
			t.Errorf("corpo inválido: %v", err)
		}
		*header = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestOpenRouterStreamingToolUse(t *testing.T) {
	chunks := []string{
		`{"model":"anthropic/claude-sonnet-4.5","choices":[{"delta":{"role":"assistant","reasoning":"Preciso ","reasoning_details":[{"type":"reasoning.text","index":0,"text":"Preciso "}]}}]}`,
		`{"choices":[{"delta":{"reasoning":"ler.","reasoning_details":[{"type":"reasoning.text","index":0,"text":"ler.","signature":"sig"}]}}]}`,
		`{"choices":[{"delta":{"content":"Vou ler "}}]}`,
		`{"choices":[{"delta":{"content":"main.go."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}},{"index":1,"id":"call_b","function":{"name":"git_diff","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":80}}}`,
	}
	var body map[string]any
	var hdr http.Header
	srv := orServer(t, chunks, &body, &hdr)
	defer srv.Close()

	c, err := NewOpenRouter(OpenRouterConfig{APIKey: "or-key", BaseURL: srv.URL, Model: "anthropic/claude-sonnet-4.5", Effort: "xhigh", AppName: "orchestrator"})
	if err != nil {
		t.Fatal(err)
	}
	var text, thinking strings.Builder
	var starts []string
	resp, err := c.Complete(context.Background(), Request{
		System:    "sys",
		Messages:  []Message{UserText("leia main.go")},
		Tools:     []ToolSpec{{Name: "read_file", Description: "lê", Schema: map[string]any{"type": "object"}}},
		MaxTokens: 1000,
	}, func(e StreamEvent) {
		switch e.Kind {
		case StreamText:
			text.WriteString(e.Text)
		case StreamThinking:
			thinking.WriteString(e.Text)
		case StreamToolStart:
			starts = append(starts, e.ToolName)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.StopReason != StopToolUse || resp.Message.Text != "Vou ler main.go." || len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("resposta: %+v", resp)
	}
	if tc := resp.Message.ToolCalls[0]; tc.ID != "call_a" || tc.Name != "read_file" || string(tc.Input) != `{"path":"main.go"}` {
		t.Fatalf("tool call 0: %+v (%s)", tc, tc.Input)
	}
	if tc := resp.Message.ToolCalls[1]; tc.Name != "git_diff" || string(tc.Input) != `{}` {
		t.Fatalf("tool call 1: %+v (%s)", tc, tc.Input)
	}
	if text.String() != "Vou ler main.go." || thinking.String() != "Preciso ler." || strings.Join(starts, ",") != "read_file,git_diff" {
		t.Fatalf("stream: %q %q %v", text.String(), thinking.String(), starts)
	}
	if resp.Usage != (Usage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 80}) || resp.Model != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("usage/model: %+v %s", resp.Usage, resp.Model)
	}

	// Requisição
	if hdr.Get("Authorization") != "Bearer or-key" || hdr.Get("X-Title") != "orchestrator" {
		t.Fatalf("headers: %v", hdr)
	}
	if body["stream"] != true || body["model"] != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("corpo: %v", body)
	}
	if body["reasoning"].(map[string]any)["effort"] != "high" {
		t.Fatalf("reasoning: %v", body["reasoning"])
	}
	msgs := body["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("system ausente: %v", msgs)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["type"] != "function" || tool["function"].(map[string]any)["name"] != "read_file" {
		t.Fatalf("tool: %v", tool)
	}

	// Histórico: assistant com tool_calls + reasoning_details, depois role=tool.
	hist := toORMessages("", []Message{UserText("x"), resp.Message, UserToolResults([]ToolResult{
		{CallID: "call_a", Content: "ok"}, {CallID: "call_b", Content: "diff", IsError: true},
	})})
	raw, _ := json.Marshal(hist)
	for _, want := range []string{
		`"role":"assistant"`, `"id":"call_a"`, `"arguments":"{\"path\":\"main.go\"}"`,
		`"reasoning_details":[{"index":0,"signature":"sig","text":"Preciso ler.","type":"reasoning.text"}]`,
		`{"role":"tool","content":"ok","tool_call_id":"call_a"}`, `"tool_call_id":"call_b"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("histórico sem %s:\n%s", want, raw)
		}
	}
}

func TestOpenRouterStopReasonsAndErrors(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		stop   StopReason
		err    string
	}{
		{"texto", []string{`{"choices":[{"delta":{"content":"fim"},"finish_reason":"stop"}]}`}, StopEndTurn, ""},
		{"length", []string{`{"choices":[{"delta":{"content":"x"},"finish_reason":"length"}]}`}, StopMaxTokens, ""},
		{"filtro", []string{`{"choices":[{"delta":{},"finish_reason":"content_filter"}]}`}, StopRefusal, ""},
		{"tool com stop", []string{`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"git_diff","arguments":"{}"}}]},"finish_reason":"stop"}]}`}, StopToolUse, ""},
		{"erro no stream", []string{`{"error":{"code":502,"message":"provider down"},"choices":[{"delta":{},"finish_reason":"error"}]}`}, "", "provider down"},
		{"args inválidos", []string{`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"write_file","arguments":"{\"path\":"}}]},"finish_reason":"tool_calls"}]}`}, StopToolUse, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body map[string]any
			var hdr http.Header
			srv := orServer(t, c.chunks, &body, &hdr)
			defer srv.Close()
			cl, _ := NewOpenRouter(OpenRouterConfig{APIKey: "k", BaseURL: srv.URL, Model: "m"})
			resp, err := cl.Complete(context.Background(), Request{Messages: []Message{UserText("x")}}, nil)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("esperava erro %q: %v", c.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if resp.StopReason != c.stop {
				t.Fatalf("stop = %s, quer %s", resp.StopReason, c.stop)
			}
			for _, tc := range resp.Message.ToolCalls {
				if tc.ID == "" || !json.Valid(tc.Input) {
					t.Fatalf("tool call deve ter ID e Input serializável: %+v", tc)
				}
			}
			if _, ok := body["reasoning"]; ok {
				t.Fatal("reasoning não deveria ser enviado sem effort")
			}
		})
	}
}

func TestOpenRouterHTTPErrorsAndRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
		default:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer srv.Close()
	cl, _ := NewOpenRouter(OpenRouterConfig{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := cl.Complete(context.Background(), Request{Messages: []Message{UserText("x")}}, nil)
	if err != nil || resp.Message.Text != "ok" || calls.Load() != 2 {
		t.Fatalf("retry após 429: %+v %v (chamadas=%d)", resp, err, calls.Load())
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid model"}}`, http.StatusBadRequest)
	}))
	defer bad.Close()
	cl, _ = NewOpenRouter(OpenRouterConfig{APIKey: "k", BaseURL: bad.URL, Model: "m"})
	if _, err := cl.Complete(context.Background(), Request{}, nil); err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("400 não deve ter retry e deve retornar erro: %v", err)
	}

	if _, err := NewOpenRouter(OpenRouterConfig{Model: "m"}); err == nil {
		t.Fatal("sem API key deveria falhar")
	}
	if _, err := NewOpenRouter(OpenRouterConfig{APIKey: "k"}); err == nil {
		t.Fatal("sem modelo deveria falhar")
	}
}
