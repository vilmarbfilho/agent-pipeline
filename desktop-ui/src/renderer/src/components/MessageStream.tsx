import { Fragment, useEffect, useRef, useState } from 'react'
import type { MessageBlock } from '../state/run'
import { Markdown } from './Markdown'
import { phaseLabel, Spinner } from './ui'

/** Rola para o fim enquanto o usuário estiver perto do fim (não "puxa" quem rolou para cima). */
function useStickToBottom(dep: unknown) {
  const ref = useRef<HTMLDivElement>(null)
  const stick = useRef(true)
  useEffect(() => {
    const el = ref.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [dep])
  const onScroll = () => {
    const el = ref.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  }
  return { ref, onScroll }
}

function ThinkingBlock({ block }: { block: MessageBlock }) {
  // Aberto enquanto transmite; recolhe sozinho ao terminar (o usuário pode reabrir).
  const [open, setOpen] = useState(true)
  useEffect(() => {
    if (block.closed) setOpen(false)
  }, [block.closed])

  return (
    <div className="rounded-md border border-violet-900/60 bg-violet-950/20">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs font-medium text-violet-300 hover:bg-violet-950/40"
      >
        <span className={`transition-transform ${open ? 'rotate-90' : ''}`}>▸</span>
        💭 Raciocínio
        {!block.closed && <Spinner className="text-violet-400" />}
        {!open && <span className="truncate font-normal text-violet-400/60">{block.content.slice(0, 120)}</span>}
      </button>
      {open && (
        <div className="border-t border-violet-900/40 px-3 py-2 text-violet-100/80 italic">
          <Markdown className="prose-p:my-1">{block.content}</Markdown>
        </div>
      )}
    </div>
  )
}

export function MessageStream({ messages, active, task }: { messages: MessageBlock[]; active: boolean; task: string }) {
  const last = messages[messages.length - 1]
  const { ref, onScroll } = useStickToBottom(last ? `${messages.length}:${last.content.length}` : 0)

  if (!task) {
    return (
      <div className="grid flex-1 place-items-center p-8 text-center text-sm text-zinc-500">
        <div>
          <p className="text-base text-zinc-400">Nenhuma execução ainda.</p>
          <p className="mt-1">Selecione um repositório, conecte e descreva uma tarefa.</p>
        </div>
      </div>
    )
  }

  return (
    <div ref={ref} onScroll={onScroll} className="flex-1 space-y-3 overflow-y-auto p-4">
      <div className="rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm">
        <span className="mr-2 text-xs font-semibold uppercase tracking-wider text-zinc-500">Tarefa</span>
        {task}
      </div>
      {messages.map((m, i) => (
        <Fragment key={m.id}>
          {(i === 0 || messages[i - 1].phase !== m.phase) && (
            <div className="flex items-center gap-2 pt-2 text-xs font-semibold uppercase tracking-wider text-zinc-500">
              <span className="h-px w-4 bg-zinc-700" />
              {phaseLabel[m.phase] ?? m.phase}
              <span className="h-px flex-1 bg-zinc-800" />
            </div>
          )}
          {m.kind === 'thinking' ? (
            <ThinkingBlock block={m} />
          ) : (
            <div className={!m.closed ? 'streaming-caret' : undefined}>
              <Markdown>{m.content}</Markdown>
            </div>
          )}
        </Fragment>
      ))}
      {active && (!last || last.closed) && (
        <div className="flex items-center gap-2 text-xs text-zinc-500">
          <Spinner /> trabalhando…
        </div>
      )}
    </div>
  )
}
