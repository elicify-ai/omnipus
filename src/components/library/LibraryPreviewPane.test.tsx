// LibraryPreviewPane.test.tsx — library-spec.md D-5 / section 4's scope
// table, end to end: each file kind renders through the correct surface, the
// explicit view/edit toggle + save wiring works, a failed save surfaces an
// error (never silently swallowed), and a non-text/too-large file falls back
// to the download card instead of rendering garbage.
//
// Heavy third-party renderers are mocked at their own module boundary (the
// same convention historical-markdown.test.tsx and mermaid-renderer.test.tsx
// already use for `react-shiki` / `mermaid`) — this test is about OUR wiring
// (which surface mounts, what gets fetched/saved), not about re-verifying
// Mermaid's or Shiki's own rendering. `@uiw/react-codemirror` is mocked the
// same way: a plain controlled <textarea>, since CodeMirror 6 mounts a
// contenteditable view jsdom cannot drive through fireEvent — LibraryCodeEditor's
// actual props (value/onChange/extensions) were verified against the real
// package's shipped .d.ts when it was written (see that file's doc comment).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry, LibraryContentResponse } from '@/lib/api'
import type { LibraryPreviewTokenResponse } from '@/lib/api/generated/openapi-types'
import { LIBRARY_PREVIEW_KINDS } from './preview/libraryPreviewKind'
import type { LibraryPreviewKind } from './preview/libraryPreviewKind'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryContent: vi.fn(),
    putLibraryContent: vi.fn(),
    libraryDownloadUrl: vi.fn((wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`),
  }
})

const initialize = vi.fn()
const renderFn = vi.fn().mockResolvedValue({ svg: '<svg><title>mock-diagram</title></svg>' })
vi.mock('mermaid', () => ({ default: { initialize, render: renderFn } }))

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  // markdown-shared.tsx passes Shiki its pure-JS regex engine (the SPA's CSP
  // refuses the WebAssembly default); the module is mocked here, so this only
  // has to exist.
  createJavaScriptRegexEngine: () => ({}),
}))

vi.mock('@uiw/react-codemirror', () => ({
  default: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea
      data-testid="library-editor-textarea"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}))

// PDF.js is mocked at its own module boundary, the same convention `mermaid`
// and `react-shiki` use above: this file tests OUR wiring (which surface the
// pane mounts, with which props), not pdfjs-dist's rendering. The stub records
// the props it was handed so the wiring assertion is about the real contract
// rather than about a testid appearing.
const pdfProps = vi.fn()
vi.mock('./preview/LibraryPdfPreview', () => ({
  LibraryPdfPreview: (props: { workspaceId: string; entry: LibraryEntry }) => {
    pdfProps(props)
    return <div data-testid="library-pdf-preview" />
  },
}))

import { fetchLibraryContent, putLibraryContent } from '@/lib/api'
import { LibraryPreviewPane } from './LibraryPreviewPane'
import type { MintLibraryPreviewToken } from './LibraryPreviewPane'

const mockedFetchContent = vi.mocked(fetchLibraryContent)
const mockedPutContent = vi.mocked(putLibraryContent)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makeEntry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'report.md',
    path: 'report.md',
    is_dir: false,
    is_hidden: false,
    size: 20,
    modified_at: '2026-07-28T10:15:00Z',
    mime: 'text/markdown',
    is_text_editable: true,
    ...over,
  }
}

function makeContent(over: Partial<LibraryContentResponse> = {}): LibraryContentResponse {
  return {
    path: 'report.md',
    content: '# Report\n',
    size: 9,
    is_text: true,
    too_large: false,
    ...over,
  }
}

function makeToken(over: Partial<LibraryPreviewTokenResponse> = {}): LibraryPreviewTokenResponse {
  return {
    token: 'kZ8vQ2mR7xT1yB4nW6cA9pL0sD3fG5hJ8kM2nP4qR6t',
    url: '/library-preview/kZ8vQ2mR7xT1yB4nW6cA9pL0sD3fG5hJ8kM2nP4qR6t/report.html',
    expires_at: '2026-08-22T14:45:00Z',
    expires_in_seconds: 900,
    scope: 'file',
    scope_root: 'report.html',
    workspace_id: 'ws-1',
    ...over,
  }
}

interface RenderPaneHandlers {
  onClose?: () => void
  onDownload?: (entry: LibraryEntry) => void
  /** Omitted entirely = production wiring, where the minter is still null
   *  because the backend endpoint has not shipped. */
  mint?: MintLibraryPreviewToken | null
}

function renderPane(entry: LibraryEntry, handlers: RenderPaneHandlers = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <LibraryPreviewPane
        workspaceId="ws-1"
        entry={entry}
        onClose={handlers.onClose ?? vi.fn()}
        onDownload={handlers.onDownload ?? vi.fn()}
        {...('mint' in handlers ? { mintPreviewToken: handlers.mint } : {})}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
})

describe('LibraryPreviewPane — markdown + mermaid', () => {
  it('renders markdown text AND draws a nested ```mermaid fence via the shared MermaidDiagram', async () => {
    mockedFetchContent.mockResolvedValue(
      makeContent({ content: '# Report\n\nStatus is green.\n\n```mermaid\ngraph TD\n  A --> B\n```\n' }),
    )

    renderPane(makeEntry())

    await waitFor(() => expect(screen.getByText('Status is green.')).toBeInTheDocument())
    await waitFor(() => expect(renderFn).toHaveBeenCalled())
    expect(renderFn.mock.calls[0][1]).toContain('graph TD')
  })
})

