// knowledgeMarkdown.step6.test.tsx — ADR-083 Step 6 WIRING (EMB-105, US-12):
// audio, local video, a pdf page fragment, and a ```query fence were built
// and proven standalone (KbAudioEmbedMount.tsx, KbVideoEmbedMount.tsx,
// KbPdfPageEmbedMount.tsx, KbQueryFenceEmbed.tsx) but reachable from NO note
// until this file's dispatch — KINDS_WITH_INLINE_RENDERER,
// isPromotableBlockEmbedNode, KnowledgeMarkdownLink's isEmbed branch, and the
// `code` slot's `language === 'query'` case — actually calls them.
//
// Audio and video mount their REAL, already-proven renderers
// (LibraryAudioPreview/LibraryVideoPreview) through the SAME
// fetchLibraryEntries boundary knowledgeMarkdown.dashboards.test.tsx already
// mocks for `.base` — cheap enough to exercise for real, so a regression in
// the DISPATCH (not just the renderer) fails these tests. A pdf page embed's
// own renderer (LibraryPdfPreview) needs a full pdf.js worker-pool mock
// already built and exhaustively exercised in KbPdfPageEmbedMount.test.tsx —
// duplicating that here would test the renderer a second time, not the
// dispatch this file owns — so KbPdfPageEmbedMount itself is mocked to a
// prop-recording stub, and the assertions are about which props reach it.
// The query-fence mount is real (KbQueryFenceEmbed: a plain useQuery over
// searchVault, no heavier than the audio/video renderers).
//
// Every negative assertion below (inline-mixed, no page fragment, no
// workspace/collection context, nested) is paired with a positive one
// proving the real dispatch fired — the pairing discipline
// knowledgeMarkdown.dashboards.test.tsx and .transclusion.test.tsx already
// use, so a test asserting only absence never passes trivially.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeBaseMarkdown, parseWikilink, type EmbedResolution } from './knowledgeMarkdown'
import type { LibraryEntry } from '@/lib/api'
import type { components } from '@/lib/api/generated/openapi-types'

type VaultSearchResponse = components['schemas']['VaultSearchResponse']

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

// KbPdfPageEmbedMount's own renderer is proven elsewhere (see header) — this
// file mocks it to a stub that records exactly the props the dispatch handed
// it, so the assertions below are about DISPATCH, not pdf.js.
vi.mock('./KbPdfPageEmbedMount', () => ({
  KbPdfPageEmbedMount: (props: { workspaceId: string; workspacePath: string; page: number }) => (
    <div
      data-testid="kb-pdf-page-embed-mount-stub"
      data-workspace-id={props.workspaceId}
      data-workspace-path={props.workspacePath}
      data-page={props.page}
    />
  ),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryEntries: vi.fn(),
    fetchLibraryContent: vi.fn(),
    searchVault: vi.fn(),
  }
})

import { fetchLibraryEntries, fetchLibraryContent, searchVault } from '@/lib/api'

const WORKSPACE = 'ws-1'
const AUDIO_PATH = 'vault/media/song.mp3'
const VIDEO_PATH = 'vault/media/clip.mp4'
const PDF_PATH = 'vault/docs/doc.pdf'
const OTHER_PATH = 'notes/Other.md'
const SELF_PATH = 'notes/Cockpit.md'

function libraryEntry(path: string, name: string): LibraryEntry {
  return {
    name,
    path,
    is_dir: false,
    is_hidden: false,
    size: 1000,
    modified_at: '2026-09-01T00:00:00Z',
    is_text_editable: false,
  }
}

function searchResponse(overrides: Partial<VaultSearchResponse> = {}): VaultSearchResponse {
  return {
    collection_id: 'kb_1',
    complete: true,
    notes: [],
    records: [],
    views: [],
    ...overrides,
  }
}

