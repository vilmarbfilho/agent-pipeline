import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron'
import { IPC, type OrchestratorAPI } from '@shared/protocol'

// Expõe apenas funções específicas — nunca o ipcRenderer inteiro.
function subscribe<T>(channel: string, cb: (v: T) => void): () => void {
  const listener = (_e: IpcRendererEvent, v: T) => cb(v)
  ipcRenderer.on(channel, listener)
  return () => ipcRenderer.removeListener(channel, listener)
}

const api: OrchestratorAPI = {
  selectRepo: () => ipcRenderer.invoke(IPC.selectRepo),
  defaultBinary: () => ipcRenderer.invoke(IPC.defaultBinary),
  sidecar: {
    start: (opts) => ipcRenderer.invoke(IPC.sidecarStart, opts),
    stop: () => ipcRenderer.invoke(IPC.sidecarStop),
    status: () => ipcRenderer.invoke(IPC.sidecarStatus)
  },
  run: (task) => ipcRenderer.invoke(IPC.runStart, task),
  cancel: (runId) => ipcRenderer.invoke(IPC.runCancel, runId),
  respondApproval: (id, approved) => ipcRenderer.invoke(IPC.approvalRespond, id, approved),
  gitDiff: () => ipcRenderer.invoke(IPC.gitDiff),
  copyText: (text) => ipcRenderer.invoke(IPC.copyText, text),
  onNotification: (cb) => subscribe(IPC.onNotification, cb),
  onStatus: (cb) => subscribe(IPC.onStatus, cb),
  onLog: (cb) => subscribe(IPC.onLog, cb)
}

contextBridge.exposeInMainWorld('orchestrator', api)
