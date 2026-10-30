package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpenRouterConfig configura o cliente OpenRouter (API compatível com
// OpenAI Chat Completions).
type OpenRouterConfig struct {
	APIKey  string // OPENROUTER_API_KEY
	Model   string // ex.: "anthropic/claude-sonnet-4.5", "openai/gpt-5", "google/gemini-2.5-pro"
	BaseURL string // padrão https://openrouter.ai/api/v1
	// Effort: low | medium | high (xhigh/max viram high). Vazio = não envia.
	Effort string
	// Atribuição opcional exibida no painel do OpenRouter.
	SiteURL string
	AppName string
	HTTP    *http.Client
}

// OpenRouter implementa Client via /chat/completions com streaming SSE.
type OpenRouter struct {
	cfg  OpenRouterConfig
	http *http.Client
}

// NewOpenRouter cria o cliente.
func NewOpenRouter(cfg OpenRouterConfig) (*OpenRouter, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("OPENROUTER_API_KEY não definida")
	}
	if cfg.Model == "" {
		return nil, errors.New("modelo obrigatório para o OpenRouter (ex.: ORCH_MODEL=anthropic/claude-sonnet-4.5; veja https://openrouter.ai/models)")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://openrouter.ai/api/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	h := cfg.HTTP
	if h == nil {
		// Sem timeout global: respostas longas chegam em streaming.
		h = &http.Client{}
	}
	return &OpenRouter{cfg: cfg, http: h}, nil
}

// ------------------------------------------------------------- wire types

type orMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []orToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	// Raciocínio estruturado devolvido sem alteração para modelos que exigem
	// continuidade do raciocínio entre tool calls (ex.: Claude, Gemini, o-series).
	ReasoningDetails []map[string]any `json:"reasoning_details,omitempty"`
}

type orToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type orTool struct {
	Type     string         `json:"type"`
	Function orToolFunction `json:"function"`
}

type orToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type orRequest struct {
	Model     string         `json:"model"`
	Messages  []orMessage    `json:"messages"`
	Tools     []orTool       `json:"tools,omitempty"`
	Stream    bool           `json:"stream"`
	MaxTokens int64          `json:"max_tokens,omitempty"`
	Reasoning map[string]any `json:"reasoning,omitempty"`
	Usage     map[string]any `json:"usage,omitempty"`
	// Roteia só para provedores que suportam todos os parâmetros (tools).
	Provider map[string]any `json:"provider,omitempty"`
}

type orChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string           `json:"content"`
			Reasoning        string           `json:"reasoning"`
			ReasoningDetails []map[string]any `json:"reasoning_details"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *orError `json:"error"`
}

type orError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

func (e *orError) Error() string { return fmt.Sprintf("openrouter: %v: %s", e.Code, e.Message) }

// --------------------------------------------------------------- Complete

func (o *OpenRouter) Complete(ctx context.Context, req Request, onStream StreamFunc) (*Response, error) {
	body := orRequest{
		Model:     o.cfg.Model,
		Messages:  toORMessages(req.System, req.Messages),
		Tools:     toORTools(req.Tools),
		Stream:    true,
		MaxTokens: req.MaxTokens,
		Usage:     map[string]any{"include": true},
		Provider:  map[string]any{"require_parameters": true},
	}
	if e := orEffort(o.cfg.Effort); e != "" {
		body.Reasoning = map[string]any{"effort": e}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	resp, err := o.post(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return o.readStream(resp.Body, onStream)
}

// post envia a requisição, com retry para 408/429/5xx e erros de rede
// (apenas antes de o stream começar).
func (o *OpenRouter) post(ctx context.Context, payload []byte) (*http.Response, error) {
	const attempts = 4
	var lastErr error
	for i := range attempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<(i-1)) * time.Second):
			}
		}
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		hreq.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("Accept", "text/event-stream")
		if o.cfg.SiteURL != "" {
			hreq.Header.Set("HTTP-Referer", o.cfg.SiteURL)
		}
		if o.cfg.AppName != "" {
			hreq.Header.Set("X-Title", o.cfg.AppName)
		}
		resp, err := o.http.Do(hreq)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		lastErr = fmt.Errorf("openrouter: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
		if resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

type toolAcc struct {
	id, name string
	args     strings.Builder
}

func (o *OpenRouter) readStream(r io.Reader, onStream StreamFunc) (*Response, error) {
	var (
		text      strings.Builder
		calls     = map[int]*toolAcc{}
		reasoning []map[string]any
		finish    string
		res       = &Response{Model: o.cfg.Model}
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Linhas vazias separam eventos; ":" são comentários de keep-alive
		// (": OPENROUTER PROCESSING").
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch orChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return nil, fmt.Errorf("openrouter: chunk inválido: %w", err)
		}
		if ch.Error != nil {
			return nil, ch.Error
		}
		if ch.Model != "" {
			res.Model = ch.Model
		}
		if u := ch.Usage; u != nil {
			res.Usage.InputTokens = u.PromptTokens
			res.Usage.OutputTokens = u.CompletionTokens
			if u.PromptTokensDetails != nil {
				res.Usage.CacheReadTokens = u.PromptTokensDetails.CachedTokens
			}
		}
		for _, c := range ch.Choices {
			d := c.Delta
			if d.Reasoning != "" && onStream != nil {
				onStream(StreamEvent{Kind: StreamThinking, Text: d.Reasoning})
			}
			reasoning = mergeReasoning(reasoning, d.ReasoningDetails)
			if d.Content != "" {
				text.WriteString(d.Content)
				if onStream != nil {
					onStream(StreamEvent{Kind: StreamText, Text: d.Content})
				}
			}
			for _, tc := range d.ToolCalls {
				acc, ok := calls[tc.Index]
				if !ok {
					acc = &toolAcc{}
					calls[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					first := acc.name == ""
					acc.name += tc.Function.Name
					if first && onStream != nil {
						onStream(StreamEvent{Kind: StreamToolStart, ToolName: acc.name})
					}
				}
				acc.args.WriteString(tc.Function.Arguments)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("openrouter: lendo stream: %w", err)
	}
	if finish == "error" {
		return nil, errors.New("openrouter: o provedor encerrou a geração com erro")
	}

	msg := Message{Role: RoleAssistant, Text: text.String()}
	native := orMessage{Role: "assistant", ReasoningDetails: reasoning}
	if msg.Text != "" {
		t := msg.Text
		native.Content = &t
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		acc := calls[i]
		if acc.id == "" {
			acc.id = "call_" + strconv.Itoa(i)
		}
		args := acc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		input := json.RawMessage(args)
		if !json.Valid(input) {
			// Mantém o evento serializável; a ferramenta rejeita a entrada.
			input = json.RawMessage(strconv.Quote(args))
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Input: input})
		var wc orToolCall
		wc.ID, wc.Type = acc.id, "function"
		wc.Function.Name, wc.Function.Arguments = acc.name, args
		native.ToolCalls = append(native.ToolCalls, wc)
	}
	msg.Native = native
	res.Message = msg

	switch {
	case finish == "length":
		res.StopReason = StopMaxTokens
	case finish == "content_filter":
		res.StopReason = StopRefusal
		res.RefusalCategory = "content_filter"
	case len(msg.ToolCalls) > 0:
		// Alguns provedores retornam "stop" mesmo com tool calls.
		res.StopReason = StopToolUse
	case finish == "stop" || finish == "":
		res.StopReason = StopEndTurn
	default:
		res.StopReason = StopOther
	}
	return res, nil
}

// mergeReasoning junta fragmentos de reasoning_details recebidos em streaming:
// fragmentos com o mesmo "index" são concatenados (campos de texto) e os
// demais campos assumem o último valor não vazio.
func mergeReasoning(acc []map[string]any, parts []map[string]any) []map[string]any {
	for _, p := range parts {
		var target map[string]any
		if pi, ok := p["index"]; ok {
			for _, a := range acc {
				if a["index"] == pi && a["type"] == p["type"] {
					target = a
					break
				}
			}
		}
		if target == nil {
			cp := make(map[string]any, len(p))
			for k, v := range p {
				cp[k] = v
			}
			acc = append(acc, cp)
			continue
		}
		for k, v := range p {
			switch k {
			case "text", "summary", "data":
				s, _ := v.(string)
				prev, _ := target[k].(string)
				target[k] = prev + s
			default:
				if v != nil && v != "" {
					target[k] = v
				}
			}
		}
	}
	return acc
}

func toORMessages(system string, msgs []Message) []orMessage {
	var out []orMessage
	str := func(s string) *string { return &s }
	if system != "" {
		out = append(out, orMessage{Role: "system", Content: str(system)})
	}
	for _, m := range msgs {
		if native, ok := m.Native.(orMessage); ok {
			out = append(out, native)
			continue
		}
		if m.Role == RoleAssistant {
			am := orMessage{Role: "assistant"}
			if m.Text != "" {
				am.Content = str(m.Text)
			}
			for _, c := range m.ToolCalls {
				var wc orToolCall
				wc.ID, wc.Type = c.ID, "function"
				wc.Function.Name, wc.Function.Arguments = c.Name, string(c.Input)
				am.ToolCalls = append(am.ToolCalls, wc)
			}
			out = append(out, am)
			continue
		}
		// Resultados de ferramentas viram mensagens role=tool (uma por chamada).
		for _, r := range m.ToolResults {
			out = append(out, orMessage{Role: "tool", ToolCallID: r.CallID, Content: str(r.Content)})
		}
		if m.Text != "" {
			out = append(out, orMessage{Role: "user", Content: str(m.Text)})
		}
	}
	return out
}

func toORTools(specs []ToolSpec) []orTool {
	out := make([]orTool, 0, len(specs))
	for _, s := range specs {
		out = append(out, orTool{Type: "function", Function: orToolFunction{
			Name: s.Name, Description: s.Description, Parameters: s.Schema,
		}})
	}
	return out
}

func orEffort(e string) string {
	switch e {
	case "low", "medium", "high":
		return e
	case "xhigh", "max":
		return "high"
	default:
		return ""
	}
}
