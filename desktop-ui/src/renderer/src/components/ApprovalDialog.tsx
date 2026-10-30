import { useEffect, useRef, useState } from 'react'
import type { ApprovalRequest } from '@shared/protocol'
import { Badge, Button } from './ui'

interface Props {
  queue: ApprovalRequest[]
  onRespond(id: string, approved: boolean): Promise<void>
}

/**
 * Modal HITL. Bloqueia a UI até o usuário decidir; mostra uma solicitação
 * por vez (as demais ficam na fila). Não há atalho de teclado para aprovar e
 * o foco inicial fica em "Recusar", para evitar aprovação acidental.
 */
export function ApprovalDialog({ queue, onRespond }: Props) {
  const current = queue[0]
  const refuseRef = useRef<HTMLButtonElement>(null)
  const [sending, setSending] = useState(false)

  useEffect(() => {
    if (current) refuseRef.current?.focus()
    setSending(false)
  }, [current?.id])

  if (!current) return null
  const { tool, action, reason, details } = current.request

  async function respond(approved: boolean) {
    setSending(true)
    try {
      await onRespond(current.id, approved)
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/70 p-6 backdrop-blur-sm" role="presentation">
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="approval-title"
        aria-describedby="approval-reason"
        className="flex max-h-full w-full max-w-3xl flex-col overflow-hidden rounded-xl border border-amber-700/70 bg-zinc-900 shadow-2xl shadow-amber-950/40"
      >
        <header className="flex items-center gap-3 border-b border-amber-900/60 bg-amber-950/40 px-5 py-3">
          <span className="text-xl">⚠️</span>
          <div className="flex-1">
            <h2 id="approval-title" className="font-semibold text-amber-200">Aprovação necessária</h2>
            <p className="text-xs text-amber-300/70">O agente quer executar uma ação sensível.</p>
          </div>
          {queue.length > 1 && <Badge tone="amber">+{queue.length - 1} na fila</Badge>}
        </header>

        <div className="space-y-4 overflow-y-auto px-5 py-4 text-sm">
          <dl className="grid grid-cols-[7rem_1fr] gap-x-4 gap-y-2">
            <dt className="text-zinc-500">Ferramenta</dt>
            <dd><Badge tone="amber">{tool}</Badge></dd>
            <dt className="text-zinc-500">Ação</dt>
            <dd className="font-mono text-zinc-100">{action}</dd>
            <dt className="text-zinc-500">Justificativa</dt>
            <dd id="approval-reason" className="text-zinc-300">{reason}</dd>
          </dl>
          {details && (
            <div>
              <div className="mb-1 text-xs font-semibold uppercase tracking-wider text-zinc-500">Detalhes</div>
              <pre className="max-h-[45vh] overflow-auto rounded-md border border-zinc-800 bg-zinc-950 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-zinc-200">
                {details}
              </pre>
            </div>
          )}
        </div>

        <footer className="flex justify-end gap-2 border-t border-zinc-800 bg-zinc-950/50 px-5 py-3">
          <Button ref={refuseRef} variant="danger" disabled={sending} onClick={() => respond(false)} className="min-w-28">
            ✕ Recusar
          </Button>
          <Button variant="success" disabled={sending} onClick={() => respond(true)} className="min-w-28">
            ✓ Aprovar
          </Button>
        </footer>
      </div>
    </div>
  )
}