function renderNote(
  content: string,
  resolveEmbedUrl: (target: string, heading?: string, block?: string) => EmbedResolution,
  extra: Partial<{ workspaceId: string; collectionId: string; notePath: string }> = {},
) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <KnowledgeBaseMarkdown
        content={content}
        notePath={extra.notePath ?? SELF_PATH}
        resolveEmbedUrl={resolveEmbedUrl}
        {...(extra.workspaceId ? { workspaceId: extra.workspaceId } : {})}
        {...(extra.collectionId ? { collectionId: extra.collectionId } : {})}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('a standalone audio embed mounts the real player (ADR-083 Step 6, EMB-105)', () => {
  function resolver(): EmbedResolution {
    return {
      state: 'resolved',
      path: 'media/song.mp3',
      url: 'https://example.test/song.mp3',
      workspaceId: WORKSPACE,
      workspacePath: AUDIO_PATH,
    }
  }

  beforeEach(() => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([libraryEntry(AUDIO_PATH, 'song.mp3')])
  })

  it('renders LibraryAudioPreview, never the link-fallback badge, when the embed stands alone', async () => {
    renderNote('![[song.mp3]]', resolver)
    await waitFor(() => expect(screen.getByTestId('library-audio-preview')).toBeInTheDocument())
    expect(screen.queryByText('embed shown as a link')).not.toBeInTheDocument()
  })

  it('keeps the link-fallback treatment — paired negative — when mixed inline with other text', async () => {
    renderNote('Listen to ![[song.mp3]] now.', resolver)
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    expect(screen.queryByTestId('library-audio-preview')).not.toBeInTheDocument()
    expect(screen.getByText('embed shown as a link')).toBeInTheDocument()
  })

  it('never applies the |400 size modifier — EMB-030 is pictures only, and the display text is never eaten by it', async () => {
    // DIES ON: reintroducing the exact bug this feature already fixed once
    // (![[song.mp3|400]] losing its filename to "400").
    renderNote('![[song.mp3|400]]', resolver)
    await waitFor(() => expect(screen.getByTestId('library-audio-preview')).toBeInTheDocument())
    expect(screen.queryByText('400')).not.toBeInTheDocument()
  })
})

describe('a standalone video embed mounts the real player (ADR-083 Step 6, EMB-105)', () => {
  function resolver(): EmbedResolution {
    return {
      state: 'resolved',
      path: 'media/clip.mp4',
      url: 'https://example.test/clip.mp4',
      workspaceId: WORKSPACE,
      workspacePath: VIDEO_PATH,
    }
  }

  beforeEach(() => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([libraryEntry(VIDEO_PATH, 'clip.mp4')])
  })

  it('renders LibraryVideoPreview, never the link-fallback badge, when the embed stands alone', async () => {
    renderNote('![[clip.mp4]]', resolver)
    await waitFor(() => expect(screen.getByTestId('library-video-preview')).toBeInTheDocument())
    expect(screen.queryByText('embed shown as a link')).not.toBeInTheDocument()
  })

  it('keeps the link-fallback treatment — paired negative — when mixed inline with other text', async () => {
    renderNote('See ![[clip.mp4]] above.', resolver)
    await waitFor(() => expect(screen.getByTestId('markdown-link')).toBeInTheDocument())
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()
    expect(screen.getByText('embed shown as a link')).toBeInTheDocument()
  })

  it('is not confused with the allow-listed EXTERNAL video facade (US-9) — a local file wikilink never mounts VideoEmbed', async () => {
    renderNote('![[clip.mp4]]', resolver)
    await waitFor(() => expect(screen.getByTestId('library-video-preview')).toBeInTheDocument())
    expect(document.querySelector('[data-testid="video-embed"]')).toBeNull()
  })
})

describe('a standalone pdf page-fragment embed dispatches to KbPdfPageEmbedMount (ADR-083 Step 6, EMB-105)', () => {
  function resolver(): EmbedResolution {
    return {
      state: 'resolved',
      path: 'docs/doc.pdf',
      url: 'https://example.test/doc.pdf',
      workspaceId: WORKSPACE,
      workspacePath: PDF_PATH,
    }
  }

  it('passes the parsed page number and workspace coordinates through to the real renderer’s mount', () => {
    renderNote('![[doc.pdf#page=3]]', resolver)
    const mount = screen.getByTestId('kb-pdf-page-embed-mount-stub')
    expect(mount.getAttribute('data-workspace-id')).toBe(WORKSPACE)
    expect(mount.getAttribute('data-workspace-path')).toBe(PDF_PATH)
    expect(mount.getAttribute('data-page')).toBe('3')
  })

  it('keeps the ordinary link-fallback treatment — paired negative — for a WHOLE-document pdf embed (no #page= fragment, EMB-025 still applies)', () => {
    renderNote('![[doc.pdf]]', resolver)
    expect(screen.queryByTestId('kb-pdf-page-embed-mount-stub')).not.toBeInTheDocument()
    expect(screen.getByText('embed shown as a link')).toBeInTheDocument()
  })

  it('keeps the link-fallback treatment — paired negative — when a page-fragment embed is mixed inline with other text', () => {
    renderNote('See ![[doc.pdf#page=3]] for details.', resolver)
    expect(screen.queryByTestId('kb-pdf-page-embed-mount-stub')).not.toBeInTheDocument()
    expect(screen.getByText('embed shown as a link')).toBeInTheDocument()
  })
})

