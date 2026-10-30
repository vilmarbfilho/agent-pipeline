import { useEffect, useState } from 'react'
import type { SidecarOptions, SidecarStatus } from '@shared/protocol'
import { Badge, Button, Spinner, type Tone } from './ui'

const STORAGE_KEY = 'orchestrator.connection'

const statusView: Record<SidecarStatus['state'], { tone: Tone; label: string }> = {
  stopped: { tone: 'zinc', label: 'Desconectado' },
  starting: { tone: 'sky', label: 'Iniciando…' },
  running: { tone: 'emerald', label: 'Conectado' },
  stopping: { tone: 'amber', label: 'Encerrando…' },
  crashed: { tone: 'rose', label: 'Falhou' }
}

function loadOptions(): SidecarOptions {
  try {
    return { repo: '', provider: '', model: '', binary: '', confirmWrites: false, ...JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}') }
  } catch {
    return { repo: '' }
  }
}

interface Props {
  status: SidecarStatus
  busy: boolean
  onConnect(opts: SidecarOptions): Promise<unknown>
  onDisconnect(): Promise<unknown>
}

export function ConnectionPanel({ status, busy, onConnect, onDisconnect }: Props) {
  const [opts, setOpts] = useState<SidecarOptions>(loadOptions)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [defaultBinary, setDefaultBinary] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  useEffect(() => {
    void window.orchestrator.defaultBinary().then(setDefaultBinary)
  }, [])
  useEffect(() => localStorage.setItem(STORAGE_KEY, JSON.stringify(opts)), [opts])

  const connected = status.state === 'running'
  const transitioning = pending || status.state === 'starting' || status.state === 'stopping'
  const view = statusView[status.state]
  const set = (patch: Partial<SidecarOptions>) => setOpts((o) => ({ ...o, ...patch }))

  async function pickRepo() {
    const dir = await window.orchestrator.selectRepo()
    if (dir) set({ repo: dir })
  }

  async function toggle() {
    setError(null)
    setPending(true)
    try {
      if (connected) await onDisconnect()
      else await onConnect(opts)
    } catch (err) {
      setError((err as Error).message.replace(/^Error invoking remote method '[^']+': (Error: )?/, ''))
    } finally {
      setPending(false)
    }
  }

  const locked = connected || transitioning

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Button onClick={pickRepo} disabled={locked} title="Escolher pasta do repositório">
          📁 Repositório
        </Button>
        <div
          className="min-w-0 flex-1 truncate rounded-md border border-zinc-800 bg-zinc-950 px-3 py-1.5 font-mono text-xs text-zinc-300"
          title={opts.repo}
        >
          {opts.repo || <span className="text-zinc-500">nenhuma pasta selecionada</span>}
        </div>
        <Badge tone={view.tone}>
          {transitioning && <Spinner />}
          {view.label}
          {connected && status.pid ? <span className="text-zinc-500">pid {status.pid}</span> : null}
        </Badge>
        <Button variant="ghost" onClick={() => setShowAdvanced((v) => !v)} disabled={locked} aria-expanded={showAdvanced}>
          ⚙︎
        </Button>
        <Button variant={connected ? 'ghost' : 'primary'} onClick={toggle} disabled={transitioning || busy || (!connected && !opts.repo)}>
          {connected ? 'Desconectar' : 'Conectar'}
        </Button>
      </div>

      {showAdvanced && !locked && (
        <div className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-2 rounded-md border border-zinc-800 bg-zinc-950/60 p-3 text-sm">
          <label htmlFor="bin" className="text-zinc-400">Binário</label>
          <input
            id="bin"
            className="rounded border border-zinc-800 bg-zinc-900 px-2 py-1 font-mono text-xs"
            placeholder={defaultBinary}
            value={opts.binary ?? ''}
            onChange={(e) => set({ binary: e.target.value })}
          />
          <label htmlFor="provider" className="text-zinc-400">Provedor</label>
          <select
            id="provider"
            className="rounded border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs"
            value={opts.provider ?? ''}
            onChange={(e) => set({ provider: e.target.value })}
          >
            <option value="">padrão (.env / ORCH_PROVIDER)</option>
            <option value="anthropic">anthropic</option>
            <option value="openrouter">openrouter</option>
            <option value="mock">mock (offline, sem API key)</option>
          </select>
          <label htmlFor="model" className="text-zinc-400">Modelo</label>
          <input
            id="model"
            className="rounded border border-zinc-800 bg-zinc-900 px-2 py-1 font-mono text-xs"
            placeholder="padrão (ORCH_MODEL)"
            value={opts.model ?? ''}
            onChange={(e) => set({ model: e.target.value })}
          />
          <span />
          <label className="flex items-center gap-2 text-xs text-zinc-300">
            <input type="checkbox" checked={!!opts.confirmWrites} onChange={(e) => set({ confirmWrites: e.target.checked })} />
            Pedir confirmação para toda escrita de arquivo (--confirm-writes)
          </label>
        </div>
      )}

      {(error || (status.state === 'crashed' && status.message)) && (
        <p className="rounded-md border border-rose-900 bg-rose-950/50 px-3 py-2 font-mono text-xs whitespace-pre-wrap text-rose-300">
          {error ?? status.message}
        </p>
      )}
    </div>
  )
}
