package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Tool é uma ferramenta invocável pelo LLM.
type Tool interface {
	Name() string
	Description() string
	// Schema retorna o JSON Schema (objeto) dos parâmetros de entrada.
	Schema() map[string]any
	// Run executa a ferramenta. Erros retornados viram tool_result com is_error.
	Run(ctx context.Context, input json.RawMessage) (string, error)
}

// Env reúne as dependências compartilhadas pelas ferramentas.
type Env struct {
	Sandbox        *Sandbox
	Approver       Approver
	Policy         Policy
	CommandTimeout time.Duration
	MaxOutputBytes int
}

func (e *Env) maxOut() int {
	if e.MaxOutputBytes <= 0 {
		return 64 * 1024
	}
	return e.MaxOutputBytes
}

func (e *Env) approve(ctx context.Context, req ApprovalRequest) error {
	if e.Approver == nil {
		return fmt.Errorf("ação requer confirmação (%s), mas nenhum aprovador está configurado", req.Reason)
	}
	ok, err := e.Approver.Approve(ctx, req)
	if err != nil {
		return fmt.Errorf("falha ao obter confirmação: %w", err)
	}
	if !ok {
		return fmt.Errorf("ação negada pelo usuário: %s", req.Action)
	}
	return nil
}

// Registry mapeia nomes para ferramentas.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry cria um registro com as ferramentas informadas.
func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range ts {
		r.tools[t.Name()] = t
	}
	return r
}

// Default retorna o registro com todas as ferramentas padrão.
func Default(env *Env) *Registry {
	return NewRegistry(
		&ReadFile{env},
		&WriteFile{env},
		&ListFiles{env},
		&RunCommand{env},
		&GitDiff{env},
	)
}

// Get busca uma ferramenta pelo nome.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Subset retorna um novo registro apenas com os nomes informados.
func (r *Registry) Subset(names ...string) *Registry {
	out := &Registry{tools: map[string]Tool{}}
	for _, n := range names {
		if t, ok := r.tools[n]; ok {
			out.tools[n] = t
		}
	}
	return out
}

// Add registra (ou substitui) uma ferramenta.
func (r *Registry) Add(t Tool) { r.tools[t.Name()] = t }

// List retorna as ferramentas em ordem determinística (estável para cache de prompt).
func (r *Registry) List() []Tool {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// decode faz unmarshal estrito da entrada da ferramenta.
func decode(input json.RawMessage, v any) error {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if err := json.Unmarshal(input, v); err != nil {
		return fmt.Errorf("entrada JSON inválida: %w", err)
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n...[truncado: %d bytes omitidos]", len(s)-max)
}
