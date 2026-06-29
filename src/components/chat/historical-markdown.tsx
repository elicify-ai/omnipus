// Renderer for already-finalized assistant messages in the virtualized list.
// Shares every element renderer with the live AssistantUI renderer (markdown-text.tsx)
// via markdown-shared.tsx — the two now differ ONLY in their block-code path: this one
// renders a plain <pre><code> (Shiki is live-only, to keep this bundle light), the live
// one uses Shiki. Both route a ```mermaid fence to the shared <MermaidDiagram>.

import { useQuery } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import 'katex/dist/katex.min.css'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
import { resolveEffectivePreview } from '@/lib/preview-url'
import { fetchAboutInfo } from '@/lib/api'
import {
  PhosphorEmojiSpan,
  MarkdownImage,
  createLinkRenderer,
  InlineCode,
  classifyFence,
  MermaidDiagram,
  commonMarkdownComponents,
} from './markdown-shared'

export function HistoricalMessageMarkdown({ content }: { content: string }) {
  const { data: aboutInfo } = useQuery({ queryKey: ['about'], queryFn: fetchAboutInfo, staleTime: 5 * 60 * 1000 })
  const effectivePreview = resolveEffectivePreview(
    aboutInfo ?? null,
    typeof window !== 'undefined' ? window.location.hostname : '',
  )

  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm, remarkMath]}
      rehypePlugins={[rehypeKatex, rehypePhosphorEmoji]}
      components={{
        // Shared element renderers (paragraphs, headings, lists, strong/em,
        // blockquote, tables, hr) — single source of truth in markdown-shared.tsx.
        ...commonMarkdownComponents,

        // `pre` is a pass-through: the `code` renderer below emits its OWN block
        // wrapper (or a MermaidDiagram), so the default <pre> would double-wrap it
        // (the nested <pre><pre> seen in the DOM). Let `code` own the block layout.
        pre: ({ children }) => <>{children}</>,

        // Block code is a plain <pre><code>; a `language-mermaid` fence routes to the
        // shared diagram (parity with the live renderer). classifyFence centralises
        // the block/inline + language detection — block-ness must NOT key solely on a
        // `language-` class, or bare/indented fences collapse (see regression test).
        code: ({ children, className }) => {
          const { isBlock, language, text } = classifyFence(children, className)
          if (!isBlock) return <InlineCode>{children}</InlineCode>
          if (language === 'mermaid') {
            // Fenced content carries a trailing newline; trim so mermaid parses cleanly.
            return <MermaidDiagram code={text.replace(/\n$/, '')} />
          }
          return (
            <pre className="text-xs bg-[var(--color-surface-1)] rounded p-2 overflow-auto my-2 font-mono text-[var(--color-secondary)]">
              <code className={className}>{children}</code>
            </pre>
          )
        },

        // Phosphor-emoji spans (from rehypePhosphorEmoji), lightbox images, and links
        // (legacy-host rewrite + scheme allow-list) — all shared.
        span: PhosphorEmojiSpan,
        img: MarkdownImage,
        a: createLinkRenderer(effectivePreview),
      }}
    >
      {content}
    </ReactMarkdown>
  )
}
