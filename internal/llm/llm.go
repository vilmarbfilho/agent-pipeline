// Package llm define uma interface neutra de provedor para modelos com tool
// calling, e as implementações (Anthropic e um mock roteirizado para testes).
package llm

import (
	"context"
	"encoding/json"
)

// Role de uma mensagem na conversa.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ToolSpec descreve uma ferramenta exposta ao modelo.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema do objeto de entrada
}

// ToolCall é um pedido de execução de ferramenta feito pelo modelo.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult é a resposta a um ToolCall.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Message é uma mensagem neutra. Mensagens do assistente carregam em Native a
// representação do próprio provedor, para que blocos que não modelamos aqui
// (ex.: thinking) sejam devolvidos à API sem alteração.
type Message struct {
	Role        Role
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	Native      any
}

// UserText cria uma mensagem de usuário com texto.
func UserText(s string) Message { return Message{Role: RoleUser, Text: s} }

// UserToolResults cria a mensagem de usuário que devolve resultados de ferramentas.
// Todos os resultados de um turno vão em uma única mensagem.
func UserToolResults(rs []ToolResult) Message { return Message{Role: RoleUser, ToolResults: rs} }

// StopReason normalizado entre provedores.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopOther     StopReason = "other"
)

// Usage de tokens de uma chamada.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

// Request para o modelo.
type Request struct {
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int64
}

// Response do modelo.
type Response struct {
	Message    Message // mensagem do assistente pronta para ir ao histórico
	StopReason StopReason
	// RefusalCategory é preenchido quando StopReason == StopRefusal (pode ser vazio).
	RefusalCategory string
	Model           string
	Usage           Usage
}

// StreamKind identifica o tipo de evento de streaming.
type StreamKind string

const (
	StreamText      StreamKind = "text"
	StreamThinking  StreamKind = "thinking"
	StreamToolStart StreamKind = "tool_start"
)

// StreamEvent é emitido incrementalmente durante a geração.
type StreamEvent struct {
	Kind     StreamKind `json:"kind"`
	Text     string     `json:"text,omitempty"`
	ToolName string     `json:"tool_name,omitempty"`
}

// StreamFunc recebe eventos de streaming. Pode ser nil.
type StreamFunc func(StreamEvent)

// Client é a abstração de provedor usada pelo orquestrador.
type Client interface {
	Complete(ctx context.Context, req Request, onStream StreamFunc) (*Response, error)
}