describe('LibraryPreviewPane — code file', () => {
  it('highlights a non-markdown text file via the shared ShikiCodeBlock', async () => {
    mockedFetchContent.mockResolvedValue(
      makeContent({ path: 'main.ts', content: 'export const x = 1\n' }),
    )

    renderPane(makeEntry({ name: 'main.ts', path: 'main.ts', mime: 'text/typescript' }))

    await waitFor(() => expect(screen.getByTestId('shiki')).toBeInTheDocument())
    expect(screen.getByTestId('shiki')).toHaveTextContent('export const x = 1')
  })
})

describe('LibraryPreviewPane — edit and save', () => {
  it('edits then saves, calling putLibraryContent with the new content and showing the saved state', async () => {
    mockedFetchContent.mockResolvedValue(makeContent())
    mockedPutContent.mockResolvedValue(makeEntry({ size: 30 }))

    renderPane(makeEntry())

    await waitFor(() => expect(screen.getByTestId('library-preview-mode-edit')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-preview-mode-edit'))

    const textarea = await screen.findByTestId('library-editor-textarea')
    fireEvent.change(textarea, { target: { value: '# Report\n\nUpdated body.\n' } })

    const saveButton = screen.getByTestId('library-preview-save')
    expect(saveButton).not.toBeDisabled()
    fireEvent.click(saveButton)

    await waitFor(() =>
      expect(mockedPutContent).toHaveBeenCalledWith('ws-1', {
        path: 'report.md',
        content: '# Report\n\nUpdated body.\n',
      }),
    )
    await waitFor(() => expect(screen.getByText(/saved/i)).toBeInTheDocument())
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'success')).toBe(true)
  })

  it('surfaces a failed save as a visible error — never silently swallowed', async () => {
    mockedFetchContent.mockResolvedValue(makeContent())
    mockedPutContent.mockRejectedValue(new Error('disk full'))

    renderPane(makeEntry())

    await waitFor(() => expect(screen.getByTestId('library-preview-mode-edit')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-preview-mode-edit'))

    const textarea = await screen.findByTestId('library-editor-textarea')
    fireEvent.change(textarea, { target: { value: '# Report\n\nbroken save\n' } })
    fireEvent.click(screen.getByTestId('library-preview-save'))

    await waitFor(() => expect(screen.getByText('disk full')).toBeInTheDocument())
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(true)
    // The Save button stays enabled — the edit is still there to retry, not
    // discarded by the failed attempt.
    expect(screen.getByTestId('library-preview-save')).not.toBeDisabled()
  })
})

describe('LibraryPreviewPane — non-text / too-large fallback', () => {
  it('falls back to the download card when the content endpoint reports is_text: false, despite the listing hint', async () => {
    mockedFetchContent.mockResolvedValue(makeContent({ content: undefined, is_text: false }))

    renderPane(makeEntry({ name: 'notes.txt', path: 'notes.txt' }))

    await waitFor(() => expect(screen.getByTestId('library-download-card')).toBeInTheDocument())
    expect(screen.queryByTestId('library-preview-mode-view')).not.toBeInTheDocument()
  })

  it('falls back to the download card when the content endpoint reports too_large: true', async () => {
    mockedFetchContent.mockResolvedValue(makeContent({ content: undefined, too_large: true }))

    renderPane(makeEntry({ name: 'huge.log', path: 'huge.log' }))

    await waitFor(() => expect(screen.getByTestId('library-download-card')).toBeInTheDocument())
  })

  it('renders the download card directly (no content fetch) for a kind with no preview at all', async () => {
    renderPane(makeEntry({ name: 'archive.zip', path: 'archive.zip', mime: 'application/zip', is_text_editable: false }))

    await waitFor(() => expect(screen.getByTestId('library-download-card')).toBeInTheDocument())
    expect(mockedFetchContent).not.toHaveBeenCalled()
  })

  it('the download card button calls the onDownload handler passed down from the explorer', async () => {
    const onDownload = vi.fn()
    renderPane(
      makeEntry({ name: 'archive.zip', path: 'archive.zip', mime: 'application/zip', is_text_editable: false }),
      { onDownload },
    )

    await waitFor(() => expect(screen.getByTestId('library-download-card')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-download-card-button'))
    expect(onDownload).toHaveBeenCalledWith(expect.objectContaining({ path: 'archive.zip' }))
  })
})

describe('LibraryPreviewPane — media kinds (no content fetch)', () => {
  it('renders an <img> for an image entry, using the raw download URL as the src', async () => {
    renderPane(makeEntry({ name: 'photo.png', path: 'photo.png', mime: 'image/png', is_text_editable: false }))

    const img = await screen.findByRole('img', { name: 'photo.png' })
    expect(img).toHaveAttribute('src', '/api/v1/library/ws-1/download?path=photo.png')
    expect(mockedFetchContent).not.toHaveBeenCalled()
  })

  it('renders a <video controls> for a video entry, using the raw download URL as the src', async () => {
    renderPane(makeEntry({ name: 'clip.mp4', path: 'clip.mp4', mime: 'video/mp4', is_text_editable: false }))

    await waitFor(() => expect(screen.getByTestId('library-video-preview')).toBeInTheDocument())
    const video = screen.getByTestId('library-video-preview').querySelector('video')
    expect(video).toHaveAttribute('controls')
    expect(video).toHaveAttribute('src', '/api/v1/library/ws-1/download?path=clip.mp4')
    expect(mockedFetchContent).not.toHaveBeenCalled()
  })
})

describe('LibraryPreviewPane — the single header row', () => {
  // Three header bars became one (operator direction, 2026-08-04). Download,
  // Rename, Move and Delete were REMOVED from the pane, not relocated — every
  // entry row already carries the same four in its own DotsThree menu, so the
  // strip duplicated a menu the user had just used to open this very file.
  it('no longer offers download/rename/move/delete inside the pane', async () => {
    mockedFetchContent.mockResolvedValue(makeContent())
    renderPane(makeEntry())

    const pane = await screen.findByTestId('library-preview-pane')
    for (const name of [/rename/i, /move/i, /delete/i]) {
      expect(within(pane).queryByRole('button', { name })).toBeNull()
    }
  })

  // Close is the ONLY way to dismiss the pane — LibraryExplorer clears
  // selectedEntry from here and from destructive mutations, never from a second
  // click on the row — so it survives the cull that took the other four.
  it('keeps Close reachable and wired', async () => {
    const onClose = vi.fn()
    mockedFetchContent.mockResolvedValue(makeContent())
    renderPane(makeEntry(), { onClose })

    await screen.findByTestId('library-preview-pane')
    fireEvent.click(screen.getByTestId('library-preview-close'))
    expect(onClose).toHaveBeenCalled()
  })

  // The editable body's view/edit/save controls portal INTO this row rather
  // than rendering a second bar of their own.
  it('hosts the view/edit/save controls in the same row as the title', async () => {
    mockedFetchContent.mockResolvedValue(makeContent())
    renderPane(makeEntry())

    // The header row paints before the content query resolves, so wait for the
    // editor's own control to arrive before asserting where it landed.
    await screen.findByTestId('library-preview-save')
    const title = screen.getByTestId('library-preview-title')
    const row = title.parentElement as HTMLElement
    for (const id of ['library-preview-mode-view', 'library-preview-mode-edit', 'library-preview-save']) {
      expect(row.querySelector(`[data-testid="${id}"]`)).toBeTruthy()
    }
    expect(row.querySelector('[data-testid="library-preview-close"]')).toBeTruthy()
  })

  // Dropping the visible labels makes the accessible name the ONLY name.
  it('gives every icon-only control an accessible name', async () => {
    mockedFetchContent.mockResolvedValue(makeContent())
    renderPane(makeEntry())

    await screen.findByTestId('library-preview-save')
    const pane = screen.getByTestId('library-preview-pane')
    expect(within(pane).getByRole('button', { name: /^view$/i })).toBeInTheDocument()
    expect(within(pane).getByRole('button', { name: /^edit$/i })).toBeInTheDocument()
    expect(within(pane).getByRole('button', { name: /^save$/i })).toBeInTheDocument()
    expect(within(pane).getByRole('button', { name: /close preview/i })).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// ADR-067 STAGE 1 — render-first preview.
//
// The pane shows the ARTIFACT; source appears only after Edit. Three kinds are
// new (html / pdf / audio) and the dispatch that mounts them is the thing most
// likely to rot: it was an `&&` chain, where a widened `LibraryPreviewKind`
// compiled clean and rendered an empty pane (spec SC-017 / test 89).
// ─────────────────────────────────────────────────────────────────────────────

const HTML_MARKUP = '<!doctype html><title>Q3</title><h1>Quarterly report</h1>'

function htmlEntry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return makeEntry({ name: 'report.html', path: 'report.html', mime: 'text/html', is_text_editable: true, ...over })
}

const mintOk: MintLibraryPreviewToken = () => Promise.resolve(makeToken())

afterEach(() => {
  vi.useRealTimers()
})

describe('LibraryPreviewPane — HTML renders; source only behind Edit (US-1 AS-1/AS-2)', () => {
  it('mounts a sandboxed iframe pointed at the preview-token URL, and shows no markup', async () => {
    const mint = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))

    renderPane(htmlEntry(), { mint })

    const frame = await screen.findByTestId('library-html-preview-frame')
    expect(frame.tagName).toBe('IFRAME')
    // srcdoc is not an alternative: it resolves relative URLs against the
    // EMBEDDER (so a bundle's css/js/font load from the SPA's own origin and
    // 404) and it has no response to carry the isolation policy (§10.6).
    expect(frame).toHaveAttribute('src', makeToken().url)
    expect(frame).not.toHaveAttribute('srcdoc')
    // Render-first: the markup is the source, and source is behind Edit.
    expect(screen.queryByText(/Quarterly report/)).toBeNull()
    // Dies on: replacing `src={token.url}` with `srcDoc={content}`.
  })

  it('carries the three frame attributes, and never allow-same-origin', async () => {
    const mint = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))

    renderPane(htmlEntry(), { mint })

    const frame = await screen.findByTestId('library-html-preview-frame')
    // The effective sandbox is the INTERSECTION of this attribute and the
    // response's `sandbox` directive — a capability exists only if both grant
    // it. Adding allow-same-origin to both hands the page the session cookie.
    expect(frame).toHaveAttribute('sandbox', 'allow-scripts')
    expect(frame.getAttribute('sandbox')).not.toContain('allow-same-origin')
    expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(frame).toHaveAttribute('allow', '')
    // Dies on: sandbox="allow-scripts allow-same-origin", or dropping
    // referrerpolicy (which would leak the URL-borne token in Referer).
  })

  it('reveals the source in an editor on Edit, and returns to the rendered page on View', async () => {
    const mint = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))

    renderPane(htmlEntry(), { mint })
    await screen.findByTestId('library-html-preview-frame')

    fireEvent.click(screen.getByTestId('library-preview-mode-edit'))
    expect(await screen.findByTestId('library-editor-textarea')).toHaveValue(HTML_MARKUP)
    expect(screen.queryByTestId('library-html-preview-frame')).toBeNull()

    fireEvent.click(screen.getByTestId('library-preview-mode-view'))
    expect(await screen.findByTestId('library-html-preview-frame')).toBeInTheDocument()
    // Dies on: making the html body frame-only (no LibraryTextPreview shell),
    // which removes Edit entirely.
  })

  it('still renders a too-large HTML file — losing Edit, never the page', async () => {
    const mint = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(
      makeContent({ path: 'report.html', content: undefined, too_large: true }),
    )

    renderPane(htmlEntry(), { mint })

    await screen.findByTestId('library-html-preview-frame')
    expect(screen.queryByTestId('library-download-card')).toBeNull()
    expect(screen.queryByTestId('library-preview-mode-edit')).toBeNull()
    // Dies on: routing html through LibraryTextBody, whose too_large/binary
    // branches replace the whole surface with a download card.
  })

  it('mints a bundle scope for a page in a folder, and a file scope at the work-tree root', async () => {
    const nested = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'reports/q3/index.html', content: HTML_MARKUP }))
    const { unmount } = renderPane(htmlEntry({ name: 'index.html', path: 'reports/q3/index.html' }), { mint: nested })
    await waitFor(() => expect(nested).toHaveBeenCalledTimes(1))
    // Only a bundle scope makes the page's own relative stylesheet, script and
    // font resolve — the token is in the URL, so relative subresources inherit
    // it (US-1 AS-4).
    expect(nested).toHaveBeenCalledWith({
      workspace_id: 'ws-1',
      path: 'reports/q3',
      scope: 'bundle',
      entry_path: 'index.html',
    })
    unmount()

    const atRoot = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))
    renderPane(htmlEntry(), { mint: atRoot })
    await waitFor(() => expect(atRoot).toHaveBeenCalledTimes(1))
    // "bundle" here would mean the whole workspace, which §10.5 forbids.
    expect(atRoot).toHaveBeenCalledWith({ workspace_id: 'ws-1', path: 'report.html', scope: 'file' })
    // Dies on: always minting scope 'file' (nested case), or always 'bundle'
    // with the dirname (root case, which would ask for path '').
  })
})

