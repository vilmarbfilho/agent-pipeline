package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ---------------------------------------------------------------- run_command

// RunCommand executa um comando local (sem shell) na raiz do repositório.
type RunCommand struct{ env *Env }

func (*RunCommand) Name() string { return "run_command" }
func (*RunCommand) Description() string {
	return "Executa um comando na raiz do repositório SEM shell (sem pipes, redirecionamentos ou globs). " +
		"Retorna exit code, stdout e stderr. Use para rodar testes, linters e builds (ex.: command=\"go\", args=[\"test\",\"./...\"]). " +
		"Comandos fora da lista segura exigem confirmação do usuário."
}
func (*RunCommand) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "Executável (ex.: \"go\", \"npm\")"},
			"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Argumentos"},
			"timeout_seconds": map[string]any{
				"type": "integer", "description": "Timeout opcional (limitado ao máximo configurado)",
			},
		},
		"required": []string{"command"},
	}
}

type runCommandInput struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// CommandResult é o resultado estruturado de uma execução.
type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
	Duration time.Duration
}

func (t *RunCommand) Run(ctx context.Context, input json.RawMessage) (string, error) {
	var in runCommandInput
	if err := decode(input, &in); err != nil {
		return "", err
	}
	in.Command = strings.TrimSpace(in.Command)
	if in.Command == "" {
		return "", errors.New("command é obrigatório")
	}
	if strings.ContainsAny(in.Command, " \t|;&><$`") {
		return "", errors.New("command deve ser apenas o executável; passe os argumentos em args (não há shell)")
	}
	full := strings.TrimSpace(in.Command + " " + strings.Join(in.Args, " "))
	if reason := t.env.Policy.CommandRisk(in.Command, in.Args); reason != "" {
		if err := t.env.approve(ctx, ApprovalRequest{
			Tool: t.Name(), Action: "run_command " + full, Reason: reason, Details: full,
		}); err != nil {
			return "", err
		}
	}

	timeout := t.env.CommandTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if in.TimeoutSeconds > 0 && time.Duration(in.TimeoutSeconds)*time.Second < timeout {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	res, err := t.env.Exec(ctx, timeout, in.Command, in.Args...)
	if err != nil {
		return "", err
	}
	max := t.env.maxOut()
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s\nexit_code: %d (%.1fs)\n", full, res.ExitCode, res.Duration.Seconds())
	if res.TimedOut {
		fmt.Fprintf(&b, "TIMEOUT: processo encerrado após %s\n", timeout)
	}
	fmt.Fprintf(&b, "--- stdout ---\n%s\n--- stderr ---\n%s", truncate(res.Stdout, max), truncate(res.Stderr, max/2))
	return b.String(), nil
}

// Exec roda um executável na raiz do sandbox, com timeout e ambiente filtrado.
// Exit code != 0 não é erro (vai no resultado); erro só para falha ao iniciar.
func (e *Env) Exec(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = e.Sandbox.Root()
	cmd.Env = filteredEnv()
	// Fecha os pipes mesmo que subprocessos órfãos os mantenham abertos.
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	res := &CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	case res.TimedOut:
		res.ExitCode = -1
	default:
		return nil, fmt.Errorf("falha ao executar %s: %w", name, err)
	}
	return res, nil
}

// filteredEnv remove segredos do orquestrador do ambiente dos subprocessos,
// para que um comando sugerido pelo LLM não consiga exfiltrar a API key.
func filteredEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		ku := strings.ToUpper(k)
		if strings.HasPrefix(ku, "ANTHROPIC_") || strings.HasPrefix(ku, "OPENAI_") ||
			strings.HasPrefix(ku, "OPENROUTER_") || strings.HasPrefix(ku, "ORCH_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ------------------------------------------------------------------ git_diff

// GitDiff retorna as alterações não commitadas (staged, unstaged e arquivos novos).
type GitDiff struct{ env *Env }

func (*GitDiff) Name() string { return "git_diff" }
func (*GitDiff) Description() string {
	return "Retorna o diff de todas as alterações não commitadas no repositório (staged, unstaged e arquivos não rastreados)."
}
func (*GitDiff) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *GitDiff) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	out, err := t.env.Diff(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "(sem alterações não commitadas)", nil
	}
	return truncate(out, t.env.maxOut()), nil
}

// Diff monta o diff completo do working tree.
func (e *Env) Diff(ctx context.Context) (string, error) {
	const to = 30 * time.Second
	git := func(args ...string) (string, error) {
		res, err := e.Exec(ctx, to, "git", append([]string{"-c", "core.quotepath=off", "--no-pager"}, args...)...)
		if err != nil {
			return "", err
		}
		// git diff --no-index retorna 1 quando há diferenças.
		if res.ExitCode > 1 || (res.ExitCode == 1 && args[0] != "diff") {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(res.Stderr))
		}
		return res.Stdout, nil
	}
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		return "", fmt.Errorf("o repositório não é um repositório git: %w", err)
	}
	var b strings.Builder
	staged, err := git("diff", "--cached")
	if err != nil {
		return "", err
	}
	b.WriteString(staged)
	unstaged, err := git("diff")
	if err != nil {
		return "", err
	}
	b.WriteString(unstaged)
	untracked, err := git("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	for _, f := range strings.Split(untracked, "\x00") {
		if f == "" {
			continue
		}
		d, err := git("diff", "--no-index", "--", os.DevNull, f)
		if err != nil {
			return "", err
		}
		b.WriteString(d)
	}
	return b.String(), nil
}
