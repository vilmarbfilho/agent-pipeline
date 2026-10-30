import type { OrchestratorAPI } from '../shared/protocol'

declare global {
  interface Window {
    orchestrator: OrchestratorAPI
  }
}

export {}