describe('LibraryPreviewPane — the untrusted-content boundary (US-2 AS-4 / FR-007)', () => {
  it('shows a persistent boundary in the pane chrome, outside the frame', async () => {
    const mint = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))

    renderPane(htmlEntry(), { mint })

    const boundary = await screen.findByTestId('library-preview-untrusted-boundary')
    const frame = await screen.findByTestId('library-html-preview-frame')
    // Outside the frame, and outside the body's scroll container: an opaque
    // origin stops the page reading the session, it does NOT stop it drawing a
    // convincing login prompt inside itself, so the marker must be somewhere
    // the page can neither paint nor scroll away.
    expect(boundary.contains(frame)).toBe(false)
    expect(boundary.parentElement).toBe(screen.getByTestId('library-preview-pane'))
    expect(boundary).toHaveTextContent(/untrusted/i)
    // Dies on: deleting the `{kind === 'html' && <UntrustedContentBoundary/>}`
    // line, or moving it inside the scrolling body.
  })

  it('does not mark content Omnipus renders itself as untrusted', async () => {
    mockedFetchContent.mockResolvedValue(makeContent())

    renderPane(makeEntry())

    await screen.findByTestId('library-preview-view-body')
    expect(screen.queryByTestId('library-preview-untrusted-boundary')).toBeNull()
    // Dies on: rendering the boundary unconditionally — a marker shown
    // everywhere is a marker nobody reads where it matters.
  })
})

