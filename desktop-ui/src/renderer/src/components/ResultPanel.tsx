import { useState, type ReactNode } from 'react'
import type { RunResult, Usage } from '@shared/protocol'
import type { RunStatus } from '../state/run'
import { DiffViewer } from './DiffViewer'
import { Markdown } from './Markdown'
import { Badge, Button, type Tone } from './ui'

const statusView: Partial<Record<RunStatus, { tone: Tone; label: string; icon: string }>> = {
  succeeded: { tone: 'emerald', label: 'Aprovado pelo Reviewer', icon: '✅' },
  rejected: { tone: 'amber', label: 'Reprovado pelo Reviewer', icon: '⚠️' },
  failed: { tone: 'rose', label: 'Falhou', icon: '❌' },
  cancelled: { tone: 'zinc', label: 'Cancelado', icon: '⏹' }
}

function CopyButton({ text, label = 'Copiar' }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      disabled={!text}
      onClick={async () => {
        await window.orchestrator.copyText(text)
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      }}
    >
      {copied ? '✓ Copiado' : `⧉ ${label}`}
    </Button>
  )
}

function Card({ title, actions, children }: { title: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-zinc-800 bg-zinc-950/60">
      <header className="flex items-center justify-between gap-2 border-b border-zinc-800 px-4 py-2">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-zinc-400">{title}</h3>
        <div className="flex gap-2">{actions}</div>
      </header>
      <div className="px-4 py-3">{children}</div>
    </section>
  )
}

const fmt = (n: number) => n.toLocaleString('pt-BR')

interface Props {
  status: RunStatus
  result: RunResult | null
  error: string | null
  usage: Usage
  onNewTask(): void
}

export function ResultPanel({ status, result, error, usage, onNewTask }: Props) {
  const [showDiff, setShowDiff] = useState(false)
  const view = statusView[status]
  if (!view) return null

  const review = result?.review
  const hasReview = !!review?.summary
  const u = result?.usage?.input_tokens ? result.usage : usage
  const files = result?.coder?.files_changed ?? []
  const commit = review?.commit_message ?? ''
  const prBody = review ? `${review.pr_title ? `# ${review.pr_title}\n\n` : ''}${review.pr_description ?? ''}` : ''

  return (
    <div className="flex-1 space-y-4 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-2xl">{view.icon}</span>
        <div className="flex-1">
          <h2 className="text-lg font-semibold">Resultado</h2>
          <div className="flex flex-wrap items-center gap-2 text-xs text-zinc-500">
            <Badge tone={view.tone}>{view.label}</Badge>
            {result && result.rounds > 0 && <span>{result.rounds} rodada(s) Coder ↔ Reviewer</span>}
            <span>
              tokens: {fmt(u.input_tokens)} in · {fmt(u.output_tokens)} out
              {u.cache_read_tokens ? ` · ${fmt(u.cache_read_tokens)} cache` : ''}
            </span>
          </div>
        </div>
        <Button onClick={() => setShowDiff(true)}>
          ± Ver diff
        </Button>
        <Button variant="primary" onClick={onNewTask}>
          Nova tarefa
        </Button>
      </div>

      {error && (
        <p className="rounded-md border border-rose-900 bg-rose-950/40 px-3 py-2 font-mono text-xs whitespace-pre-wrap text-rose-300">{error}</p>
      )}

      {hasReview && (
        <Card title="Revisão">
          <Markdown>{review!.summary}</Markdown>
          {!!review!.issues?.length && (
            <div className="mt-3">
              <div className="mb-1 text-xs font-semibold text-amber-300">Problemas apontados</div>
              <ul className="list-disc space-y-1 pl-5 text-sm text-zinc-300">
                {review!.issues.map((i, k) => (
                  <li key={k}>{i}</li>
                ))}
              </ul>
            </div>
          )}
        </Card>
      )}

      {commit && (
        <Card title="Mensagem de commit sugerida" actions={<CopyButton text={commit} />}>
          <pre className="font-mono text-sm whitespace-pre-wrap text-zinc-100">{commit}</pre>
        </Card>
      )}

      {review?.pr_description && (
        <Card title="Pull request" actions={<CopyButton text={prBody} label="Copiar PR" />}>
          {review.pr_title && <h4 className="mb-2 font-semibold text-zinc-100">{review.pr_title}</h4>}
          <Markdown>{review.pr_description}</Markdown>
        </Card>
      )}

      {(result?.coder?.summary || files.length > 0) && (
        <Card title="Alterações do Coder">
          {result!.coder.summary && <Markdown>{result!.coder.summary}</Markdown>}
          {files.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1.5">
              {files.map((f) => (
                <Badge key={f}>{f}</Badge>
              ))}
            </div>
          )}
          {result!.coder.tests_run && (
            <pre className="mt-3 rounded border border-zinc-800 bg-zinc-950 p-2 font-mono text-xs whitespace-pre-wrap text-zinc-400">
              {result!.coder.tests_run}
            </pre>
          )}
        </Card>
      )}

      {!!result?.plan?.steps?.length && (
        <Card title="Plano">
          {result.plan.summary && <Markdown className="mb-2">{result.plan.summary}</Markdown>}
          <ol className="list-decimal space-y-1 pl-5 text-sm">
            {result.plan.steps.map((s, i) => (
              <li key={i}>
                <span className="font-medium text-zinc-100">{s.title}</span>
                {s.details && <span className="text-zinc-400"> — {s.details}</span>}
              </li>
            ))}
          </ol>
        </Card>
      )}

      {showDiff && <DiffViewer initial={result?.diff ?? ''} onClose={() => setShowDiff(false)} />}
    </div>
  )
}
