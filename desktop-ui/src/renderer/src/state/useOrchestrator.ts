import { useCallback, useEffect, useReducer, useRef, useState } from 'react'
import type { ServerNotification, SidecarOptions, SidecarStatus } from '@shared/protocol'
import { initialRun, isRunActive, runReducer } from './run'

const api = window.orchestrator
const MAX_LOG_LINES = 500

/**
 * Liga o renderer ao sidecar: status do processo, logs do stderr e o estado
 * da execução atual. Notificações são agrupadas por frame para que o
 * streaming token a token não provoque um render por delta.
 */
export function useOrchestrator() {
  const [run, dispatch] = useReducer(runReducer, initialRun)
  const [sidecar, setSidecar] = useState<SidecarStatus>({ state: 'stopped' })
  const [logs, setLogs] = useState<string[]>([])
  const queue = useRef<ServerNotification[]>([])
  const frame = useRef<number | null>(null)

  useEffect(() => {
    void api.sidecar.status().then(setSidecar)
    const offStatus = api.onStatus((s) => {
      setSidecar(s)
      if (s.state === 'crashed' || s.state === 'stopped') {
        dispatch({ type: 'sidecar_lost', error: s.message ?? 'o servidor do orquestrador foi encerrado' })
      }
    })
    const offLog = api.onLog((line) => setLogs((l) => [...l.slice(-(MAX_LOG_LINES - 1)), line]))
    const offNotif = api.onNotification((n) => {
      queue.current.push(n)
      if (frame.current === null) {
        frame.current = requestAnimationFrame(() => {
          frame.current = null
          const batch = queue.current
          queue.current = []
          dispatch({ type: 'notifications', batch })
        })
      }
    })
    return () => {
      offStatus()
      offLog()
      offNotif()
      if (frame.current !== null) cancelAnimationFrame(frame.current)
    }
  }, [])

  const connect = useCallback((opts: SidecarOptions) => api.sidecar.start(opts), [])
  const disconnect = useCallback(() => api.sidecar.stop(), [])

  const start = useCallback(async (task: string) => {
    dispatch({ type: 'start', task })
    try {
      const { run_id } = await api.run(task)
      dispatch({ type: 'started', runId: run_id })
    } catch (err) {
      dispatch({ type: 'start_failed', error: (err as Error).message })
    }
  }, [])

  const cancel = useCallback(async () => {
    if (!run.runId) return
    dispatch({ type: 'cancelling' })
    await api.cancel(run.runId)
  }, [run.runId])

  const respond = useCallback(async (id: string, approved: boolean) => {
    // Só remove da fila depois que o servidor recebeu a resposta.
    await api.respondApproval(id, approved)
    dispatch({ type: 'approval_answered', id })
  }, [])

  const reset = useCallback(() => dispatch({ type: 'reset' }), [])

  return { run, active: isRunActive(run), sidecar, logs, connect, disconnect, start, cancel, respond, reset }
}