describe('LibraryPreviewPane — preview unavailable, never a blank frame (FR-003c/FR-003n)', () => {
  it('says so plainly when no preview-token minter is wired (the shipping state today)', async () => {
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))

    renderPane(htmlEntry())

    const notice = await screen.findByTestId('library-html-preview-unavailable')
    expect(notice).toHaveTextContent(/preview unavailable/i)
    expect(screen.queryByTestId('library-html-preview-frame')).toBeNull()
    // Dies on: returning null (or an empty <iframe>) when the minter is absent
    // — a blank pane the reader cannot distinguish from a page that rendered
    // nothing.
  })

  it('offers a retry that re-mints when minting fails', async () => {
    const mint = vi.fn<MintLibraryPreviewToken>().mockRejectedValue(new Error('mint refused'))
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))

    renderPane(htmlEntry(), { mint })

    fireEvent.click(await screen.findByTestId('library-html-preview-retry'))
    await waitFor(() => expect(mint).toHaveBeenCalledTimes(2))
    // Dies on: swallowing the mint error into the loading state, which spins
    // forever with no way out.
  })

  it('announces expiry from the minted lifetime and re-mints on Reload (FR-003m)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const mint = vi.fn(mintOk)
    mockedFetchContent.mockResolvedValue(makeContent({ path: 'report.html', content: HTML_MARKUP }))

    renderPane(htmlEntry(), { mint })
    await screen.findByTestId('library-html-preview-frame')

    // The frame is cross-origin, opaque and sandboxed: onload fires for the
    // gateway's 404 page exactly as for the document, so the ONLY honest
    // signal is the lifetime the mint returned.
    await act(async () => {
      vi.advanceTimersByTime(899_000)
    })
    expect(screen.queryByTestId('library-html-preview-expired')).toBeNull()

    await act(async () => {
      vi.advanceTimersByTime(2_000)
    })
    expect(screen.getByTestId('library-html-preview-expired')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('library-html-preview-reload'))
    await waitFor(() => expect(mint).toHaveBeenCalledTimes(2))
    // Dies on: dropping the expiry effect (never announced), and on hard-coding
    // the delay to 0 instead of expires_in_seconds (announced at 899s).
  })
})

