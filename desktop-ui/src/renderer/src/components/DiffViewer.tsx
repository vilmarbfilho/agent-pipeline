import { useEffect, useMemo, useState } from 'react'
import { Button, Spinner } from './ui'

function lineClass(l: string): string {
  if (l.startsWith('+++') || l.startsWith('---')) return 'text-zinc-400 font-semibold'
  if (l.startsWith('diff --git')) return 'mt-3 border-t border-zinc-800 pt-2 text-sky-300 font-semibold'
  if (l.startsWith('@@')) return 'text-violet-300 bg-violet-950/30'
  if (l.startsWith('+')) return 'text-emerald-300 bg-emerald-950/40'
  if (l.startsWith('-')) return 'text-rose-300 bg-rose-950/40'
  if (l.startsWith('#')) return 'text-amber-300/80'
  return 'text-zinc-400'
}

interface Props {
  /** Diff capturado ao final da execução (Result.diff). */
  initial: string
  onClose(): void
}

/** Modal com o diff colorido; permite recarregar do working tree (`git diff HEAD`). */
export function DiffViewer({ initial, onClose }: Props) {
  const [diff, setDiff] = useState(initial)
  const [source, setSource] = useState<'result' | 'live'>('result')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function reload() {
    setLoading(true)
    setError(null)
    try {
      setDiff(await window.orchestrator.gitDiff())
      setSource('live')
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const lines = useMemo(() => diff.split('\n'), [diff])
  const stats = useMemo(() => {
    let add = 0
    let del = 0
    for (const l of lines) {
      if (l.startsWith('+') && !l.startsWith('+++')) add++
      else if (l.startsWith('-') && !l.startsWith('---')) del++
    }
    return { add, del }
  }, [lines])

  return (
    <div className="fixed inset-0 z-40 grid place-items-center bg-black/70 p-6 backdrop-blur-sm" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Diff"
        onClick={(e) => e.stopPropagation()}
        className="flex h-full w-full max-w-6xl flex-col overflow-hidden rounded-xl border border-zinc-700 bg-zinc-900"
      >
        <header className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2">
          <h2 className="font-semibold">Diff</h2>
          <span className="text-xs text-zinc-500">{source === 'result' ? 'capturado ao fim da execução' : 'working tree atual (git diff HEAD)'}</span>
          <span className="text-xs text-emerald-400">+{stats.add}</span>
          <span className="text-xs text-rose-400">−{stats.del}</span>
          <div className="flex-1" />
          <Button onClick={reload} disabled={loading}>
            {loading ? <Spinner /> : '↻'} Working tree
          </Button>
          <Button onClick={() => window.orchestrator.copyText(diff)} disabled={!diff}>Copiar</Button>
          <Button onClick={onClose}>Fechar</Button>
        </header>
        {error && <p className="bg-rose-950/50 px-4 py-2 font-mono text-xs text-rose-300">{error}</p>}
        <pre className="flex-1 overflow-auto p-4 font-mono text-xs leading-5">
          {diff.trim() ? (
            lines.map((l, i) => (
              <div key={i} className={`px-2 whitespace-pre ${lineClass(l)}`}>
                {l || ' '}
              </div>
            ))
          ) : (
            <span className="text-zinc-500">Sem alterações.</span>
          )}
        </pre>
      </div>
    </div>
  )
}
