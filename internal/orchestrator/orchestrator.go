package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"orchestrator/internal/llm"
	"orchestrator/internal/tools"
)

// Config controla limites do pipeline.
type Config struct {
	MaxStepsPerPhase int
	MaxReviewRounds  int // quantas vezes o Coder pode corrigir após reprovação
	MaxTokens        int64
}

// Orchestrator encadeia Planner -> Coder -> Reviewer sobre um repositório.
type Orchestrator struct {
	LLM    llm.Client
	Env    *tools.Env
	Sink   Sink
	Config Config
}

// Result é o resultado completo de uma execução.
type Result struct {
	Task    string      `json:"task"`
	Plan    Plan        `json:"plan"`
	Coder   CoderReport `json:"coder"`
	Review  Review      `json:"review"`
	Diff    string      `json:"diff"`
	Rounds  int         `json:"rounds"`
	Usage   llm.Usage   `json:"usage"`
	Success bool        `json:"success"`
}

// Run executa o pipeline completo para a tarefa.
func (o *Orchestrator) Run(ctx context.Context, task string) (*Result, error) {
	reg := tools.Default(o.Env)
	res := &Result{Task: task}
	emit(o.Sink, Event{Type: EventRunStarted, Text: task, Data: map[string]string{"repo": o.Env.Sandbox.Root()}})

	// 1. Planner (somente leitura)
	overview := o.repoOverview(ctx, reg)
	out, err := o.phase(ctx, res, PhasePlanner, plannerSystem, reg.Subset("read_file", "list_files", "git_diff"), submitPlanTool(),
		fmt.Sprintf("## Tarefa\n%s\n\n## Visão geral do repositório\n%s", task, overview))
	if err != nil {
		return o.fail(res, err)
	}
	res.Plan = out.(Plan)

	// 2..3. Coder <-> Reviewer, com rodadas de correção.
	var issues []string
	maxRounds := max(o.Config.MaxReviewRounds, 0)
	for round := 0; round <= maxRounds; round++ {
		res.Rounds = round + 1
		coderPrompt := fmt.Sprintf("## Tarefa\n%s\n\n## Plano\n%s", task, res.Plan.Markdown())
		if len(issues) > 0 {
			coderPrompt += "\n\n## Problemas apontados na revisão anterior (corrija-os)\n- " + strings.Join(issues, "\n- ") +
				"\n\nAs alterações da rodada anterior já estão no working tree (veja git_diff)."
		}
		out, err = o.phase(ctx, res, PhaseCoder, coderSystem,
			reg.Subset("read_file", "write_file", "list_files", "run_command", "git_diff"), finishTool(), coderPrompt)
		if err != nil {
			return o.fail(res, err)
		}
		res.Coder = out.(CoderReport)

		reviewPrompt := fmt.Sprintf("## Tarefa\n%s\n\n## Plano\n%s\n\n## Relatório do Coder\n%s\nArquivos: %s\nTestes: %s",
			task, res.Plan.Markdown(), res.Coder.Summary, strings.Join(res.Coder.FilesChanged, ", "), res.Coder.TestsRun)
		out, err = o.phase(ctx, res, PhaseReviewer, reviewerSystem,
			reg.Subset("read_file", "list_files", "run_command", "git_diff"), submitReviewTool(), reviewPrompt)
		if err != nil {
			return o.fail(res, err)
		}
		res.Review = out.(Review)
		if res.Review.Approved {
			break
		}
		issues = res.Review.Issues
	}

	if diff, err := o.Env.Diff(ctx); err == nil {
		res.Diff = diff
	}
	res.Success = res.Review.Approved
	emit(o.Sink, Event{Type: EventRunCompleted, Data: res, Usage: &res.Usage})
	return res, nil
}

func (o *Orchestrator) phase(ctx context.Context, res *Result, name, system string, reg *tools.Registry, term TerminalTool, prompt string) (any, error) {
	emit(o.Sink, Event{Type: EventPhaseStarted, Phase: name})
	agent := &Agent{
		Name:      name,
		System:    system,
		LLM:       o.LLM,
		Tools:     reg,
		Terminal:  term,
		MaxSteps:  o.Config.MaxStepsPerPhase,
		MaxTokens: o.Config.MaxTokens,
		Sink:      o.Sink,
	}
	ar, err := agent.Run(ctx, prompt)
	if ar != nil {
		addUsage(&res.Usage, ar.Usage)
	}
	if err != nil {
		return nil, err
	}
	emit(o.Sink, Event{Type: EventPhaseCompleted, Phase: name, Data: ar.Output, Step: ar.Steps})
	return ar.Output, nil
}

func (o *Orchestrator) fail(res *Result, err error) (*Result, error) {
	emit(o.Sink, Event{Type: EventError, Text: err.Error(), IsError: true})
	return res, err
}

// repoOverview dá ao Planner um ponto de partida sem gastar passos.
func (o *Orchestrator) repoOverview(ctx context.Context, reg *tools.Registry) string {
	t, _ := reg.Get("list_files")
	files, err := t.Run(ctx, json.RawMessage(`{"path":"."}`))
	if err != nil {
		return "(não foi possível listar arquivos: " + err.Error() + ")"
	}
	lines := strings.Split(files, "\n")
	if len(lines) > 200 {
		files = strings.Join(lines[:200], "\n") + fmt.Sprintf("\n...(+%d arquivos; use list_files)", len(lines)-200)
	}
	return "```\n" + files + "\n```"
}
