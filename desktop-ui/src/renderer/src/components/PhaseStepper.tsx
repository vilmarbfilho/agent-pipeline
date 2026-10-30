import { Fragment } from 'react'
import { PHASES, type PhaseName } from '@shared/protocol'
import type { PhaseStatus } from '../state/run'
import { phaseLabel, Spinner } from './ui'

const style: Record<PhaseStatus, { ring: string; icon: string; text: string; sub: string }> = {
  pending: { ring: 'border-zinc-700 text-zinc-500', icon: '', text: 'text-zinc-500', sub: 'aguardando' },
  active: { ring: 'border-sky-500 text-sky-300 shadow-[0_0_0_4px] shadow-sky-500/15', icon: '', text: 'text-sky-200', sub: 'em execução' },
  done: { ring: 'border-emerald-500 bg-emerald-500/15 text-emerald-300', icon: '✓', text: 'text-emerald-200', sub: 'concluído' },
  failed: { ring: 'border-rose-500 bg-rose-500/15 text-rose-300', icon: '✕', text: 'text-rose-200', sub: 'falhou' }
}

const description: Record<PhaseName, string> = {
  planner: 'planeja (somente leitura)',
  coder: 'implementa e testa',
  reviewer: 'revisa e aprova'
}

export function PhaseStepper({ phases, round }: { phases: Record<PhaseName, PhaseStatus>; round: number }) {
  return (
    <ol className="flex items-center gap-3" aria-label="Fases do pipeline">
      {PHASES.map((p, i) => {
        const st = style[phases[p]]
        return (
          <Fragment key={p}>
            {i > 0 && (
              <li aria-hidden className={`h-px flex-1 ${phases[p] === 'pending' ? 'bg-zinc-800' : 'bg-gradient-to-r from-emerald-600 to-sky-600'}`} />
            )}
            <li className="flex items-center gap-2.5" aria-current={phases[p] === 'active' ? 'step' : undefined}>
              <span className={`grid size-8 place-items-center rounded-full border-2 text-sm font-bold transition-all ${st.ring}`}>
                {phases[p] === 'active' ? <Spinner className="size-3.5" /> : st.icon || i + 1}
              </span>
              <span className="leading-tight">
                <span className={`block text-sm font-semibold ${st.text}`}>
                  {phaseLabel[p]}
                  {p !== 'planner' && round > 1 && <span className="ml-1.5 text-xs font-normal text-amber-300">rodada {round}</span>}
                </span>
                <span className="block text-xs text-zinc-500">
                  {st.sub} · {description[p]}
                </span>
              </span>
            </li>
          </Fragment>
        )
      })}
    </ol>
  )
}
