// Package cli define os comandos cobra do binário.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"orchestrator/internal/config"
	"orchestrator/internal/llm"
	"orchestrator/internal/orchestrator"
	"orchestrator/internal/rpc"
	"orchestrator/internal/tools"
)

type flags struct {
	repo           string
	task           string
	envFile        string
	provider       string
	model          string
	effort         string
	yes            bool
	confirmWrites  bool
	output         string
	verbose        bool
	commandTimeout time.Duration
	maxSteps       int
	reviewRounds   int
}

// Execute é o ponto de entrada da CLI.
func Execute() int {
	if err := newRoot().Execute(); err != nil {
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	f := &flags{}
	root := &cobra.Command{
		Use:   "orchestrator",
		Short: "Orquestrador multi-agente (Planner → Coder → Reviewer) para repositórios locais",
		Long: `Orquestra agentes LLM com tool calling sobre um repositório local.

Exemplo:
  orchestrator --repo ./meu-projeto --task "adicione testes unitários para a função Parse"`,
		SilenceUsage: true,
		RunE:         func(cmd *cobra.Command, _ []string) error { return runTask(cmd, f) },
	}
	pf := root.PersistentFlags()
	pf.StringVar(&f.repo, "repo", ".", "caminho do repositório alvo")
	pf.StringVar(&f.envFile, "env-file", ".env", "arquivo .env a carregar (do diretório atual, não do repo)")
	pf.StringVar(&f.provider, "provider", "", "provedor LLM: anthropic | mock (padrão: ORCH_PROVIDER ou anthropic)")
	pf.StringVar(&f.model, "model", "", "modelo (padrão: ORCH_MODEL ou claude-opus-5-5)")
	pf.StringVar(&f.effort, "effort", "", "esforço: low | medium | high | xhigh | max (padrão: ORCH_EFFORT ou high)")
	pf.BoolVar(&f.confirmWrites, "confirm-writes", false, "pedir confirmação para TODA escrita de arquivo")
	pf.DurationVar(&f.commandTimeout, "command-timeout", 0, "timeout máximo de run_command (ex.: 90s; padrão ORCH_COMMAND_TIMEOUT ou 2m)")
	pf.IntVar(&f.maxSteps, "max-steps", 0, "máximo de passos ReAct por fase (padrão ORCH_MAX_STEPS ou 40)")
	pf.IntVar(&f.reviewRounds, "review-rounds", -1, "rodadas extras de correção após reprovação (padrão ORCH_MAX_REVIEW_ROUNDS ou 1)")

	rf := root.Flags()
	rf.StringVar(&f.task, "task", "", "instrução em linguagem natural")
	rf.BoolVarP(&f.yes, "yes", "y", false, "aprovar automaticamente ações sensíveis (cuidado)")
	rf.StringVarP(&f.output, "output", "o", "pretty", "formato de saída: pretty | json (JSONL de eventos)")
	rf.BoolVarP(&f.verbose, "verbose", "v", false, "mostrar uso de tokens e saídas completas")

	root.AddCommand(newServe(f))
	return root
}

func newServe(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Servidor JSON-RPC 2.0 via stdio (para UIs desktop como Electron/Tauri)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, env, err := setup(f)
			if err != nil {
				return err
			}
			client, err := newLLM(cfg)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			srv := rpc.NewServer(func(approver tools.Approver, sink orchestrator.Sink) *orchestrator.Orchestrator {
				e := *env
				e.Approver = approver
				return newOrchestrator(cfg, client, &e, sink)
			})
			fmt.Fprintf(os.Stderr, "orchestrator: JSON-RPC em stdio (repo=%s)\n", env.Sandbox.Root())
			return srv.Serve(ctx, os.Stdin, os.Stdout)
		},
	}
}

func runTask(cmd *cobra.Command, f *flags) error {
	if strings.TrimSpace(f.task) == "" {
		return errors.New("--task é obrigatório")
	}
	if f.output != "pretty" && f.output != "json" {
		return fmt.Errorf("--output inválido: %q", f.output)
	}
	cfg, env, err := setup(f)
	if err != nil {
		return err
	}
	client, err := newLLM(cfg)
	if err != nil {
		return err
	}
	console := NewConsole(os.Stdout, os.Stdin, f.output == "json", f.yes, f.verbose)
	env.Approver = console

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, err := newOrchestrator(cfg, client, env, console).Run(ctx, f.task)
	if f.output == "json" {
		return err // o resultado já saiu no evento run_completed
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerro: %v\n", err)
		return err
	}
	printSummary(res)
	if !res.Success {
		return errors.New("revisão não aprovou as alterações")
	}
	return nil
}

func setup(f *flags) (*config.Config, *tools.Env, error) {
	cfg, err := config.Load(f.envFile)
	if err != nil {
		return nil, nil, err
	}
	if f.provider != "" {
		cfg.Provider = f.provider
	}
	if f.model != "" {
		cfg.Model = f.model
	}
	if f.effort != "" {
		cfg.Effort = f.effort
	}
	if f.confirmWrites {
		cfg.ConfirmAllWrites = true
	}
	if f.commandTimeout > 0 {
		cfg.CommandTimeout = f.commandTimeout
	}
	if f.maxSteps > 0 {
		cfg.MaxStepsPerPhase = f.maxSteps
	}
	if f.reviewRounds >= 0 {
		cfg.MaxReviewRounds = f.reviewRounds
	}
	sb, err := tools.NewSandbox(f.repo)
	if err != nil {
		return nil, nil, err
	}
	policy := tools.DefaultPolicy()
	policy.ConfirmAllWrites = cfg.ConfirmAllWrites
	return cfg, &tools.Env{Sandbox: sb, Policy: policy, CommandTimeout: cfg.CommandTimeout}, nil
}

func newLLM(cfg *config.Config) (llm.Client, error) {
	switch cfg.Provider {
	case "mock":
		return orchestrator.DemoClient(), nil
	case "anthropic":
		return llm.NewAnthropic(llm.AnthropicConfig{
			APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Model: cfg.Model, Effort: cfg.Effort, Fallbacks: cfg.Fallbacks,
		}), nil
	default:
		return nil, fmt.Errorf("provedor desconhecido: %q (use anthropic ou mock)", cfg.Provider)
	}
}

func newOrchestrator(cfg *config.Config, client llm.Client, env *tools.Env, sink orchestrator.Sink) *orchestrator.Orchestrator {
	return &orchestrator.Orchestrator{
		LLM:  client,
		Env:  env,
		Sink: sink,
		Config: orchestrator.Config{
			MaxStepsPerPhase: cfg.MaxStepsPerPhase,
			MaxReviewRounds:  cfg.MaxReviewRounds,
			MaxTokens:        cfg.MaxTokens,
		},
	}
}

func printSummary(res *orchestrator.Result) {
	status := "✔ APROVADO"
	if !res.Success {
		status = "✗ NÃO APROVADO"
	}
	fmt.Printf("\n══ RESULTADO: %s (rodadas: %d) ══\n\n%s\n", status, res.Rounds, res.Review.Summary)
	for _, i := range res.Review.Issues {
		fmt.Printf("  - %s\n", i)
	}
	fmt.Printf("\n── Commit ──\n%s\n\n── PR: %s ──\n%s\n", res.Review.CommitMessage, res.Review.PRTitle, res.Review.PRDescription)
	u, _ := json.Marshal(res.Usage)
	fmt.Printf("\nTokens: %s\nRevise com: git -C <repo> diff  (nada foi commitado)\n", u)
}
