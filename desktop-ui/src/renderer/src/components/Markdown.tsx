import { memo } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

// Sem rehype-raw: HTML vindo do modelo é exibido como texto, nunca interpretado.
// Links abrem no navegador externo (o main nega navegação/janelas no app).
export const Markdown = memo(function Markdown({ children, className = '' }: { children: string; className?: string }) {
  return (
    <div className={`prose prose-sm prose-invert max-w-none prose-pre:bg-zinc-950 prose-pre:ring-1 prose-pre:ring-zinc-800 prose-code:before:content-none prose-code:after:content-none ${className}`}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={{ a: ({ node: _n, ...p }) => <a {...p} target="_blank" rel="noreferrer" /> }}>
        {children}
      </ReactMarkdown>
    </div>
  )
})
