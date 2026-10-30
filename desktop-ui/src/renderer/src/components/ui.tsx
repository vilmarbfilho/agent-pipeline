import type { ComponentProps, ReactNode } from 'react'

type Variant = 'primary' | 'danger' | 'success' | 'ghost'

const variants: Record<Variant, string> = {
  primary: 'bg-sky-600 text-white hover:bg-sky-500 disabled:bg-sky-900/60',
  danger: 'bg-rose-600 text-white hover:bg-rose-500 disabled:bg-rose-900/60',
  success: 'bg-emerald-600 text-white hover:bg-emerald-500 disabled:bg-emerald-900/60',
  ghost: 'bg-zinc-800/70 text-zinc-200 hover:bg-zinc-700 disabled:text-zinc-500'
}

export function Button({ variant = 'ghost', className = '', ...props }: ComponentProps<'button'> & { variant?: Variant }) {
  return (
    <button
      type="button"
      {...props}
      className={`inline-flex items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors disabled:cursor-not-allowed focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sky-500 ${variants[variant]} ${className}`}
    />
  )
}

const tones = {
  zinc: 'bg-zinc-800 text-zinc-300 ring-zinc-700',
  sky: 'bg-sky-950 text-sky-300 ring-sky-800',
  emerald: 'bg-emerald-950 text-emerald-300 ring-emerald-800',
  rose: 'bg-rose-950 text-rose-300 ring-rose-800',
  amber: 'bg-amber-950 text-amber-300 ring-amber-800',
  violet: 'bg-violet-950 text-violet-300 ring-violet-800'
}
export type Tone = keyof typeof tones

export function Badge({ tone = 'zinc', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium ring-1 ring-inset ${tones[tone]}`}>{children}</span>
}

export function Spinner({ className = '' }: { className?: string }) {
  return <span className={`inline-block size-3 animate-spin rounded-full border-2 border-current border-t-transparent ${className}`} />
}

export function Panel({ title, actions, children, className = '' }: { title: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`flex min-h-0 flex-col rounded-lg border border-zinc-800 bg-zinc-900/50 ${className}`}>
      <header className="flex items-center justify-between gap-2 border-b border-zinc-800 px-3 py-2">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-400">{title}</h2>
        {actions}
      </header>
      {children}
    </section>
  )
}

export const phaseLabel: Record<string, string> = { planner: 'Planner', coder: 'Coder', reviewer: 'Reviewer' }
