// Package orchestrator implementa o loop ReAct e o pipeline multi-agente
// (Planner -> Coder -> Reviewer).
package orchestrator

import (
	"encoding/json"
	"sync"
	"time"

	"orchestrator/internal/llm"
)

// EventType identifica eventos emitidos durante uma execução. Os mesmos
// eventos alimentam a CLI e a camada JSON-RPC (UI desktop).
type EventType string

const (
	EventRunStarted     EventType = "run_started"
	EventPhaseStarted   EventType = "phase_started"
	EventPhaseCompleted EventType = "phase_completed"
	EventLLMText        EventType = "llm_text"     // delta de texto (streaming)
	EventLLMThinking    EventType = "llm_thinking" // delta do resumo de raciocínio
	EventLLMTurnEnd     EventType = "llm_turn_end" // fim de uma chamada ao modelo
	EventToolCall       EventType = "tool_call"
	EventToolResult     EventType = "tool_result"
	EventRunCompleted   EventType = "run_completed"
	EventError          EventType = "error"
)

// Event é a unidade de saída estruturada.
type Event struct {
	Type    EventType       `json:"type"`
	Time    time.Time       `json:"time"`
	Phase   string          `json:"phase,omitempty"`
	Step    int             `json:"step,omitempty"`
	Text    string          `json:"text,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Output  string          `json:"output,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
	Usage   *llm.Usage      `json:"usage,omitempty"`
	Data    any             `json:"data,omitempty"`
}

// Sink recebe eventos. Implementações devem ser seguras para uso concorrente.
type Sink interface {
	Emit(Event)
}

// SinkFunc adapta uma função para Sink.
type SinkFunc func(Event)

func (f SinkFunc) Emit(e Event) { f(e) }

// Recorder guarda eventos em memória (útil para testes).
type Recorder struct {
	mu     sync.Mutex
	Events []Event
}

func (r *Recorder) Emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, e)
}

// OfType retorna os eventos de um tipo.
func (r *Recorder) OfType(t EventType) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, e := range r.Events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func emit(s Sink, e Event) {
	if s == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	s.Emit(e)
}
