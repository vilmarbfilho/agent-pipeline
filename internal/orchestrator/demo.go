package orchestrator

import "orchestrator/internal/llm"

// DemoClient retorna um LLM roteirizado que percorre o pipeline inteiro sem
// rede nem API key: lista arquivos, planeja, cria um arquivo, roda um comando,
// revisa o diff e aprova. Usado por `--provider mock` para o primeiro teste.
func DemoClient() *llm.MockClient {
	const file = "ORCHESTRATOR_DEMO.md"
	content := "# Demo do orquestrador\n\nArquivo criado pelo agente Coder no modo mock.\n"
	return llm.NewMock(
		// Planner
		llm.MockToolCalls("Vou inspecionar a estrutura do repositório.", "list_files", map[string]any{"path": "."}),
		llm.MockToolCalls("Tenho contexto suficiente; enviando o plano.", "submit_plan", Plan{
			Summary: "Criar um arquivo de demonstração e validar o diff.",
			Steps: []PlanStep{
				{Title: "Criar " + file, Details: "Escrever um Markdown curto.", Files: []string{file}},
				{Title: "Validar", Details: "Listar arquivos para confirmar a criação."},
			},
		}),
		// Coder
		llm.MockToolCalls("Criando o arquivo planejado.", "write_file", map[string]any{"path": file, "content": content}),
		llm.MockToolCalls("Verificando com um comando local.", "run_command", map[string]any{"command": "ls", "args": []string{"-la"}}),
		llm.MockToolCalls("Implementação concluída.", "finish", CoderReport{
			Summary: "Criado " + file + ".", FilesChanged: []string{file}, TestsRun: "ls -la (exit 0)",
		}),
		// Reviewer
		llm.MockToolCalls("Revisando as alterações.", "git_diff", map[string]any{}),
		llm.MockToolCalls("Alterações conferem com o plano.", "submit_review", Review{
			Approved:      true,
			Summary:       "Arquivo de demo criado conforme o plano.",
			CommitMessage: "docs: adiciona arquivo de demonstração do orquestrador",
			PRTitle:       "docs: arquivo de demonstração",
			PRDescription: "## Contexto\nExecução em modo mock.\n\n## Mudanças\n- Adiciona `" + file + "`.\n\n## Como testar\nAbra o arquivo.",
		}),
	)
}
