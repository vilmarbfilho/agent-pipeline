package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/llm"
	"orchestrator/internal/tools"
)

func newEnv(t *testing.T) *tools.Env {
	t.Helper()
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# projeto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &tools.Env{Sandbox: sb, Approver: tools.DenyAll, Policy: tools.DefaultPolicy(), CommandTimeout: 10 * time.Second}
}

func TestPipelineWithDemoClient(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	env := newEnv(t)
	rec := &Recorder{}
	mock := DemoClient()
	o := &Orchestrator{LLM: mock, Env: env, Sink: rec, Config: Config{MaxStepsPerPhase: 10}}

	res, err := o.Run(context.Background(), "crie um arquivo de demo")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Plan.Summary == "" || len(res.Coder.FilesChanged) != 1 {
		t.Fatalf("resultado inesperado: %+v", res)
	}
	if !strings.Contains(res.Diff, "ORCHESTRATOR_DEMO.md") {
		t.Fatalf("diff final sem o arquivo: %q", res.Diff)
	}
	if mock.Remaining() != 0 {
		t.Fatalf("respostas não consumidas: %d", mock.Remaining())
	}

	// Cada fase recebe só as ferramentas permitidas.
	toolNames := func(r llm.Request) string {
		var n []string
		for _, s := range r.Tools {
			n = append(n, s.Name)
		}
		return strings.Join(n, ",")
	}
	if got := toolNames(mock.Requests[0]); strings.Contains(got, "write_file") || strings.Contains(got, "run_command") {
		t.Errorf("planner não deve escrever nem executar: %s", got)
	}
	if got := toolNames(mock.Requests[2]); !strings.Contains(got, "write_file") || !strings.Contains(got, "finish") {
		t.Errorf("coder sem ferramentas esperadas: %s", got)
	}
	if got := toolNames(mock.Requests[5]); strings.Contains(got, "write_file") {
		t.Errorf("reviewer não deve escrever: %s", got)
	}
	// O loop ReAct devolve o resultado da ferramenta ao modelo.
	second := mock.Requests[1].Messages
	last := second[len(second)-1]
	if len(last.ToolResults) != 1 || !strings.Contains(last.ToolResults[0].Content, "README.md") {
		t.Errorf("observação do list_files não voltou ao modelo: %+v", last)
	}
	if len(rec.OfType(EventPhaseCompleted)) != 3 || len(rec.OfType(EventRunCompleted)) != 1 {
		t.Errorf("eventos de fase incorretos: %d", len(rec.OfType(EventPhaseCompleted)))
	}
}

func TestAgentErrorsAndNudges(t *testing.T) {
	env := newEnv(t)
	mock := llm.NewMock(
		// Ferramenta desconhecida + escrita sensível negada -> erros como observação.
		llm.MockToolCalls("tentando", "delete_repo", map[string]any{}, "write_file", map[string]any{"path": ".env", "content": "X=1"}),
		// Terminal com entrada inválida -> erro de validação.
		llm.MockToolCalls("enviando", "submit_plan", map[string]any{"summary": "", "steps": []any{}}),
		// Termina sem ferramenta -> recebe um lembrete.
		llm.MockText("acho que terminei"),
		llm.MockToolCalls("ok", "submit_plan", Plan{Summary: "s", Steps: []PlanStep{{Title: "t", Details: "d"}}}),
	)
	a := &Agent{Name: "planner", LLM: mock, Tools: tools.Default(env), Terminal: submitPlanTool(), MaxSteps: 10}
	res, err := a.Run(context.Background(), "tarefa")
	if err != nil {
		t.Fatal(err)
	}
	if res.Output.(Plan).Summary != "s" || res.Steps != 4 {
		t.Fatalf("resultado: %+v", res)
	}
	obs := mock.Requests[1].Messages[2].ToolResults
	if len(obs) != 2 || !obs[0].IsError || !obs[1].IsError || !strings.Contains(obs[1].Content, "negada") {
		t.Fatalf("observações de erro esperadas: %+v", obs)
	}
	if _, err := os.Stat(filepath.Join(env.Sandbox.Root(), ".env")); err == nil {
		t.Fatal(".env não deveria existir")
	}
	nudge := mock.Requests[3].Messages[len(mock.Requests[3].Messages)-1]
	if !strings.Contains(nudge.Text, "submit_plan") {
		t.Fatalf("lembrete esperado: %+v", nudge)
	}
}

func TestAgentLimitsAndRefusal(t *testing.T) {
	env := newEnv(t)
	loop := llm.NewMock(
		llm.MockToolCalls("a", "list_files", map[string]any{}),
		llm.MockToolCalls("b", "list_files", map[string]any{}),
	)
	a := &Agent{Name: "coder", LLM: loop, Tools: tools.Default(env), Terminal: finishTool(), MaxSteps: 2}
	if _, err := a.Run(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), ErrMaxSteps.Error()) {
		t.Fatalf("esperava ErrMaxSteps: %v", err)
	}

	refusal := llm.NewMock(&llm.Response{StopReason: llm.StopRefusal, RefusalCategory: "cyber", Message: llm.Message{Role: llm.RoleAssistant}})
	a = &Agent{Name: "coder", LLM: refusal, Tools: tools.Default(env), Terminal: finishTool()}
	if _, err := a.Run(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "recusou") {
		t.Fatalf("esperava erro de recusa: %v", err)
	}

	// Tool calls cortadas por max_tokens não são executadas.
	trunc := llm.MockToolCalls("x", "write_file", map[string]any{"path": "a.txt", "content": "parcial"})
	trunc.StopReason = llm.StopMaxTokens
	mock := llm.NewMock(trunc, llm.MockToolCalls("ok", "finish", CoderReport{Summary: "s", FilesChanged: []string{}}))
	a = &Agent{Name: "coder", LLM: mock, Tools: tools.Default(env), Terminal: finishTool()}
	if _, err := a.Run(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.Sandbox.Root(), "a.txt")); err == nil {
		t.Fatal("escrita truncada não deveria ter sido executada")
	}
}

func TestReviewRejectionTriggersFixRound(t *testing.T) {
	env := newEnv(t)
	plan := Plan{Summary: "s", Steps: []PlanStep{{Title: "t", Details: "d"}}}
	report := CoderReport{Summary: "feito", FilesChanged: []string{"a.go"}}
	mock := llm.NewMock(
		llm.MockToolCalls("", "submit_plan", plan),
		llm.MockToolCalls("", "finish", report),
		llm.MockToolCalls("", "submit_review", Review{Approved: false, Summary: "faltou teste", Issues: []string{"adicionar teste"}, CommitMessage: "feat: x"}),
		llm.MockToolCalls("", "finish", report),
		llm.MockToolCalls("", "submit_review", Review{Approved: true, Summary: "ok", CommitMessage: "feat: x", PRTitle: "x", PRDescription: "y"}),
	)
	o := &Orchestrator{LLM: mock, Env: env, Config: Config{MaxReviewRounds: 1}}
	res, err := o.Run(context.Background(), "tarefa")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Rounds != 2 {
		t.Fatalf("esperava aprovação na 2ª rodada: %+v", res)
	}
	if !strings.Contains(mock.Requests[3].Messages[0].Text, "adicionar teste") {
		t.Fatal("issues da revisão não foram repassadas ao coder")
	}
}
