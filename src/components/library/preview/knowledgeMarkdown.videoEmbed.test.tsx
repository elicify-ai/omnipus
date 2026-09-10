// knowledgeMarkdown.videoEmbed.test.tsx — ADR-083 US-9/B11/EMB-075: an
// allow-listed video named by a markdown LINK or IMAGE destination is
// recognised before the graph and mounts VideoEmbed; a wikilink is never
// recognised this way (EMB-075: "recognised only from a markdown-link
// form").
//
// Separate file from knowledgeMarkdown.test.tsx for the reason that file's
// own header names: it globally stubs `@tanstack/react-query`'s useQuery to
// `{ data: null }`, and VideoEmbed needs REAL query behaviour — it reads
// the live `['app-state']` cache VideoEmbed.test.tsx exercises directly.
//
// The trap named in VideoEmbed.test.tsx's own header applies here too: a
// test asserting "no VideoEmbed for a wikilink" passes trivially before any
// dispatch code exists. Every wikilink-negative assertion below is PAIRED,
// in the SAME fixture, with a positive assertion that a scheme-bearing
// destination of the SAME shape (link or image) DOES mount VideoEmbed — so
// the pair proves the routing rule, not merely its absence.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeBaseMarkdown, type EmbedResolution } from './knowledgeMarkdown'

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

const ALLOWED_HOST = 'www.youtube-nocookie.com'
const VIDEO_ID = 'dQw4w9WgXcQ' // 11 chars — the spec's own F1 fixture
const VIDEO_URL = `https://${ALLOWED_HOST}/embed/${VIDEO_ID}`

function appState(hosts: string[]): AppState {
  return { onboarding_complete: true, video_embed_hosts: hosts }
}

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
  vi.mocked(fetchAppState).mockResolvedValue(appState([ALLOWED_HOST]))
})

describe('a markdown LINK naming a video is recognised before the graph (B11), a wikilink of the same shape never is (EMB-075)', () => {
  it('mounts VideoEmbed for the scheme-bearing link, and leaves a same-shaped wikilink alone — the pair proves the routing rule', async () => {
    renderNote(`[Watch it](${VIDEO_URL}) and also [[${VIDEO_ID}]]`)

    // Positive half: the scheme-bearing markdown link mounted the facade —
    // waited all the way to the play control, not merely the outer
    // container (which also exists, unhelpfully, in the "loading" state).
    expect(await screen.findByTestId('video-embed-play')).toBeInTheDocument()

    // Negative half, same fixture: a WIKILINK whose text happens to be a
    // valid 11-character video identifier renders through the ordinary
    // wikilink path — never VideoEmbed. Exactly one video-embed panel
    // exists on the whole page.
    const wikilink = screen.getByTestId('markdown-link')
    expect(wikilink).toHaveAttribute('data-kb-link', 'wikilink')
    expect(wikilink.textContent).toContain(VIDEO_ID)
    expect(screen.getAllByTestId('video-embed')).toHaveLength(1)
  })
})

describe('a markdown IMAGE naming a video is recognised the same way, and a wikilink IMAGE embed never is (EMB-075)', () => {
  it('mounts VideoEmbed for the scheme-bearing image, and renders a resolved wikilink image embed as a picture, not as VideoEmbed — the pair proves the routing rule', async () => {
    renderNote(`![Demo](${VIDEO_URL})\n\n![[internal.png]]`, {
      resolveEmbedUrl: (target) =>
        target === 'internal.png'
          ? { state: 'resolved', url: 'https://example.test/internal.png' }
          : { state: 'unresolved' },
    })

    // Positive half: the scheme-bearing IMAGE destination mounted the
    // facade — waited to the play control itself, same reasoning as above.
    expect(await screen.findByTestId('video-embed-play')).toBeInTheDocument()

    // Negative half, same fixture: the wikilink IMAGE embed — resolved to a
    // real, same-origin file — renders as a picture through the inherited
    // image renderer, never as VideoEmbed.
    const picture = screen.getByTestId('chat-image')
    expect(picture).toHaveAttribute('src', 'https://example.test/internal.png')
    expect(screen.getAllByTestId('video-embed')).toHaveLength(1)
  })
})

describe('a video destination degrades honestly when it cannot play, rather than silently becoming a plain link', () => {
  it('shows the refusal text naming the disallowed host for a video-shaped link on a host that is not on the allow-list', async () => {
    renderNote(`[Sketchy](https://evil.example.com/embed/${VIDEO_ID})`)
    await waitFor(() => expect(screen.getByTestId('video-embed')).toHaveAttribute('data-state', 'refused'))
  })
})

describe('an ordinary external link — not video-shaped — is completely unaffected', () => {
  it('renders a plain external link as before, never VideoEmbed', () => {
    renderNote('[Docs](https://example.test/guide)')
    expect(screen.queryByTestId('video-embed')).toBeNull()
    expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('href', 'https://example.test/guide')
  })
})
