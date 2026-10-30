# desktop-ui

Interface desktop (Electron + Vite + React + TypeScript + Tailwind) para o
orquestrador. Ela inicia o binário Go como processo filho
(`orchestrator serve --repo <dir>`) e conversa com ele por JSON-RPC 2.0 no
stdin/stdout. O stderr do processo aparece na gaveta **Logs do servidor**.

```
src/
  shared/protocol.ts        tipos do protocolo JSON-RPC + contrato IPC (canais e API)
  main/index.ts             janela, handlers IPC (com validação), diálogo de pasta, clipboard, git diff
  main/sidecar.ts           ciclo de vida do processo Go + cliente JSON-RPC (framing por linha)
  preload/index.ts          contextBridge: expõe window.orchestrator (API mínima e tipada)
  renderer/src/
    state/run.ts            reducer: eventos do pipeline -> estado da UI (fases, mensagens, ferramentas, aprovações)
    state/useOrchestrator.ts hook que liga o IPC ao reducer (agrupa notificações por frame)
    components/             ConnectionPanel, TaskInput, PhaseStepper, MessageStream (+ thinking),
                            ToolTimeline, ApprovalDialog, ResultPanel, DiffViewer, LogDrawer
```

## Rodando em modo de desenvolvimento

Pré-requisitos: Go 1.25+, Node 20+ (testado com Node 24) e git.

1. **Compile o binário Go** na raiz do repositório (gera `bin/orchestrator`):

   ```sh
   cd ..            # raiz do orquestrador
   make build
   ```

2. **Configure as credenciais** em `../.env` (copie de `.env.example`). A UI
   inicia o binário com o diretório de trabalho na raiz do projeto, então esse
   `.env` é carregado. Para testar sem API key, escolha o provedor `mock` nas
   configurações (⚙︎) da UI.

3. **Instale as dependências** (o `postinstall` baixa o binário do Electron):

   ```sh
   cd desktop-ui
   npm install
   ```

4. **Suba em modo dev** (Vite com HMR no renderer e rebuild do main/preload):

   ```sh
   npm run dev
   ```

5. Na janela:
   1. Clique em **📁 Repositório** e escolha um repositório Git local.
   2. (Opcional) Em **⚙︎**, ajuste o binário, o provedor (`mock` para testar offline), o modelo ou `--confirm-writes`.
   3. Clique em **Conectar**. O status muda para *Conectado* depois que o servidor responde ao `ping`.
   4. Descreva a tarefa e clique em **▶ Iniciar** (ou ⌘/Ctrl+Enter).

Para usar outro binário, defina `ORCH_BIN=/caminho/orchestrator npm run dev`
ou preencha o campo **Binário** nas configurações.

### Outros scripts

| Script | O que faz |
|---|---|
| `npm run typecheck` | `tsc` em main/preload e no renderer |
| `npm run build` | build de produção em `out/` |
| `npm run preview` | roda o build de produção no Electron |

## Segurança

- `contextIsolation: true`, `nodeIntegration: false` e `sandbox: true`. O
  renderer não tem acesso ao Node. Só as funções de `window.orchestrator` são
  expostas, nunca o `ipcRenderer`.
- O main aceita IPC apenas do frame principal da janela e valida tipos e
  tamanhos dos argumentos antes de repassá-los ao processo Go.
- O processo Go é iniciado com `spawn` e argumentos separados, sem shell. O
  provedor é validado contra uma lista fixa.
- CSP estrita em produção (`script-src 'self'`). Em dev, ela é relaxada só para
  o React Refresh e o HMR (veja `electron.vite.config.ts`).
- O markdown do modelo é renderizado sem HTML bruto (sem `rehype-raw`).
  Navegação e novas janelas são bloqueadas, e links `http(s)` abrem no
  navegador do sistema.
- No modal de aprovação, o foco inicial fica em **Recusar** e não há atalho de
  teclado para aprovar.
- Ao sair do app, o stdin do processo é fechado. O `Serve` termina e cancela as
  execuções em andamento. Se o processo não sair, recebe `SIGTERM` e depois
  `SIGKILL`.

## Mapeamento do protocolo

| Notificação / evento | Efeito na UI |
|---|---|
| `phase_started` / `phase_completed` | stepper Planner → Coder → Reviewer (ativo/concluído). Uma nova rodada do Coder reabre o Reviewer |
| `llm_text` / `llm_thinking` | deltas acumulados em blocos markdown / blocos de raciocínio colapsáveis |
| `llm_turn_end` | fecha o bloco atual e soma o uso de tokens |
| `tool_call` / `tool_result` | timeline de ferramentas, ligadas por `call_id`, com entrada e saída expansíveis |
| `error` / `run.finished` com `error` | a fase ativa fica como *falhou* e a mensagem aparece no resultado |
| `approval.request` | modal com fila. A resposta vai por `approval.respond` |
| `run.finished` | aba **Resultado**: revisão, commit sugerido (copiar), PR (copiar), plano, arquivos e diff |
