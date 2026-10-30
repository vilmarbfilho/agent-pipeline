# orchestrator

MVP de um orquestrador multi-agente para desenvolvimento de software local.
Um binário Go roda um pipeline **Planner → Coder → Reviewer** sobre um
repositório. Cada papel é um loop **ReAct** (raciocina, age com uma ferramenta,
observa o resultado) que usa tool calling nativo, via Claude (API da Anthropic)
ou qualquer modelo com suporte a tools no OpenRouter.

```
cmd/orchestrator/        main
internal/cli/            cobra (run + serve), renderização no console, confirmação via stdin
internal/config/         variáveis de ambiente / .env
internal/llm/            interface neutra de provedor + Anthropic + OpenRouter (streaming) + mock
internal/orchestrator/   loop ReAct (Agent), papéis/prompts, pipeline, eventos
internal/tools/          sandbox, política de aprovação, read/write/list/run_command/git_diff
internal/rpc/            JSON-RPC 2.0 via stdio (ponte para UI Electron/Tauri)
```

## Como funciona

| Fase | Ferramentas | Saída estruturada (ferramenta terminal) |
|---|---|---|
| Planner | `read_file`, `list_files`, `git_diff` | `submit_plan {summary, steps[]}` |
| Coder | as acima + `write_file`, `run_command` | `finish {summary, files_changed, tests_run}` |
| Reviewer | `read_file`, `list_files`, `git_diff`, `run_command` | `submit_review {approved, issues, commit_message, pr_title, pr_description}` |

Se o Reviewer reprovar, o Coder recebe a lista de problemas e faz mais uma
rodada de correção (`ORCH_MAX_REVIEW_ROUNDS`). **Nada é commitado.** O
resultado fica no working tree para você revisar.

### Segurança

- **Sandbox de caminhos.** Todo caminho é resolvido em relação a `--repo`. Os
  seguintes casos são rejeitados: `..`, caminhos absolutos fora da raiz, bytes
  nulos e symlinks que apontem para fora (inclusive quando o alvo ainda não
  existe).
- **Sem shell.** `run_command` recebe o executável e os argumentos
  separadamente, e metacaracteres (`; | & > $`) no executável são recusados.
- **Confirmação humana (HITL).** Pedem confirmação:
  - comandos fora da lista segura;
  - `rm`/`curl`/`sudo`/`sh`;
  - `git` com subcomandos que não sejam de leitura;
  - flags como `--force`, `-delete` e `-exec`;
  - escritas sensíveis: `.env`, `.git/`, `.github/`, `*.pem`, `go.mod`, `package.json`, `Makefile` etc.

  Com `--confirm-writes`, toda escrita pede confirmação.
- **Timeout** em todo comando. O stdout e o stderr são capturados e truncados.
- **Segredos.** As variáveis `ANTHROPIC_*`, `OPENAI_*` e `ORCH_*` são removidas
  do ambiente dos subprocessos.
- **Entradas truncadas.** Tool calls cortadas por `max_tokens` não são
  executadas, e JSON inválido volta ao modelo como erro.

### Integração com o Claude

O padrão é `claude-opus-5-5`, com streaming e as seguintes configurações:

- thinking adaptativo com resumo visível, exibido esmaecido no terminal;
- `effort` configurável;
- cache de prompt automático;
- `eager_input_streaming` nas ferramentas;
- **fallback server-side** em recusas dos classificadores de segurança
  (`fallbacks: "default"`). Para desligar, use `ORCH_FALLBACKS=false`.

O histórico é append-only e devolve os blocos de thinking à API sem alteração.

### OpenRouter

Use `--provider openrouter` (ou `ORCH_PROVIDER=openrouter`), defina
`OPENROUTER_API_KEY` e informe o modelo pelo slug do OpenRouter, por exemplo
`--model anthropic/claude-sonnet-4.5`, `openai/gpt-5` ou
`google/gemini-2.5-pro`.

O cliente (`internal/llm/openrouter.go`) usa a API Chat Completions com
streaming SSE e sem dependências extras. Ele:

