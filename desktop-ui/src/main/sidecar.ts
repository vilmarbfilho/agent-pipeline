import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { EventEmitter } from 'node:events'
import { existsSync, statSync } from 'node:fs'
import { dirname } from 'node:path'
import type { ServerNotification, SidecarOptions, SidecarStatus } from '@shared/protocol'

const REQUEST_TIMEOUT_MS = 30_000
const STOP_GRACE_MS = 4_000
const PROVIDERS = new Set(['anthropic', 'openrouter', 'mock'])

interface Pending {
  resolve: (v: unknown) => void
  reject: (e: Error) => void
  timer: NodeJS.Timeout
}

/**
 * Gerencia o processo `orchestrator serve` e fala JSON-RPC 2.0 com ele
 * (uma mensagem JSON por linha no stdin/stdout; stderr = logs).
 *
 * Eventos: 'notification' (ServerNotification), 'status' (SidecarStatus), 'log' (string).
 */
export class Sidecar extends EventEmitter {
  private proc: ChildProcessWithoutNullStreams | null = null
  private pending = new Map<number, Pending>()
  private nextId = 1
  private stdoutBuf = ''
  private stderrBuf = ''
  private _status: SidecarStatus = { state: 'stopped' }

  private readonly defaultBinary: string

  constructor(defaultBinary: string) {
    super()
    this.defaultBinary = defaultBinary
  }

  get status(): SidecarStatus {
    return this._status
  }

  async start(opts: SidecarOptions): Promise<SidecarStatus> {
    await this.stop()

    const binary = opts.binary?.trim() || this.defaultBinary
    if (!existsSync(binary)) {
      throw new Error(`binário não encontrado: ${binary} (rode \`make build\` na raiz do orquestrador)`)
    }
    if (!existsSync(opts.repo) || !statSync(opts.repo).isDirectory()) {
      throw new Error(`repositório inválido: ${opts.repo}`)
    }

    const args = ['serve', '--repo', opts.repo]
    if (opts.provider) {
      if (!PROVIDERS.has(opts.provider)) throw new Error(`provedor inválido: ${opts.provider}`)
      args.push('--provider', opts.provider)
    }
    if (opts.model?.trim()) args.push('--model', opts.model.trim())
    if (opts.confirmWrites) args.push('--confirm-writes')

    // cwd = raiz do projeto Go (pai de bin/), para que o `.env` padrão seja
    // encontrado — o binário carrega `.env` do diretório atual, não do repo.
    const cwd = dirname(dirname(binary))
    this.setStatus({ state: 'starting', repo: opts.repo })
    const proc = spawn(binary, args, { cwd, env: process.env, stdio: ['pipe', 'pipe', 'pipe'] })
    this.proc = proc

    proc.stdout.setEncoding('utf8')
    proc.stderr.setEncoding('utf8')
    proc.stdout.on('data', (chunk: string) => this.onStdout(chunk))
    proc.stderr.on('data', (chunk: string) => this.onStderr(chunk))
    proc.on('error', (err) => this.onExit(proc, null, null, err.message))
    proc.on('exit', (code, signal) => this.onExit(proc, code, signal))

    // `ping` confirma que o servidor subiu e está lendo o stdin.
    try {
      await this.request('ping', undefined, 10_000)
    } catch (err) {
      await this.stop()
      throw new Error(`o servidor não respondeu ao ping: ${(err as Error).message}${this.lastStderrHint()}`)
    }
    this.setStatus({ state: 'running', repo: opts.repo, pid: proc.pid })
    return this._status
  }

  /** Encerra o processo: fecha o stdin (EOF encerra o Serve e cancela execuções), depois SIGTERM/SIGKILL. */
  async stop(): Promise<SidecarStatus> {
    const proc = this.proc
    if (!proc || proc.exitCode !== null || proc.signalCode !== null) {
      this.proc = null
      if (this._status.state !== 'crashed') this.setStatus({ state: 'stopped' })
      return this._status
    }
    this.setStatus({ ...this._status, state: 'stopping' })
    const exited = new Promise<void>((r) => proc.once('exit', () => r()))
    proc.stdin.end()
    const term = setTimeout(() => proc.kill('SIGTERM'), STOP_GRACE_MS)
    const kill = setTimeout(() => proc.kill('SIGKILL'), STOP_GRACE_MS * 2)
    await exited
    clearTimeout(term)
    clearTimeout(kill)
    return this._status
  }

  request<T = unknown>(method: string, params?: unknown, timeoutMs = REQUEST_TIMEOUT_MS): Promise<T> {
    const proc = this.proc
    if (!proc || !proc.stdin.writable) {
      return Promise.reject(new Error('servidor do orquestrador não está em execução'))
    }
    const id = this.nextId++
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`timeout aguardando resposta de "${method}"`))
      }, timeoutMs)
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject, timer })
      const msg: Record<string, unknown> = { jsonrpc: '2.0', id, method }
      if (params !== undefined) msg.params = params
      proc.stdin.write(JSON.stringify(msg) + '\n')
    })
  }

  private onStdout(chunk: string): void {
    this.stdoutBuf += chunk
    let nl: number
    while ((nl = this.stdoutBuf.indexOf('\n')) >= 0) {
      const line = this.stdoutBuf.slice(0, nl).trim()
      this.stdoutBuf = this.stdoutBuf.slice(nl + 1)
      if (line) this.handleMessage(line)
    }
  }

  private handleMessage(line: string): void {
    let msg: any
    try {
      msg = JSON.parse(line)
    } catch {
      this.emit('log', `[stdout não-JSON] ${line}`)
      return
    }
    if (msg.id !== undefined && msg.id !== null && (msg.result !== undefined || msg.error !== undefined || !msg.method)) {
      const p = this.pending.get(msg.id)
      if (!p) return
      this.pending.delete(msg.id)
      clearTimeout(p.timer)
      if (msg.error) p.reject(new Error(msg.error.message ?? 'erro JSON-RPC'))
      else p.resolve(msg.result)
      return
    }
    if (typeof msg.method === 'string') {
      switch (msg.method) {
        case 'event':
        case 'approval.request':
        case 'run.finished':
          this.emit('notification', { method: msg.method, params: msg.params } as ServerNotification)
          return
        default:
          this.emit('log', `[notificação desconhecida] ${msg.method}`)
      }
    }
  }

  private onStderr(chunk: string): void {
    this.stderrBuf = (this.stderrBuf + chunk).slice(-8_000)
    for (const l of chunk.split('\n')) if (l.trim()) this.emit('log', l)
  }

  private lastStderrHint(): string {
    const tail = this.stderrBuf.trim().split('\n').slice(-3).join(' | ')
    return tail ? ` — stderr: ${tail}` : ''
  }

  private onExit(proc: ChildProcessWithoutNullStreams, code: number | null, signal: NodeJS.Signals | null, errMsg?: string): void {
    if (this.proc !== proc) return
    this.proc = null
    this.stdoutBuf = ''
    for (const [, p] of this.pending) {
      clearTimeout(p.timer)
      p.reject(new Error('o processo do orquestrador terminou'))
    }
    this.pending.clear()
    const expected = this._status.state === 'stopping'
    if (expected) {
      this.setStatus({ state: 'stopped' })
    } else {
      const why = errMsg ?? (signal ? `sinal ${signal}` : `código ${code}`)
      this.setStatus({ state: 'crashed', message: `processo encerrou (${why})${this.lastStderrHint()}` })
    }
  }

  private setStatus(s: SidecarStatus): void {
    this._status = s
    this.emit('status', s)
  }
}
