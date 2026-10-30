import { memo, useState } from 'react'
import type { ToolCall } from '../state/run'
import { Badge, phaseLabel, Spinner, type Tone } from './ui'

const toolTone: Record<string, Tone> = {
  read_file: 'sky',
  list_files: 'sky',
  git_diff: 'violet',
  write_file: 'amber',
  run_command: 'amber'
}
const TERMINAL_TOOLS = new Set(['submit_plan', 'finish', 'submit_review'])

/** Resumo de uma linha da entrada, por ferramenta. */
function summarize(tool: string, input: unknown): string {
  if (!input || typeof input !== 'object') return ''
  const i = input as Record<string, unknown>
  switch (tool) {
    case 'read_file': {
      const range = i.start_line ? `:${i.start_line}-${i.end_line || ''}` : ''
      return `${i.path ?? ''}${range}`
    }
    case 'write_file':
      return `${i.path ?? ''}${typeof i.content === 'string' ? ` (${i.content.length} bytes)` : ''}`
    case 'list_files':
      return `${i.path || '.'}${i.pattern ? ` ${i.pattern}` : ''}`
    case 'run_command':
      return `${i.command ?? ''} ${Array.isArray(i.args) ? i.args.join(' ') : ''}`.trim()
    case 'git_diff':
      return 'working tree'
    default:
      return ''
  }
}

function pretty(v: unknown): string {
  if (v === undefined || v === null) return ''
  if (typeof v === 'string') return v
  return JSON.stringify(v, null, 2)
}

const ToolItem = memo(function ToolItem({ call }: { call: ToolCall }) {
  const [open, setOpen] = useState(false)
  const running = call.output === undefined
  const terminal = TERMINAL_TOOLS.has(call.tool)
  const summary = summarize(call.tool, call.input)
  const dot = running ? 'bg-sky-400 animate-pulse' : call.isError ? 'bg-rose-500' : 'bg-emerald-500'

  return (
    <li className="relative pl-5">
      <span className={`absolute top-3 left-0 size-2 rounded-full ${dot}`} />
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-zinc-800/60"
      >
        <Badge tone={terminal ? 'emerald' : (toolTone[call.tool] ?? 'zinc')}>{call.tool}</Badge>
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-zinc-400" title={summary}>
          {summary}
        </span>
        {running ? <Spinner className="text-sky-400" /> : call.isError ? <span className="text-xs text-rose-400">erro</span> : null}
        <span className={`text-xs text-zinc-500 transition-transform ${open ? 'rotate-90' : ''}`}>▸</span>
      </button>
      {open && (
        <div className="mt-1 mb-2 space-y-2 pl-2">
          <div className="text-[11px] text-zinc-500">
            {phaseLabel[call.phase] ?? call.phase} · {new Date(call.startedAt).toLocaleTimeString()}
          </div>
          <Section label="Entrada" body={pretty(call.input)} />
          {!running && <Section label="Saída" body={call.output || '(vazio)'} error={call.isError} />}
        </div>
      )}
    </li>
  )
})

function Section({ label, body, error }: { label: string; body: string; error?: boolean }) {
  return (
    <div>
      <div className="mb-0.5 text-[11px] font-semibold uppercase tracking-wider text-zinc-500">{label}</div>
      <pre
        className={`max-h-72 overflow-auto rounded border bg-zinc-950 p-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-all ${error ? 'border-rose-900 text-rose-300' : 'border-zinc-800 text-zinc-300'}`}
      >
        {body}
      </pre>
    </div>
  )
}

export function ToolTimeline({ tools }: { tools: ToolCall[] }) {
  if (tools.length === 0) {
    return <p className="p-4 text-xs text-zinc-500">Nenhuma ferramenta invocada ainda.</p>
  }
  return (
    <ol className="relative space-y-0.5 overflow-y-auto p-3 before:absolute before:top-4 before:bottom-4 before:left-[15px] before:w-px before:bg-zinc-800">
      {tools.map((t) => (
        <ToolItem key={t.callId} call={t} />
      ))}
    </ol>
  )
}
