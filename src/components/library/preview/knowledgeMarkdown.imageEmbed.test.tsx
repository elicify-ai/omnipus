// knowledgeMarkdown.imageEmbed.test.tsx — ADR-083 EMB-030: a size given
// after a bar in a picture embed (`![[photo.png|400]]`) is applied through
// LibraryImagePreview's EXISTING width prop — the same component the
// Library pane uses (EMB-027) — never left unread, and never eaten as the
// picture's own display text.
//
// Separate file, mirroring knowledgeMarkdown.dashboards.test.tsx's own
// reasoning: this needs REAL react-query behaviour (a parent-directory
// listing resolves the full LibraryEntry LibraryImagePreview requires — a
// graph edge carries neither `size`, `modified_at` nor `is_text_editable`),
// so it cannot share knowledgeMarkdown.test.tsx's global useQuery stub.
//
// The trap this file is written against, same as VideoEmbed.test.tsx's own
// header names it: a test asserting "the unsized embed still shows a plain
// picture" passes trivially before ANY width-routing code exists. Every
// width-application assertion below is PAIRED with the identical target,
// unsized, in the SAME describe block — proving the routing rule, not
// merely its absence.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeBaseMarkdown, type EmbedResolution } from './knowledgeMarkdown'
import type { LibraryEntry as GenLibraryEntry } from '@/lib/api/generated/openapi-types'

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
  return { ...actual, fetchLibraryEntries: vi.fn() }
})

import { fetchLibraryEntries } from '@/lib/api'

const WORKSPACE = 'ws-1'
const IMAGE_PATH = 'assets/diagram.png'
const AUDIO_PATH = 'assets/song.mp3'

function entries(): GenLibraryEntry[] {
  return [
    {
      name: 'diagram.png',
      path: IMAGE_PATH,
      is_dir: false,
      is_hidden: false,
      size: 2048,
      modified_at: '2026-08-22T10:15:00Z',
      is_text_editable: false,
    },
    {
      name: 'song.mp3',
      path: AUDIO_PATH,
      is_dir: false,
      is_hidden: false,
      size: 4096,
      modified_at: '2026-08-22T10:15:00Z',
      is_text_editable: false,
    },
  ]
}

function resolverFor(path: string): () => EmbedResolution {
  return () => ({
    state: 'resolved',
    path,
    // Absolute — `MarkdownImage`'s `isSafeHref` gate (chat's own, shared,
    // untouched — see the header of chat/markdown-shared.tsx) rejects a
    // schemeless relative URL outright, same as every other passing fixture
    // in this suite for a resolved image kind (knowledgeMarkdown.test.tsx's
    // own `https://example.test/diagram.png`). The SIZED path below never
    // reads this field at all — KbImageEmbedContent builds its own src from
    // a real fetched LibraryEntry, exactly what this file is proving works.
    url: `https://example.test/${path.split('/').pop()}`,
    workspaceId: WORKSPACE,
    workspacePath: path,
  })
}

function renderNote(content: string, resolveEmbedUrl: () => EmbedResolution) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <KnowledgeBaseMarkdown content={content} notePath="notes/Note.md" resolveEmbedUrl={resolveEmbedUrl} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchLibraryEntries).mockResolvedValue(entries())
})

describe('a standalone, sized picture embed applies its width through LibraryImagePreview (EMB-030)', () => {
  it('mounts the pane’s own image renderer with the written width, and the caption is the filename — never the digits', async () => {
    renderNote('![[diagram.png|400]]', resolverFor(IMAGE_PATH))

    await waitFor(() => expect(screen.getByTestId('library-image-preview')).toBeInTheDocument())
    const img = screen.getByRole('img')
    expect(img).toHaveAttribute('width', '400')
    expect(img).toHaveAttribute('alt', 'diagram.png')
    expect(img.getAttribute('alt')).not.toContain('400')
    // The plain-<img> path (ChatImage, chat's own untouched renderer) was
    // NOT used for this sized embed.
    expect(screen.queryByTestId('chat-image')).not.toBeInTheDocument()
  })

  it('reads a digitsxdigits bar segment and applies only the leading width', async () => {
    renderNote('![[diagram.png|320x240]]', resolverFor(IMAGE_PATH))
    await waitFor(() => expect(screen.getByTestId('library-image-preview')).toBeInTheDocument())
    expect(screen.getByRole('img')).toHaveAttribute('width', '320')
  })

  // The paired negative control (test-integrity discipline): the IDENTICAL
  // target, unsized, must still go through the ORIGINAL plain-<img> path —
  // proving the width-routing branch is read from the notation, not merely
  // "every resolved picture embed now mounts LibraryImagePreview".
  it('an UNSIZED embed of the same file keeps the original plain-image treatment, not LibraryImagePreview', async () => {
    renderNote('![[diagram.png]]', resolverFor(IMAGE_PATH))
    await waitFor(() => expect(screen.getByTestId('chat-image')).toBeInTheDocument())
    expect(screen.queryByTestId('library-image-preview')).not.toBeInTheDocument()
    // A directory listing is never fetched at all for the unsized path —
    // LibraryImagePreview's real-LibraryEntry lookup never runs.
    expect(vi.mocked(fetchLibraryEntries)).not.toHaveBeenCalled()
  })
})

describe('a sized picture embed mixed inline with other text keeps the plain-image treatment (block-promotion gate, EMB-030)', () => {
  it('does not mount LibraryImagePreview when other words share its paragraph — the width is inert there, never a downgrade to a link', async () => {
    renderNote('See ![[diagram.png|400]] for the diagram.', resolverFor(IMAGE_PATH))
    await waitFor(() => expect(screen.getByTestId('chat-image')).toBeInTheDocument())
    expect(screen.queryByTestId('library-image-preview')).not.toBeInTheDocument()
    // Crucially: still the PICTURE, never the "embed shown as a link"
    // fallback treatment a non-image embed kind would get.
    expect(screen.queryByText('embed shown as a link')).not.toBeInTheDocument()
  })
})

describe('a size on a NON-PICTURE embed is ignored at read time, without eating the display text (EMB-030)', () => {
  it('shows the audio file’s own name as the fallback link text, never "400"', async () => {
    renderNote('![[song.mp3|400]]', resolverFor(AUDIO_PATH))
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    const text = screen.getByTestId('markdown-link').textContent ?? ''
    expect(text).toContain('song.mp3')
    expect(text).not.toContain('400')
  })
})
