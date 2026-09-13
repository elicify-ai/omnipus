// knowledgeMarkdown.uatReader.test.tsx — UAT 2026-09-13 D-111 (a deliberately
// downgraded embed is not labelled "unresolved link") and D-41 (a plain
// link to a refused video host stays the link the author wrote).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  KnowledgeBaseMarkdown,
  NESTED_TRANSCLUSION_EMBED_REASON,
  UnresolvedLink,
  type EmbedResolution,
} from './knowledgeMarkdown'

vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))
vi.mock('@/components/chat/ChatImage', () => ({
  ChatImage: ({ src, alt }: { src: string; alt?: string }) => <img data-testid="chat-image" src={src} alt={alt} />,
}))
vi.mock('@/store/ui', () => ({ useUiStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAppState: vi.fn() }
})

import { fetchAppState, type AppState } from '@/lib/api'

function renderNote(content: string, opts: { resolveEmbedUrl?: (target: string) => EmbedResolution } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <KnowledgeBaseMarkdown content={content} resolveEmbedUrl={opts.resolveEmbedUrl} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchAppState).mockReset()
  const state: AppState = { onboarding_complete: true, video_embed_hosts: ['www.youtube-nocookie.com'] }
  vi.mocked(fetchAppState).mockResolvedValue(state)
})

describe('UAT D-111 — the downgrade prefix is not "unresolved link"', () => {
  it('UnresolvedLink accepts a label and keeps "unresolved link" as the default', () => {
    const { rerender } = render(<UnresolvedLink detail="x">a</UnresolvedLink>)
    expect(screen.getByTestId('markdown-link')).toHaveAttribute('data-kb-unresolved-label', 'unresolved link')
    rerender(
      <UnresolvedLink detail="x" label="embed shown as a link">
        a
      </UnresolvedLink>,
    )
    expect(screen.getByTestId('markdown-link')).toHaveAttribute('data-kb-unresolved-label', 'embed shown as a link')
    expect(screen.getByTestId('markdown-link').textContent).toContain('(embed shown as a link: x)')
  })

  it('labels the nested-transclusion downgrade "embed shown as a link", and a real miss "unresolved link"', async () => {
    renderNote('![[Inner]]\n\n![[Missing]]', {
      resolveEmbedUrl: (target) =>
        target === 'Inner'
          ? { state: 'unresolved', reason: NESTED_TRANSCLUSION_EMBED_REASON }
          : { state: 'unresolved', reason: `no file in this collection matches "${target}"` },
    })
    const links = await screen.findAllByTestId('markdown-link')
    const labels = links.map((l) => l.getAttribute('data-kb-unresolved-label'))
    // DIES ON the old code: both carried "unresolved link".
    expect(labels).toContain('embed shown as a link')
    expect(labels).toContain('unresolved link')
  })
})

describe('UAT D-41 — a plain link to a refused video host stays a link', () => {
  it('renders the link the author wrote, with the refusal as a note — not a refused-video box', async () => {
    renderNote('[Watch on YouTube](https://www.youtube.com/watch?v=dQw4w9WgXcQ)')
    const link = await screen.findByTestId('video-embed-refused-link')
    // DIES ON the old code: a red `video-embed` box with data-state="refused"
    // replaced the link entirely.
    expect(link).toHaveAttribute('href', 'https://www.youtube.com/watch?v=dQw4w9WgXcQ')
    expect(link.textContent).toBe('Watch on YouTube')
    expect(screen.getByTestId('video-embed')).toHaveAttribute('data-state', 'refused-link')
    expect(screen.getByTestId('video-embed').textContent).toMatch(/www\.youtube\.com/)
  })

  it('still shows the refusal box for an EMBED-authored video on a refused host', async () => {
    renderNote('![Demo](https://www.youtube.com/watch?v=dQw4w9WgXcQ)')
    await waitFor(() => expect(screen.getByTestId('video-embed')).toHaveAttribute('data-state', 'refused'))
    expect(screen.queryByTestId('video-embed-refused-link')).toBeNull()
  })
})
