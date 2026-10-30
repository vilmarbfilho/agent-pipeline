package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orchestrator/internal/llm"
)

// Phase nomeia os papéis do pipeline.
const (
	PhasePlanner  = "planner"
	PhaseCoder    = "coder"
	PhaseReviewer = "reviewer"
)

// Plan é a saída estruturada do Planner.
type Plan struct {
	Summary string     `json:"summary"`
	Steps   []PlanStep `json:"steps"`
}

// PlanStep é uma etapa do plano.
type PlanStep struct {
	Title   string   `json:"title"`
	Details string   `json:"details"`
	Files   []string `json:"files,omitempty"`
}

// Markdown renderiza o plano para prompts e saída.
func (p Plan) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", p.Summary)
	for i, s := range p.Steps {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, s.Title, s.Details)
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, "   Arquivos: %s\n", strings.Join(s.Files, ", "))
		}
	}
	return b.String()
}

// CoderReport é a saída estruturada do Coder.
type CoderReport struct {
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"files_changed"`
	TestsRun     string   `json:"tests_run,omitempty"`
}

// Review é a saída estruturada do Reviewer.
type Review struct {
	Approved      bool     `json:"approved"`
	Summary       string   `json:"summary"`
	Issues        []string `json:"issues,omitempty"`
	CommitMessage string   `json:"commit_message"`
	PRTitle       string   `json:"pr_title"`
	PRDescription string   `json:"pr_description"`
}

const commonRules = `Você opera sobre um repositório local através de ferramentas. Regras:
- Todos os caminhos são relativos à raiz do repositório; não é possível acessar nada fora dela.
- run_command não usa shell: passe o executável em command e os argumentos em args.
- Algumas ações pedem confirmação humana; se uma ação for negada, adapte o plano em vez de insistir.
- Antes de cada ação, explique brevemente o raciocínio (Reason) e então aja (Act) com uma ferramenta; use o resultado (Observe) para decidir o próximo passo.
- Responda no idioma da tarefa.`

const plannerSystem = `Você é o PLANNER de um time de agentes de engenharia de software.
Sua função: entender a demanda e o repositório e produzir um plano de implementação em etapas concretas.
Explore apenas o necessário (list_files, read_file, git_diff) para identificar os arquivos, convenções, comandos de build/teste.
Não modifique arquivos. Quando tiver informação suficiente, chame submit_plan com etapas objetivas, cada uma indicando os arquivos envolvidos e como validar (ex.: qual comando de teste rodar).

` + commonRules

const coderSystem = `Você é o CODER de um time de agentes de engenharia de software.
Sua função: implementar o plano recebido lendo, criando e editando arquivos.
- Leia um arquivo antes de alterá-lo e preserve o estilo do código existente.
- write_file substitui o arquivo inteiro: sempre envie o conteúdo completo.
- Faça apenas as mudanças que o plano e a tarefa exigem.
- Rode os testes/compilação relevantes com run_command e corrija falhas antes de concluir.
Ao terminar, chame finish com um resumo, a lista de arquivos alterados e os testes executados (com resultado).

` + commonRules

const reviewerSystem = `Você é o REVIEWER de um time de agentes de engenharia de software.
Sua função: revisar as alterações não commitadas (use git_diff), verificar se cumprem a tarefa e o plano, e procurar bugs, regressões e lacunas de teste.
Você pode ler arquivos e rodar testes/linters com run_command, mas não pode modificar arquivos.
Chame submit_review com:
- approved=true apenas se as alterações estão corretas e completas; caso contrário approved=false e issues com problemas acionáveis;
- commit_message no formato Conventional Commits (linha de assunto <= 72 caracteres, corpo opcional);
- pr_title e pr_description (Markdown: contexto, o que mudou, como testar).

` + commonRules

func strArray(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func submitPlanTool() TerminalTool {
	return TerminalTool{
		Spec: llm.ToolSpec{
			Name:        "submit_plan",
			Description: "Entrega o plano final de implementação e encerra a fase de planejamento.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{"type": "string", "description": "Resumo da abordagem"},
					"steps": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"title":   map[string]any{"type": "string"},
								"details": map[string]any{"type": "string"},
								"files":   strArray("Arquivos envolvidos"),
							},
							"required":             []string{"title", "details"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"summary", "steps"},
				"additionalProperties": false,
			},
		},
		Validate: func(in json.RawMessage) (any, error) {
			var p Plan
			if err := decodeStrict(in, &p); err != nil {
				return nil, err
			}
			if strings.TrimSpace(p.Summary) == "" || len(p.Steps) == 0 {
				return nil, errors.New("summary e ao menos uma etapa são obrigatórios")
			}
			return p, nil
		},
	}
}

func finishTool() TerminalTool {
	return TerminalTool{
		Spec: llm.ToolSpec{
			Name:        "finish",
			Description: "Sinaliza que a implementação terminou e encerra a fase de codificação.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary":       map[string]any{"type": "string", "description": "O que foi feito"},
					"files_changed": strArray("Arquivos criados/alterados"),
					"tests_run":     map[string]any{"type": "string", "description": "Comandos de teste executados e resultado"},
				},
				"required":             []string{"summary", "files_changed"},
				"additionalProperties": false,
			},
		},
		Validate: func(in json.RawMessage) (any, error) {
			var r CoderReport
			if err := decodeStrict(in, &r); err != nil {
				return nil, err
			}
			if strings.TrimSpace(r.Summary) == "" {
				return nil, errors.New("summary é obrigatório")
			}
			return r, nil
		},
	}
}

func submitReviewTool() TerminalTool {
	return TerminalTool{
		Spec: llm.ToolSpec{
			Name:        "submit_review",
			Description: "Entrega a revisão final (aprovação, problemas, mensagem de commit e descrição de PR).",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"approved":       map[string]any{"type": "boolean"},
					"summary":        map[string]any{"type": "string"},
					"issues":         strArray("Problemas acionáveis (vazio se aprovado)"),
					"commit_message": map[string]any{"type": "string"},
					"pr_title":       map[string]any{"type": "string"},
					"pr_description": map[string]any{"type": "string"},
				},
				"required":             []string{"approved", "summary", "commit_message", "pr_title", "pr_description"},
				"additionalProperties": false,
			},
		},
		Validate: func(in json.RawMessage) (any, error) {
			var r Review
			if err := decodeStrict(in, &r); err != nil {
				return nil, err
			}
			if !r.Approved && len(r.Issues) == 0 {
				return nil, errors.New("revisão reprovada precisa listar issues")
			}
			if strings.TrimSpace(r.CommitMessage) == "" {
				return nil, errors.New("commit_message é obrigatório")
			}
			return r, nil
		},
	}
}