describe('parseWikilink — a `page=N` fragment is a third form, alongside heading and block (ADR-083 Step 6, EMB-105)', () => {
  it('parses a page fragment distinctly from a heading', () => {
    const parsed = parseWikilink('doc.pdf#page=3')
    expect(parsed?.page).toBe(3)
    expect(parsed?.heading).toBeUndefined()
    expect(parsed?.block).toBeUndefined()
    expect(parsed?.target).toBe('doc.pdf')
  })

  it('does not misparse an ordinary heading that merely starts with "page" as a page fragment', () => {
    const parsed = parseWikilink('Note#page-notes')
    expect(parsed?.page).toBeUndefined()
    expect(parsed?.heading).toBe('page-notes')
  })

  it('lets a block anchor win over the page pattern even in the pathological case where its text looks page-shaped', () => {
    // `^` is checked first (isBlock), so this is read as block id "page=3",
    // never as page number 3 — proves the ordering, not just the outcome.
    const parsed = parseWikilink('Note#^page=3')
    expect(parsed?.block).toBe('page=3')
    expect(parsed?.page).toBeUndefined()
  })
})

describe('a ```query fence runs a real search when the open note carries workspace/collection context (ADR-083 Step 6, US-12)', () => {
  function noResolver(): EmbedResolution {
    return { state: 'indeterminate', reason: 'no reason available' }
  }

  it('sends the fence body as a free-text query and renders real results', async () => {
    vi.mocked(searchVault).mockResolvedValue(
      searchResponse({ notes: [{ path: 'a.md', title: 'Match A', snippet: 'hit' }] }),
    )
    renderNote('```query\nMatch A\n```', noResolver, { workspaceId: WORKSPACE, collectionId: 'kb_1' })
    await waitFor(() => expect(screen.getByTestId('kb-query-fence-results')).toBeInTheDocument())
    expect(screen.getByText('Match A')).toBeInTheDocument()
    expect(vi.mocked(searchVault)).toHaveBeenCalledWith(
      WORKSPACE,
      expect.objectContaining({ query: 'Match A', collection_id: 'kb_1' }),
    )
  })

  it('falls back to the inherited, non-searching code renderer — paired negative — with no workspace/collection context', () => {
    // DIES ON: an empty box. The fence's own text must still be visible
    // through the inherited Shiki renderer even when nothing can search it.
    renderNote('```query\nMatch A\n```', noResolver)
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
    expect(vi.mocked(searchVault)).not.toHaveBeenCalled()
    expect(screen.getByTestId('shiki').textContent).toContain('Match A')
  })

  it('still routes a mermaid fence through the inherited renderer untouched — no regression from the new code slot', () => {
    renderNote('```mermaid\ngraph TD; A-->B;\n```', noResolver, { workspaceId: WORKSPACE, collectionId: 'kb_1' })
    expect(screen.getByTestId('mermaid-diagram')).toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
  })

  it('still routes plain inline code through the inherited renderer untouched', () => {
    renderNote('some `inline code` here', noResolver, { workspaceId: WORKSPACE, collectionId: 'kb_1' })
    expect(screen.getByText('inline code')).toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
  })
})

describe('nesting is out: a query fence inside a transcluded note never runs (ADR-083 EMB-060/N1)', () => {
  it('renders the fence as plain, non-searching code inside the transclusion — the outer note’s workspace/collection is not inherited by the nested pass', async () => {
    vi.mocked(fetchLibraryContent).mockResolvedValue({
      path: OTHER_PATH,
      content: '```query\nMatch A\n```',
      size: 20,
      is_text: true,
      too_large: false,
    })
    const resolver = (): EmbedResolution => ({
      state: 'resolved',
      path: OTHER_PATH,
      url: `/api/v1/library/${WORKSPACE}/download?path=${encodeURIComponent(OTHER_PATH)}`,
      workspaceId: WORKSPACE,
      workspacePath: OTHER_PATH,
    })
    renderNote('![[Other.md]]', resolver, { workspaceId: WORKSPACE, collectionId: 'kb_1' })
    await waitFor(() => expect(screen.getByTestId('kb-transclusion')).toBeInTheDocument())
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
    expect(vi.mocked(searchVault)).not.toHaveBeenCalled()
    // Paired positive: the fence's own text is still visible, not an empty box.
    expect(screen.getByTestId('shiki').textContent).toContain('Match A')
  })
})
