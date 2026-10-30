import { useEffect, useRef, useState } from 'react'

/** Gaveta com o stderr do processo Go (logs e erros do servidor). */
export function LogDrawer({ logs }: { logs: string[] }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLPreElement>(null)
  useEffect(() => {
    if (open && ref.current) ref.current.scrollTop = ref.current.scrollHeight
  }, [logs, open])

  return (
    <div className="border-t border-zinc-800 bg-zinc-950">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-4 py-1 text-left text-xs text-zinc-500 hover:text-zinc-300"
      >
        <span className={`transition-transform ${open ? 'rotate-90' : ''}`}>▸</span>
        Logs do servidor (stderr)
        <span className="text-zinc-600">{logs.length}</span>
      </button>
      {open && (
        <pre ref={ref} className="h-40 overflow-auto px-4 pb-2 font-mono text-[11px] leading-relaxed text-zinc-400">
          {logs.length ? logs.join('\n') : 'sem logs'}
        </pre>
      )}
    </div>
  )
}
