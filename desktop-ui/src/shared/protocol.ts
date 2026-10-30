// Tipos do protocolo JSON-RPC exposto por `orchestrator serve` e do contrato
// IPC entre main e renderer. Espelham internal/rpc e internal/orchestrator.

export type PhaseName = 'planner' | 'coder' | 'reviewer'
export const PHASES: PhaseName[] = ['planner', 'coder', 'reviewer']

export type EventType =
  | 'run_started'
  | 'phase_started'
  | 'phase_completed'
  | 'llm_text'
  | 'llm_thinking'
  | 'llm_turn_end'
  | 'tool_call'
  | 'tool_result'
  | 'run_completed'
  | 'error'

export interface Usage {
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
}

export interface OrchestratorEvent {
  type: EventType
  time: string
  phase?: string
  step?: number
  text?: string
  tool?: string
  call_id?: string
  input?: unknown
  output?: string
  is_error?: boolean
  usage?: Usage
  data?: unknown
}

export interface PlanStep {
  title: string
  details: string
  files?: string[]
}

export interface RunResult {
  task: string
  plan: { summary: string; steps: PlanStep[] | null }
  coder: { summary: string; files_changed: string[] | null; tests_run?: string }
  review: {
    approved: boolean
    summary: string
    issues?: string[] | null
    commit_message: string
    pr_title: string
    pr_description: string
  }
  diff: string
  rounds: number
  usage: Usage
  success: boolean
}

export interface ApprovalRequest {
  id: string
  run_id: string
  request: { tool: string; action: string; reason: string; details: string }
}

/** Notificações servidor -> cliente, já discriminadas por método. */
export type ServerNotification =
  | { method: 'event'; params: { run_id: string; event: OrchestratorEvent } }
  | { method: 'approval.request'; params: ApprovalRequest }
  | { method: 'run.finished'; params: { run_id: string; result: RunResult | null; error?: string } }

// ---------------------------------------------------------------------------
// Sidecar / IPC

export type SidecarState = 'stopped' | 'starting' | 'running' | 'stopping' | 'crashed'

export interface SidecarStatus {
  state: SidecarState
  repo?: string
  pid?: number
  /** Mensagem de erro/saída quando state = crashed. */
  message?: string
}

export interface SidecarOptions {
  repo: string
  /** Caminho do binário; vazio = ORCH_BIN ou ../bin/orchestrator. */
  binary?: string
  /** anthropic | openrouter | mock; vazio = .env / ORCH_PROVIDER. */
  provider?: string
  model?: string
  confirmWrites?: boolean
}

/** Canais IPC. Centralizados para que main e preload não divirjam. */
export const IPC = {
  selectRepo: 'dialog:select-repo',
  sidecarStart: 'sidecar:start',
  sidecarStop: 'sidecar:stop',
  sidecarStatus: 'sidecar:status',
  runStart: 'rpc:run',
  runCancel: 'rpc:cancel',
  approvalRespond: 'rpc:approval-respond',
  gitDiff: 'repo:git-diff',
  copyText: 'clipboard:write',
  defaultBinary: 'sidecar:default-binary',
  // main -> renderer
  onNotification: 'sidecar:notification',
  onStatus: 'sidecar:status-changed',
  onLog: 'sidecar:log'
} as const

/** API exposta ao renderer via contextBridge (window.orchestrator). */
export interface OrchestratorAPI {
  selectRepo(): Promise<string | null>
  defaultBinary(): Promise<string>
  sidecar: {
    start(opts: SidecarOptions): Promise<SidecarStatus>
    stop(): Promise<SidecarStatus>
    status(): Promise<SidecarStatus>
  }
  run(task: string): Promise<{ run_id: string }>
  cancel(runId: string): Promise<{ ok: boolean }>
  respondApproval(id: string, approved: boolean): Promise<{ ok: boolean }>
  gitDiff(): Promise<string>
  copyText(text: string): Promise<void>
  onNotification(cb: (n: ServerNotification) => void): () => void
  onStatus(cb: (s: SidecarStatus) => void): () => void
  onLog(cb: (line: string) => void): () => void
}
