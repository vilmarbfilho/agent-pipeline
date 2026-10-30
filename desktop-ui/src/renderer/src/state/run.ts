import { PHASES, type ApprovalRequest, type PhaseName, type RunResult, type ServerNotification, type Usage } from '@shared/protocol'

export type PhaseStatus = 'pending' | 'active' | 'done' | 'failed'

export type MessageBlock = {
  id: number
  kind: 'text' | 'thinking'
  phase: string
  content: string
  /** false enquanto o modelo ainda está emitindo deltas para este bloco. */
  closed: boolean
}

export type ToolCall = {
  callId: string
  phase: string
  tool: string
  input: unknown
  output?: string
  isError?: boolean
  startedAt: string
  finishedAt?: string
}

export type RunStatus = 'idle' | 'starting' | 'running' | 'cancelling' | 'succeeded' | 'rejected' | 'failed' | 'cancelled'

export interface RunState {
  runId: string | null
  task: string
  status: RunStatus
  phases: Record<PhaseName, PhaseStatus>
  /** Rodada atual Coder <-> Reviewer (1 = primeira). */
  round: number
  currentPhase: string | null
  messages: MessageBlock[]
  tools: ToolCall[]
  approvals: ApprovalRequest[]
  result: RunResult | null
  error: string | null
  usage: Usage
}

const emptyUsage: Usage = { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0 }
const pendingPhases = (): Record<PhaseName, PhaseStatus> => ({ planner: 'pending', coder: 'pending', reviewer: 'pending' })

export const initialRun: RunState = {
  runId: null,
  task: '',
  status: 'idle',
  phases: pendingPhases(),
  round: 0,
  currentPhase: null,
  messages: [],
  tools: [],
  approvals: [],
  result: null,
  error: null,
  usage: emptyUsage
}

export type RunAction =
  | { type: 'start'; task: string }
  | { type: 'started'; runId: string }
  | { type: 'start_failed'; error: string }
  | { type: 'cancelling' }
  | { type: 'approval_answered'; id: string }
  | { type: 'sidecar_lost'; error: string }
  | { type: 'reset' }
  | { type: 'notifications'; batch: ServerNotification[] }

let blockSeq = 0
const isPhase = (p: string | undefined): p is PhaseName => !!p && (PHASES as string[]).includes(p)
const isActive = (s: RunStatus) => s === 'starting' || s === 'running' || s === 'cancelling'

export function runReducer(state: RunState, action: RunAction): RunState {
  switch (action.type) {
    case 'start':
      return { ...initialRun, task: action.task, status: 'starting', phases: pendingPhases() }
    case 'started':
      return { ...state, runId: action.runId, status: state.status === 'starting' ? 'running' : state.status }
    case 'start_failed':
      return { ...state, status: 'failed', error: action.error }
    case 'cancelling':
      return isActive(state.status) ? { ...state, status: 'cancelling' } : state
    case 'approval_answered':
      return { ...state, approvals: state.approvals.filter((a) => a.id !== action.id) }
    case 'sidecar_lost':
      return isActive(state.status) ? finish(state, null, action.error) : state
    case 'reset':
      return initialRun
    case 'notifications': {
      let s = state
      for (const n of action.batch) s = applyNotification(s, n)
      return s
    }
  }
}

function applyNotification(s: RunState, n: ServerNotification): RunState {
  // `run` responde com o run_id só depois que a execução já começou: eventos
  // podem chegar antes do 'started'. Enquanto 'starting', adotamos o primeiro run_id.
  const runId = n.params.run_id
  if (s.status === 'starting' && !s.runId) s = { ...s, runId, status: 'running' }
  if (runId !== s.runId) return s

  switch (n.method) {
    case 'approval.request':
      return { ...s, approvals: [...s.approvals, n.params] }
    case 'run.finished':
      return finish(s, n.params.result, n.params.error)
    case 'event':
      return applyEvent(s, n.params.event)
  }
}

