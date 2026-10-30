import { useState, type KeyboardEvent } from 'react'
import { Button, Spinner } from './ui'

interface Props {
  connected: boolean
  active: boolean
  cancelling: boolean
  onStart(task: string): void
  onCancel(): void
}

export function TaskInput({ connected, active, cancelling, onStart, onCancel }: Props) {
  const [task, setTask] = useState('')
  const canStart = connected && !active && task.trim().length > 0

  function submit() {
    if (canStart) onStart(task.trim())
  }
  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      submit()
    }
  }

  return (
    <div className="flex items-end gap-2">
      <textarea
        className="min-h-[3.25rem] flex-1 resize-y rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm placeholder:text-zinc-600 focus:border-sky-700 focus:outline-none disabled:opacity-60"
        rows={2}
        placeholder={connected ? 'Descreva a tarefa… ex.: "adicione testes unitários para a função Parse"  (⌘/Ctrl+Enter)' : 'Conecte-se a um repositório para começar'}
        value={task}
        disabled={!connected || active}
        onChange={(e) => setTask(e.target.value)}
        onKeyDown={onKeyDown}
      />
      {active ? (
        <Button variant="danger" onClick={onCancel} disabled={cancelling} className="h-[3.25rem] w-28">
          {cancelling ? <><Spinner /> Cancelando</> : '■ Cancelar'}
        </Button>
      ) : (
        <Button variant="primary" onClick={submit} disabled={!canStart} className="h-[3.25rem] w-28">
          ▶ Iniciar
        </Button>
      )}
    </div>
  )
}