- converte as ferramentas para `tools[].function`;
- devolve os resultados como mensagens `role: "tool"`;
- exibe o `reasoning` no terminal;
- preserva os `reasoning_details` entre as chamadas, o que alguns modelos
  exigem durante tool calling;
- faz retry em 408/429/5xx;
- envia `provider.require_parameters: true`, para só rotear a provedores que
  suportam tools.

Escolha um modelo com suporte a tool calling. Modelos fracos em seguir
instruções podem esgotar `--max-steps` sem chamar a ferramenta terminal.

Para outro provedor, implemente `llm.Client` em `internal/llm` e registre em
`newLLM` (`internal/cli/root.go`).

## Variáveis de ambiente

Veja [`.env.example`](.env.example). Com o provedor `anthropic`, basta
`ANTHROPIC_API_KEY`. Com `openrouter`, basta `OPENROUTER_API_KEY` + `ORCH_MODEL`. O `.env` é lido do **diretório atual**, nunca do repositório
alvo.

## Passo a passo

```bash
# 0. Pré-requisitos: Go >= 1.25 e git
go version

# 1. Dependências e testes (sem rede/API)
go mod download
make test            # go test -race ./...

# 2. Primeiro teste ponta a ponta OFFLINE (LLM roteirizado, sem API key)
make demo            # cria um repo temporário e roda o pipeline com --provider mock

# 3. Configurar o Claude
cp .env.example .env
#   edite .env e preencha ANTHROPIC_API_KEY

# 4. Primeiro teste real, num repositório descartável
make build
mkdir -p /tmp/alvo && cd /tmp/alvo && git init -q && cat > calc.go <<'EOF'
package calc

func Soma(a, b int) int { return a + b }
EOF
printf 'module calc\n\ngo 1.25\n' > go.mod && git add . && git commit -qm init && cd -

./bin/orchestrator --repo /tmp/alvo --task "adicione testes unitários table-driven para a função Soma" -v

# ou via OpenRouter
OPENROUTER_API_KEY=sk-or-v1-... ./bin/orchestrator --provider openrouter \
  --model anthropic/claude-sonnet-4.5 --repo /tmp/alvo --task "adicione testes para Soma" -v

# 5. Revise e commite você mesmo
git -C /tmp/alvo diff
```

Flags úteis:

- `-y`: aprova tudo automaticamente.
- `-o json`: emite os eventos em JSONL no stdout, para logs e pipelines.
- `--effort xhigh`: mais raciocínio em tarefas difíceis.
- `--command-timeout 5m`: timeout máximo do `run_command`.
- `--max-steps`: limite de passos ReAct por fase.

## JSON-RPC (UI desktop)

```bash
./bin/orchestrator serve --repo /caminho/do/repo
```

A comunicação é JSON-RPC 2.0, com uma mensagem por linha no stdin/stdout. Os
logs vão para o stderr.

```jsonc
// cliente -> servidor
{"jsonrpc":"2.0","id":1,"method":"run","params":{"task":"..."}}          // -> {"run_id":"run-1"}
{"jsonrpc":"2.0","id":2,"method":"approval.respond","params":{"id":"appr-2","approved":true}}
{"jsonrpc":"2.0","id":3,"method":"cancel","params":{"run_id":"run-1"}}
// servidor -> cliente (notificações)
{"jsonrpc":"2.0","method":"event","params":{"run_id":"run-1","event":{"type":"llm_text","phase":"coder","text":"..."}}}
{"jsonrpc":"2.0","method":"approval.request","params":{"id":"appr-2","run_id":"run-1","request":{"tool":"write_file","action":"...","reason":"..."}}}
{"jsonrpc":"2.0","method":"run.finished","params":{"run_id":"run-1","result":{...},"error":""}}
```

Electron e Tauri podem iniciar o binário como processo filho (*sidecar*). A UI
renderiza os eventos e responde aos pedidos de aprovação.

## Limitações do MVP

- Comandos "seguros" como `go test`, `npm test` e `make` executam código do
  próprio repositório. Use em repositórios confiáveis ou dentro de um
  container/VM.
- `write_file` reescreve o arquivo inteiro, sem edição por patch.
- O contexto não é compactado. Tarefas muito longas podem precisar de
  `--max-steps` maior ou ser divididas.