describe('LibraryPreviewPane — audio and PDF are drawn by Omnipus, not by the browser', () => {
  it('renders an <audio controls> for an audio entry, using the raw download URL as the src', async () => {
    renderPane(makeEntry({ name: 'podcast.mp3', path: 'podcast.mp3', mime: 'audio/mpeg', is_text_editable: false }))

    const wrap = await screen.findByTestId('library-audio-preview')
    const audio = wrap.querySelector('audio')
    expect(audio).toHaveAttribute('controls')
    expect(audio).toHaveAttribute('src', '/api/v1/library/ws-1/download?path=podcast.mp3')
    expect(mockedFetchContent).not.toHaveBeenCalled()
    // Dies on: dropping `controls` (a player with no transport is not a
    // player), or sourcing it from the token path, which serves no audio to
    // the authenticated SPA.
  })

  it('mounts LibraryPdfPreview with the workspace and entry, and fetches no content', async () => {
    renderPane(makeEntry({ name: 'manual.pdf', path: 'manual.pdf', mime: 'application/pdf', is_text_editable: false }))

    await screen.findByTestId('library-pdf-preview')
    expect(pdfProps).toHaveBeenCalledWith(
      expect.objectContaining({ workspaceId: 'ws-1', entry: expect.objectContaining({ path: 'manual.pdf' }) }),
    )
    expect(screen.queryByTestId('library-download-card')).toBeNull()
    expect(mockedFetchContent).not.toHaveBeenCalled()
    // Dies on: leaving .pdf on the download card, or routing it to an <iframe>
    // — which would hand the bytes to the browser's own PDF viewer, the thing
    // D15.3 exists to avoid.
  })
})

