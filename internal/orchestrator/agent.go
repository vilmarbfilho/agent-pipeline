package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orchestrator/internal/llm"
	"orchestrator/internal/tools"
)

// TerminalTool é uma ferramenta que encerra o loop do agente e carrega a
// saída estruturada da fase (plano, resumo, revisão).
type TerminalTool struct {
	Spec llm.ToolSpec
	// Validate decodifica e valida a entrada; erro volta ao modelo como tool_result.
	Validate func(input json.RawMessage) (any, error)
}

// Agent executa um loop ReAct: o modelo raciocina (thinking + texto), age
// (tool calls), observa (tool results) e repete até chamar a ferramenta terminal.
type Agent struct {
	Name      string
	System    string
	LLM       llm.Client
	Tools     *tools.Registry
	Terminal  TerminalTool
	MaxSteps  int
	MaxTokens int64
	Sink      Sink
}

// AgentResult é o resultado de um agente.
type AgentResult struct {
	Output   any // valor retornado por Terminal.Validate
	Steps    int
	Usage    llm.Usage
	Messages []llm.Message
}

// ErrMaxSteps indica que o agente não concluiu dentro do limite de passos.
var ErrMaxSteps = errors.New("limite de passos atingido")

const maxNudges = 2

// Run executa o agente com a mensagem inicial do usuário.
func (a *Agent) Run(ctx context.Context, userPrompt string) (*AgentResult, error) {
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 40
	}
	specs := a.toolSpecs()
	res := &AgentResult{Messages: []llm.Message{llm.UserText(userPrompt)}}
	nudges := 0

	for step := 1; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Steps = step
		resp, err := a.LLM.Complete(ctx, llm.Request{
			System:    a.System,
			Messages:  res.Messages,
			Tools:     specs,
			MaxTokens: a.MaxTokens,
		}, a.streamTo(step))
		if err != nil {
			return res, fmt.Errorf("%s: chamada ao modelo: %w", a.Name, err)
		}
		addUsage(&res.Usage, resp.Usage)
		usage := resp.Usage
		emit(a.Sink, Event{Type: EventLLMTurnEnd, Phase: a.Name, Step: step, Usage: &usage, Text: string(resp.StopReason)})

		if resp.StopReason == llm.StopRefusal {
			return res, fmt.Errorf("%s: o modelo recusou a solicitação (categoria %q)", a.Name, resp.RefusalCategory)
		}
		// Histórico append-only (preserva blocos de thinking e o cache de prompt).
		res.Messages = append(res.Messages, resp.Message)

		calls := resp.Message.ToolCalls
		if len(calls) == 0 {
			if nudges >= maxNudges {
				return res, fmt.Errorf("%s: o modelo encerrou sem chamar %s", a.Name, a.Terminal.Spec.Name)
			}
			nudges++
			msg := fmt.Sprintf("Para concluir esta fase, chame a ferramenta %s com o resultado estruturado.", a.Terminal.Spec.Name)
			if resp.StopReason == llm.StopMaxTokens {
				msg = "Sua resposta foi cortada por limite de tokens. Continue de forma mais concisa; " + msg
			}
			res.Messages = append(res.Messages, llm.UserText(msg))
			continue
		}

		var results []llm.ToolResult
		var final any
		done := false
		for _, call := range calls {
			emit(a.Sink, Event{Type: EventToolCall, Phase: a.Name, Step: step, Tool: call.Name, CallID: call.ID, Input: call.Input})
			var out string
			var runErr error
			switch {
			case resp.StopReason == llm.StopMaxTokens:
				// A entrada pode ter sido truncada no streaming: não executa.
				runErr = errors.New("entrada da ferramenta truncada (limite de tokens); reenvie a chamada com conteúdo menor")
			case call.Name == a.Terminal.Spec.Name:
				final, runErr = a.Terminal.Validate(call.Input)
				if runErr == nil {
					done = true
					out = "ok"
				}
			default:
				out, runErr = a.runTool(ctx, call)
			}
			r := llm.ToolResult{CallID: call.ID, Content: out}
			if runErr != nil {
				r.Content, r.IsError = "erro: "+runErr.Error(), true
			}
			results = append(results, r)
			emit(a.Sink, Event{Type: EventToolResult, Phase: a.Name, Step: step, Tool: call.Name, CallID: call.ID, Output: r.Content, IsError: r.IsError})
		}
		if done {
			res.Output = final
			return res, nil
		}
		res.Messages = append(res.Messages, llm.UserToolResults(results))
	}
	return res, fmt.Errorf("%s: %w (%d)", a.Name, ErrMaxSteps, maxSteps)
}

func (a *Agent) runTool(ctx context.Context, call llm.ToolCall) (string, error) {
	t, ok := a.Tools.Get(call.Name)
	if !ok {
		return "", fmt.Errorf("ferramenta desconhecida ou não permitida nesta fase: %s", call.Name)
	}
	if !json.Valid(call.Input) {
		return "", errors.New("entrada JSON inválida (possivelmente truncada); reenvie a chamada")
	}
	return t.Run(ctx, call.Input)
}

func (a *Agent) toolSpecs() []llm.ToolSpec {
	var specs []llm.ToolSpec
	for _, t := range a.Tools.List() {
		specs = append(specs, llm.ToolSpec{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	return append(specs, a.Terminal.Spec)
}

func (a *Agent) streamTo(step int) llm.StreamFunc {
	return func(ev llm.StreamEvent) {
		switch ev.Kind {
		case llm.StreamText:
			emit(a.Sink, Event{Type: EventLLMText, Phase: a.Name, Step: step, Text: ev.Text})
		case llm.StreamThinking:
			emit(a.Sink, Event{Type: EventLLMThinking, Phase: a.Name, Step: step, Text: ev.Text})
		}
	}
}

func addUsage(dst *llm.Usage, u llm.Usage) {
	dst.InputTokens += u.InputTokens
	dst.OutputTokens += u.OutputTokens
	dst.CacheReadTokens += u.CacheReadTokens
}

// decodeStrict decodifica rejeitando campos desconhecidos.
func decodeStrict(input json.RawMessage, v any) error {
	if !json.Valid(input) {
		return errors.New("entrada JSON inválida (possivelmente truncada)")
	}
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("entrada inválida: %w", err)
	}
	return nil
}
