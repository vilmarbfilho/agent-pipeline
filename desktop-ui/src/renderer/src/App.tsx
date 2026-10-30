import { useEffect, useState } from 'react'
import { ApprovalDialog } from './components/ApprovalDialog'
import { ConnectionPanel } from './components/ConnectionPanel'
import { LogDrawer } from './components/LogDrawer'
import { MessageStream } from './components/MessageStream'
import { PhaseStepper } from './components/PhaseStepper'
import { ResultPanel } from './components/ResultPanel'
import { TaskInput } from './components/TaskInput'
import { ToolTimeline } from './components/ToolTimeline'
import { Panel } from './components/ui'
import { useOrchestrator } from './state/useOrchestrator'

type Tab = 'stream' | 'result'

export default function App() {
  const o = useOrchestrator()
  const { run } = o
  const connected = o.sidecar.state === 'running'
  const finished = ['succeeded', 'rejected', 'failed', 'cancelled'].includes(run.status)
  const [tab, setTab] = useState<Tab>('stream')

  // Ao terminar, mostra o resultado; ao iniciar outra execução, volta ao streaming.
  useEffect(() => setTab(finished ? 'result' : 'stream'), [finished])

  const tabBtn = (t: Tab, label: string, enabled = true) => (
    <button
      type="button"
      disabled={!enabled}
      onClick={() => setTab(t)}
      className={`rounded px-2 py-0.5 text-xs font-medium ${tab === t ? 'bg-zinc-700 text-zinc-100' : 'text-zinc-400 hover:text-zinc-200 disabled:text-zinc-700'}`}
    >
      {label}
    </button>
  )

  return (
    <div className="flex h-full flex-col">
      <header className="space-y-3 border-b border-zinc-800 bg-zinc-900/60 px-4 py-3">
        <div className="flex items-center gap-3">
          <h1 className="text-sm font-bold tracking-wide">
            <span className="text-sky-400">◆</span> Orchestrator
          </h1>
          <div className="flex-1">
            <ConnectionPanel status={o.sidecar} busy={o.active} onConnect={o.connect} onDisconnect={o.disconnect} />
          </div>
        </div>
        <TaskInput
          connected={connected}
          active={o.active}
          cancelling={run.status === 'cancelling'}
          onStart={o.start}
          onCancel={o.cancel}
        />
        <PhaseStepper phases={run.phases} round={run.round} />
      </header>

      <main className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(320px,30%)] gap-3 p-3">
        <Panel
          title={
            <span className="flex items-center gap-1 normal-case tracking-normal">
              {tabBtn('stream', 'Execução')}
              {tabBtn('result', 'Resultado', finished)}
            </span>
          }
          actions={run.runId && <span className="font-mono text-[11px] text-zinc-600">{run.runId}</span>}
        >
          {tab === 'result' && finished ? (
            <ResultPanel status={run.status} result={run.result} error={run.error} usage={run.usage} onNewTask={o.reset} />
          ) : (
            <MessageStream messages={run.messages} active={o.active} task={run.task} />
          )}
        </Panel>

        <Panel title={`Ferramentas (${run.tools.length})`}>
          <ToolTimeline tools={run.tools} />
        </Panel>
      </main>

      <LogDrawer logs={o.logs} />
      <ApprovalDialog queue={run.approvals} onRespond={o.respond} />
    </div>
  )
}