// Test 89 — `TestLibraryPreviewPane_NoUnhandledKind` (spec SC-017).
//
// Two guards, one table. The `Record<LibraryPreviewKind, …>` type means a kind
// added to the union without a case here FAILS TO COMPILE; iterating
// LIBRARY_PREVIEW_KINDS at runtime means a kind the pane forgets to dispatch
// renders the WRONG surface and fails here. Both are needed: the pane's own
// `never` check is a compile-time guard that vanishes the moment someone
// rewrites the switch back into an `&&` chain, which is exactly the shape this
// component had before ADR-067 and exactly how an empty pane ships unnoticed.
//
// Each row names the surface it expects, not merely "something rendered" — the
// pane's fall-through renders a download card, so "the pane is non-empty" would
// pass with every new kind silently degraded. markdown and mermaid share the
// view-body testid; which renderer draws inside it is covered by the dedicated
// describes at the top of this file.
interface KindCase {
  entry: LibraryEntry
  surfaceTestId: string
  content?: LibraryContentResponse
  mint?: MintLibraryPreviewToken
}

const KIND_CASES: Record<LibraryPreviewKind, KindCase> = {
  image: {
    entry: makeEntry({ name: 'photo.png', path: 'photo.png', mime: 'image/png', is_text_editable: false }),
    surfaceTestId: 'library-image-preview',
  },
  video: {
    entry: makeEntry({ name: 'clip.mp4', path: 'clip.mp4', mime: 'video/mp4', is_text_editable: false }),
    surfaceTestId: 'library-video-preview',
  },
  html: {
    entry: htmlEntry(),
    surfaceTestId: 'library-html-preview-frame',
    content: makeContent({ path: 'report.html', content: HTML_MARKUP }),
    mint: mintOk,
  },
  pdf: {
    entry: makeEntry({ name: 'manual.pdf', path: 'manual.pdf', mime: 'application/pdf', is_text_editable: false }),
    surfaceTestId: 'library-pdf-preview',
  },
  audio: {
    entry: makeEntry({ name: 'podcast.mp3', path: 'podcast.mp3', mime: 'audio/mpeg', is_text_editable: false }),
    surfaceTestId: 'library-audio-preview',
  },
  base: {
    // view-kinds-design-2026-09-03 §7 — a .base opens as its views. The base
    // surface fetches the file's own content (the tab list comes from it), so
    // this case provides one; the deeper states (tabs, refusal, empty) are
    // BasePreview.test.tsx's job, not this dispatch guard's.
    entry: makeEntry({ name: 'Invoices.base', path: 'Invoices.base', mime: 'text/yaml' }),
    surfaceTestId: 'base-preview',
    content: makeContent({
      path: 'Invoices.base',
      content: 'views:\n  - type: table\n    name: Outstanding\n',
    }),
  },
  markdown: {
    entry: makeEntry(),
    surfaceTestId: 'library-preview-view-body',
    content: makeContent(),
  },
  mermaid: {
    entry: makeEntry({ name: 'flow.mmd', path: 'flow.mmd', mime: 'text/plain' }),
    surfaceTestId: 'library-preview-view-body',
    content: makeContent({ path: 'flow.mmd', content: 'graph TD\n  A --> B\n' }),
  },
  text: {
    entry: makeEntry({ name: 'main.ts', path: 'main.ts', mime: 'text/typescript' }),
    surfaceTestId: 'shiki',
    content: makeContent({ path: 'main.ts', content: 'export const x = 1\n' }),
  },
  other: {
    entry: makeEntry({ name: 'archive.zip', path: 'archive.zip', mime: 'application/zip', is_text_editable: false }),
    surfaceTestId: 'library-download-card',
  },
}

