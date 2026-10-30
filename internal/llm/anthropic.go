package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// AnthropicConfig configura o cliente Claude.
type AnthropicConfig struct {
	APIKey string // vazio = resolução padrão do SDK (ANTHROPIC_API_KEY, perfil `ant auth login`, ...)
	Model  string // ex.: "claude-opus-5-5"
	// Effort: low | medium | high | xhigh | max. Vazio = padrão do modelo.
	Effort string
	// Fallbacks habilita o fallback server-side em caso de recusa dos
	// classificadores de segurança (fallbacks: "default").
	Fallbacks bool
	BaseURL   string
}

// Anthropic implementa Client usando a Messages API com streaming.
type Anthropic struct {
	client anthropic.Client
	cfg    AnthropicConfig
}

// NewAnthropic cria o cliente.
func NewAnthropic(cfg AnthropicConfig) *Anthropic {
	opts := []option.RequestOption{option.WithMaxRetries(4)}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.Model == "" {
		cfg.Model = "claude-opus-5-5"
	}
	return &Anthropic{client: anthropic.NewClient(opts...), cfg: cfg}
}

func (a *Anthropic) Complete(ctx context.Context, req Request, onStream StreamFunc) (*Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 64000
	}
	thinking := anthropic.ThinkingConfigAdaptiveParam{Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized}
	params := anthropic.MessageNewParams{
		Model:     a.cfg.Model,
		MaxTokens: maxTokens,
		Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &thinking},
		Tools:     toAnthropicTools(req.Tools),
		Messages:  toAnthropicMessages(req.Messages),
		// Cache automático no último bloco cacheável: o loop ReAct reenvia
		// todo o histórico a cada iteração.
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if a.cfg.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(a.cfg.Effort)}
	}

	var opts []option.RequestOption
	if a.cfg.Fallbacks {
		opts = append(opts,
			option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
			option.WithJSONSet("fallbacks", "default"),
		)
	}

	stream := a.client.Messages.NewStreaming(ctx, params, opts...)
	defer stream.Close()
	msg := anthropic.Message{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, fmt.Errorf("acumulando stream: %w", err)
		}
		if onStream == nil {
			continue
		}
		switch e := ev.AsAny().(type) {
		case anthropic.ContentBlockStartEvent:
			if e.ContentBlock.Type == "tool_use" {
				onStream(StreamEvent{Kind: StreamToolStart, ToolName: e.ContentBlock.Name})
			}
		case anthropic.ContentBlockDeltaEvent:
			switch d := e.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				onStream(StreamEvent{Kind: StreamText, Text: d.Text})
			case anthropic.ThinkingDelta:
				onStream(StreamEvent{Kind: StreamThinking, Text: d.Thinking})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	return fromAnthropicMessage(msg), nil
}

func toAnthropicTools(specs []ToolSpec) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(specs))
	for _, s := range specs {
		schema := anthropic.ToolInputSchemaParam{ExtraFields: map[string]any{}}
		for k, v := range s.Schema {
			switch k {
			case "type":
			case "properties":
				schema.Properties = v
			case "required":
				if req, ok := v.([]string); ok {
					schema.Required = req
				}
			default:
				schema.ExtraFields[k] = v
			}
		}
		t := anthropic.ToolParam{
			Name:        s.Name,
			Description: anthropic.String(s.Description),
			InputSchema: schema,
			// Conteúdo de arquivos chega em streaming sem buffering; a
			// validação da entrada fica a cargo do orquestrador.
			EagerInputStreaming: anthropic.Bool(true),
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &t})
	}
	return out
}

func toAnthropicMessages(msgs []Message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		if native, ok := m.Native.(anthropic.MessageParam); ok {
			out = append(out, native)
			continue
		}
		var blocks []anthropic.ContentBlockParamUnion
		for _, r := range m.ToolResults {
			blocks = append(blocks, anthropic.NewToolResultBlock(r.CallID, r.Content, r.IsError))
		}
		if m.Text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}
		for _, c := range m.ToolCalls {
			blocks = append(blocks, anthropic.NewToolUseBlock(c.ID, c.Input, c.Name))
		}
		if m.Role == RoleAssistant {
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		} else {
			out = append(out, anthropic.NewUserMessage(blocks...))
		}
	}
	return out
}

func fromAnthropicMessage(msg anthropic.Message) *Response {
	msg.Content = dropBeforeFallback(msg.Content)
	resp := &Response{
		Model: string(msg.Model),
		Usage: Usage{
			InputTokens:     msg.Usage.InputTokens,
			OutputTokens:    msg.Usage.OutputTokens,
			CacheReadTokens: msg.Usage.CacheReadInputTokens,
		},
	}
	out := Message{Role: RoleAssistant}
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			out.Text += b.Text
		case anthropic.ToolUseBlock:
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID: b.ID, Name: b.Name, Input: json.RawMessage(b.JSON.Input.Raw()),
			})
		}
	}
	// Histórico append-only: a mensagem nativa (com blocos de thinking) volta
	// à API exatamente como recebida.
	out.Native = msg.ToParam()
	resp.Message = out

	switch msg.StopReason {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		resp.StopReason = StopEndTurn
	case anthropic.StopReasonToolUse:
		resp.StopReason = StopToolUse
	case anthropic.StopReasonMaxTokens:
		resp.StopReason = StopMaxTokens
	case anthropic.StopReasonRefusal:
		resp.StopReason = StopRefusal
		resp.RefusalCategory = string(msg.StopDetails.Category)
	default:
		resp.StopReason = StopOther
	}
	return resp
}

// dropBeforeFallback aplica a regra de eco após um fallback no meio da
// geração: blocos thinking/tool_use anteriores ao último bloco "fallback"
// não devem voltar à API. O próprio marcador também é removido.
func dropBeforeFallback(blocks []anthropic.ContentBlockUnion) []anthropic.ContentBlockUnion {
	last := -1
	for i, b := range blocks {
		if b.Type == "fallback" {
			last = i
		}
	}
	if last < 0 {
		return blocks
	}
	out := make([]anthropic.ContentBlockUnion, 0, len(blocks))
	for i, b := range blocks {
		if (i < last && b.Type == "text") || i > last {
			out = append(out, b)
		}
	}
	return out
}
