import { resolve } from 'node:path'
import { defineConfig, externalizeDepsPlugin } from 'electron-vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import type { Plugin } from 'vite'

// O React Refresh injeta um <script> inline e o HMR usa WebSocket: só em dev
// a CSP é relaxada para isso. O build de produção mantém a CSP estrita.
const devCsp = (): Plugin => ({
  name: 'dev-csp',
  apply: 'serve',
  transformIndexHtml: (html) =>
    html
      .replace("script-src 'self'", "script-src 'self' 'unsafe-inline'")
      .replace("connect-src 'self'", "connect-src 'self' ws://localhost:* http://localhost:*")
})

const shared = { '@shared': resolve(__dirname, 'src/shared') }

export default defineConfig({
  main: {
    plugins: [externalizeDepsPlugin()],
    resolve: { alias: shared }
  },
  preload: {
    plugins: [externalizeDepsPlugin()],
    resolve: { alias: shared }
  },
  renderer: {
    root: resolve(__dirname, 'src/renderer'),
    resolve: { alias: shared },
    plugins: [react(), tailwindcss(), devCsp()]
  }
})