function applyEvent(s: RunState, e: import('@shared/protocol').OrchestratorEvent): RunState {
  switch (e.type) {
    case 'phase_started': {
      if (!isPhase(e.phase)) return s
      const phases = { ...s.phases, [e.phase]: 'active' as const }
      let round = s.round
      if (e.phase === 'coder') {
        round += 1
        // Nova rodada de correção: o Reviewer volta a ficar pendente.
        if (round > 1) phases.reviewer = 'pending'
      }
      return { ...s, phases, round, currentPhase: e.phase, messages: closeOpen(s.messages) }
    }
    case 'phase_completed':
      if (!isPhase(e.phase)) return s
      return { ...s, phases: { ...s.phases, [e.phase]: 'done' }, messages: closeOpen(s.messages) }
    case 'llm_text':
    case 'llm_thinking':
      return { ...s, messages: appendDelta(s.messages, e.type === 'llm_text' ? 'text' : 'thinking', e.phase ?? s.currentPhase ?? '', e.text ?? '') }
    case 'llm_turn_end':
      return {
        ...s,
        messages: closeOpen(s.messages),
        usage: e.usage ? addUsage(s.usage, e.usage) : s.usage
      }
    case 'tool_call':
      return {
        ...s,
        messages: closeOpen(s.messages),
        tools: [
          ...s.tools,
          { callId: e.call_id ?? `call-${s.tools.length}`, phase: e.phase ?? s.currentPhase ?? '', tool: e.tool ?? '?', input: e.input, startedAt: e.time }
        ]
      }
    case 'tool_result': {
      const idx = findLastIndex(s.tools, (t) => t.callId === e.call_id && t.output === undefined)
      if (idx < 0) return s
      const tools = s.tools.slice()
      tools[idx] = { ...tools[idx], output: e.output ?? '', isError: e.is_error, finishedAt: e.time }
      return { ...s, tools }
    }
    case 'error':
      return { ...s, error: e.text ?? 'erro desconhecido' }
    default:
      return s
  }
}

function finish(s: RunState, result: RunResult | null, error: string | undefined | null): RunState {
  const phases = { ...s.phases }
  let status: RunStatus
  if (error) {
    status = s.status === 'cancelling' || /context canceled|cancelad/i.test(error) ? 'cancelled' : 'failed'
    for (const p of PHASES) if (phases[p] === 'active') phases[p] = 'failed'
  } else if (result?.success) {
    status = 'succeeded'
  } else {
    status = 'rejected'
    if (result && !result.review.approved) phases.reviewer = 'failed'
  }
  return {
    ...s,
    status,
    phases,
    result, // em caso de erro o Go devolve o resultado parcial
    error: error || null,
    approvals: [],
    currentPhase: null,
    messages: closeOpen(s.messages)
  }
}

function appendDelta(blocks: MessageBlock[], kind: MessageBlock['kind'], phase: string, text: string): MessageBlock[] {
  if (!text) return blocks
  const last = blocks[blocks.length - 1]
  if (last && !last.closed && last.kind === kind && last.phase === phase) {
    const copy = blocks.slice()
    copy[copy.length - 1] = { ...last, content: last.content + text }
    return copy
  }
  // Mudou de tipo (thinking -> texto): fecha o bloco anterior.
  return [...closeOpen(blocks), { id: ++blockSeq, kind, phase, content: text, closed: false }]
}

function closeOpen(blocks: MessageBlock[]): MessageBlock[] {
  const last = blocks[blocks.length - 1]
  if (!last || last.closed) return blocks
  const copy = blocks.slice()
  copy[copy.length - 1] = { ...last, closed: true }
  return copy
}

function addUsage(a: Usage, b: Usage): Usage {
  return {
    input_tokens: a.input_tokens + (b.input_tokens ?? 0),
    output_tokens: a.output_tokens + (b.output_tokens ?? 0),
    cache_read_tokens: a.cache_read_tokens + (b.cache_read_tokens ?? 0)
  }
}

function findLastIndex<T>(arr: T[], pred: (v: T) => boolean): number {
  for (let i = arr.length - 1; i >= 0; i--) if (pred(arr[i])) return i
  return -1
}

export const isRunActive = (s: RunState) => isActive(s.status)