describe('LibraryPreviewPane — every preview kind mounts a surface', () => {
  it('dispatches every member of LIBRARY_PREVIEW_KINDS to its own surface', async () => {
    expect(Object.keys(KIND_CASES).sort()).toEqual([...LIBRARY_PREVIEW_KINDS].sort())

    for (const kind of LIBRARY_PREVIEW_KINDS) {
      const testCase = KIND_CASES[kind]
      mockedFetchContent.mockReset()
      if (testCase.content) {
        mockedFetchContent.mockResolvedValue(testCase.content)
      } else {
        mockedFetchContent.mockRejectedValue(new Error(`kind "${kind}" must not fetch file content`))
      }

      const { unmount } = renderPane(testCase.entry, testCase.mint ? { mint: vi.fn(testCase.mint) } : {})
      try {
        await screen.findByTestId(testCase.surfaceTestId)
      } catch (err) {
        throw new Error(
          `kind "${kind}" did not mount "${testCase.surfaceTestId}" — it fell through the pane's dispatch: ${String(err)}`,
        )
      } finally {
        unmount()
      }
    }
    // Dies on: deleting any `case` from renderBody (that kind falls to the
    // download card), and on rewriting the switch as an `&&` chain that omits
    // one kind (that kind renders nothing at all).
  })
})
