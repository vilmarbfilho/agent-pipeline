import { app, BrowserWindow, clipboard, dialog, ipcMain, shell, type IpcMainInvokeEvent } from 'electron'
import { execFile } from 'node:child_process'
import { join, resolve } from 'node:path'
import { IPC, type SidecarOptions } from '@shared/protocol'
import { Sidecar } from './sidecar'

// Binário padrão: ORCH_BIN, ou ../bin/orchestrator relativo a desktop-ui/.
const DEFAULT_BINARY = process.env.ORCH_BIN || resolve(app.getAppPath(), '..', 'bin', 'orchestrator')

const sidecar = new Sidecar(DEFAULT_BINARY)
let win: BrowserWindow | null = null

function createWindow(): void {
  win = new BrowserWindow({
    width: 1400,
    height: 900,
    minWidth: 1000,
    minHeight: 640,
    backgroundColor: '#0b0d12',
    title: 'Orchestrator',
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true
    }
  })

  // Nenhuma navegação nem janela nova dentro do app; links http(s) vão para o navegador.
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//.test(url)) void shell.openExternal(url)
    return { action: 'deny' }
  })
  win.webContents.on('will-navigate', (e) => e.preventDefault())

  if (process.env.ELECTRON_RENDERER_URL) {
    void win.loadURL(process.env.ELECTRON_RENDERER_URL)
  } else {
    void win.loadFile(join(__dirname, '../renderer/index.html'))
  }
  win.on('closed', () => (win = null))
}

/** Só aceita IPC vindo da janela principal (frame de topo). */
function assertSender(e: IpcMainInvokeEvent): void {
  if (!win || e.sender !== win.webContents || e.senderFrame !== win.webContents.mainFrame) {
    throw new Error('remetente IPC não autorizado')
  }
}

function handle<A extends unknown[], R>(channel: string, fn: (...args: A) => R | Promise<R>): void {
  ipcMain.handle(channel, (e, ...args) => {
    assertSender(e)
    return fn(...(args as A))
  })
}

function str(v: unknown, name: string, max = 100_000): string {
  if (typeof v !== 'string' || v.length === 0 || v.length > max) throw new Error(`${name} inválido`)
  return v
}

function registerIpc(): void {
  handle(IPC.selectRepo, async () => {
    const r = await dialog.showOpenDialog(win!, {
      title: 'Selecione o repositório Git',
      properties: ['openDirectory']
    })
    return r.canceled ? null : r.filePaths[0]
  })
  handle(IPC.defaultBinary, () => DEFAULT_BINARY)

  handle(IPC.sidecarStart, (opts: SidecarOptions) => {
    if (!opts || typeof opts !== 'object') throw new Error('opções inválidas')
    return sidecar.start({
      repo: str(opts.repo, 'repo', 4096),
      binary: typeof opts.binary === 'string' ? opts.binary : undefined,
      provider: typeof opts.provider === 'string' ? opts.provider : undefined,
      model: typeof opts.model === 'string' ? opts.model : undefined,
      confirmWrites: opts.confirmWrites === true
    })
  })
  handle(IPC.sidecarStop, () => sidecar.stop())
  handle(IPC.sidecarStatus, () => sidecar.status)

  handle(IPC.runStart, (task: unknown) => sidecar.request('run', { task: str(task, 'task') }))
  handle(IPC.runCancel, (runId: unknown) => sidecar.request('cancel', { run_id: str(runId, 'run_id', 128) }))
  handle(IPC.approvalRespond, (id: unknown, approved: unknown) => {
    if (typeof approved !== 'boolean') throw new Error('approved inválido')
    return sidecar.request('approval.respond', { id: str(id, 'id', 128), approved })
  })

  // Diff do working tree contra HEAD. Arquivos não rastreados só são listados:
  // não usamos `git add -N` para não mexer no índice do usuário.
  handle(IPC.gitDiff, () => {
    const repo = sidecar.status.repo
    if (!repo) throw new Error('nenhum repositório conectado')
    const git = (args: string[]) =>
      new Promise<string>((ok, fail) =>
        execFile('git', args, { cwd: repo, maxBuffer: 32 * 1024 * 1024 }, (err, stdout) => (err ? fail(err) : ok(stdout)))
      )
    return Promise.all([git(['diff', 'HEAD', '--no-color']), git(['ls-files', '--others', '--exclude-standard'])]).then(
      ([diff, untracked]) => {
        const extra = untracked.trim()
        return extra ? `${diff}\n# Arquivos não rastreados:\n${extra.split('\n').map((f) => `#   ${f}`).join('\n')}\n` : diff
      }
    )
  })
  handle(IPC.copyText, (text: unknown) => {
    clipboard.writeText(str(text, 'texto', 1_000_000))
  })

  sidecar.on('notification', (n) => win?.webContents.send(IPC.onNotification, n))
  sidecar.on('status', (s) => win?.webContents.send(IPC.onStatus, s))
  sidecar.on('log', (l) => win?.webContents.send(IPC.onLog, l))
}

app.whenReady().then(() => {
  registerIpc()
  createWindow()
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

// Garante que o sidecar não fique órfão.
let quitting = false
app.on('before-quit', (e) => {
  if (quitting || sidecar.status.state === 'stopped' || sidecar.status.state === 'crashed') return
  e.preventDefault()
  quitting = true
  void sidecar.stop().finally(() => app.quit())
})
