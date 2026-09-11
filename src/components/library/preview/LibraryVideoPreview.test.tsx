// LibraryVideoPreview.test.tsx — the `pane`/`inline` variant contract added
// for ADR-083 embedded-content spec Step 6 (EMB-027/028/105). No test file
// existed for this component before this change; the pane's own behaviour
// (a plain `<video controls src=...>`) is covered here alongside the new
// inline variant, following the same shape LibraryImagePreview.test.tsx and
// LibraryAudioPreview.test.tsx already use for their own variant contracts.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { LibraryVideoPreview } from './LibraryVideoPreview'
// URL ORACLE. `libraryDownloadUrl` is deliberately NOT mocked or re-called
// here: the real one builds its query string with `URLSearchParams`, which
// percent-encodes the path separator. A hand-written mock re-implemented it
// WITHOUT that (asserting a URL production never emits), and calling the real
// function on BOTH sides of an assertion is self-referential — it agrees with
// itself no matter what the builder does. The literal below is the real
// builder's actual output, so a change to how download URLs are built fails
// this test.
const EXPECTED_SRC = '/api/v1/library/ws-1/download?path=video%2Fclip.mp4'
import type { LibraryEntry } from '@/lib/api'

const ENTRY: LibraryEntry = {
  name: 'clip.mp4',
  path: 'video/clip.mp4',
  is_dir: false,
  is_hidden: false,
  size: 1_048_576,
  modified_at: '2026-08-22T10:15:00Z',
  is_text_editable: false,
}

describe('LibraryVideoPreview — pane variant (default, unchanged)', () => {
  it('renders the download URL as the video source', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} />)
    const video = screen.getByTestId('library-video-preview').querySelector('video')
    expect(video).toHaveAttribute('controls')
    expect(video).toHaveAttribute('src', EXPECTED_SRC)
  })

  it('carries data-variant="pane" when no variant prop is given', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} />)
    expect(screen.getByTestId('library-video-preview')).toHaveAttribute('data-variant', 'pane')
  })
})

describe('LibraryVideoPreview — inline variant (EMB-027: same renderer, EMB-028: layout only)', () => {
  it('renders the identical src as the pane variant', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    expect(screen.getByTestId('library-video-preview').querySelector('video')).toHaveAttribute(
      'src',
      EXPECTED_SRC,
    )
  })

  it('draws its outermost container with DIFFERENT layout classes than the pane variant', () => {
    const pane = render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="pane" />)
    const paneClass = pane.getByTestId('library-video-preview').className
    pane.unmount()

    const inline = render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    const inlineClass = inline.getByTestId('library-video-preview').className
    inline.unmount()

    // MUTATION THIS DIES ON: a component that computes the same className
    // regardless of `variant`.
    expect(inlineClass).not.toBe(paneClass)
    expect(inlineClass).not.toContain('flex-1')
  })
})

describe('LibraryVideoPreview — no width modifier surface (EMB-030)', () => {
  it('accepts no width prop at all — a |400 segment has nothing to apply to', () => {
    // @ts-expect-error — width is not a prop of this component, by design (EMB-030).
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="inline" width={400} />)
    expect(screen.queryByText('400')).not.toBeInTheDocument()
  })
})

// ── An undecodable source is stated, not rendered as a dead player (M6) ─────
//
// The element's own fallback CHILDREN fire only when the browser does not
// support the element TYPE — never when the SOURCE fails, which is the case
// that actually happens. `libraryPreviewKind.ts` maps `.avi`/`.mkv` to video
// and `.flac`/`.opus` to audio, and no browser plays all of them; the URL can
// also 404 after the 30s-stale listing or 401 after a session expires. Step 6
// mounts these INSIDE notes, so without this the reader gets a dead control
// bar or a black box in the middle of their prose with nothing naming it.

describe('video — a source the browser refuses', () => {
  it('replaces the player with a stated failure naming the file, plus a real download link', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} />)

    // Positive control first: the element IS there before anything fails.
    const el = screen.getByTestId('library-video-element')
    expect(el).toBeInTheDocument()
    expect(screen.queryByTestId('library-video-unplayable')).not.toBeInTheDocument()

    fireEvent.error(el)

    const notice = screen.getByTestId('library-video-unplayable')
    expect(notice).toHaveTextContent(/could not play/i)
    expect(notice).toHaveTextContent('clip.mp4')
    const link = notice.querySelector('a')
    expect(link).not.toBeNull()
    expect(link).toHaveAttribute('href', EXPECTED_SRC)
    expect(link).toHaveAttribute('download')

    // And the dead element is GONE — not left sitting underneath the notice.
    expect(screen.queryByTestId('library-video-element')).not.toBeInTheDocument()
  })

  it('shows the same stated failure in the INLINE variant, which is the one Step 6 mounts inside notes', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)

    fireEvent.error(screen.getByTestId('library-video-element'))
    expect(screen.getByTestId('library-video-unplayable')).toHaveTextContent(/download it instead/i)
  })
})

// ---------------------------------------------------------------------------
// Review I12 — EMB-027's "exactly one renderer per kind", for VIDEO
// ---------------------------------------------------------------------------

// LibraryAudioPreview.test.tsx proves the pane and the inline embed reach the
// SAME module. There was no counterpart for video, and the gap is not
// theoretical: a reintroduced private `<video>` inside LibraryPreviewPane
// would carry the same `library-video-preview` testid the pane's own test
// queries, so every existing assertion would still pass while two renderers
// shipped. check-no-duplicate-renderer.sh does not cover video either, so
// this test is currently the only thing that would notice.

describe('LibraryVideoPreview — single definition used by both surfaces (EMB-027, review I12)', () => {
  it('the pane and the inline embed both render through the SAME module', async () => {
    vi.resetModules()
    const mountedVariants: string[] = []

    vi.doMock('./LibraryVideoPreview', async (importOriginal) => {
      const actual = await importOriginal<typeof import('./LibraryVideoPreview')>()
      return {
        ...actual,
        LibraryVideoPreview: (props: Parameters<typeof actual.LibraryVideoPreview>[0]) => {
          mountedVariants.push(props.variant ?? 'pane')
          return actual.LibraryVideoPreview(props)
        },
      }
    })
    // Every OTHER static preview surface LibraryPreviewPane imports, stubbed
    // to a no-op so mounting it for a VIDEO entry pulls in none of their real
    // dependencies (mermaid, shiki, CodeMirror, BasePreview, pdfjs-dist).
    vi.doMock('./BasePreview', () => ({ BasePreview: () => null }))
    vi.doMock('./LibraryImagePreview', () => ({ LibraryImagePreview: () => null }))
    vi.doMock('./LibraryAudioPreview', () => ({ LibraryAudioPreview: () => null }))
    vi.doMock('./LibraryPdfPreview', () => ({ LibraryPdfPreview: () => null }))
    vi.doMock('./LibraryMarkdownPreview', () => ({ LibraryMarkdownPreview: () => null }))
    vi.doMock('./LibraryMermaidPreview', () => ({ LibraryMermaidPreview: () => null }))
    vi.doMock('./LibraryCodePreview', () => ({ LibraryCodePreview: () => null }))
    vi.doMock('./LibraryTextPreview', () => ({ LibraryTextPreview: () => null }))
    vi.doMock('./LibraryDownloadCard', () => ({ LibraryDownloadCard: () => null }))
    vi.doMock('@/lib/api', async (importOriginal) => {
      const actual = await importOriginal<typeof import('@/lib/api')>()
      return {
        ...actual,
        mintLibraryPreviewToken: vi.fn(),
        fetchLibraryContent: vi.fn(),
        fetchLibraryEntries: vi.fn().mockResolvedValue([ENTRY]),
      }
    })

    const { LibraryPreviewPane } = await import('../LibraryPreviewPane')
    const { KbVideoEmbedMount } = await import('./KbVideoEmbedMount')

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const pane = render(
      <QueryClientProvider client={qc}>
        <LibraryPreviewPane
          workspaceId="ws-1"
          entry={ENTRY}
          onClose={() => {}}
          onDownload={() => {}}
          mintPreviewToken={null}
        />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(mountedVariants).toContain('pane'))
    pane.unmount()

    const embed = render(
      <QueryClientProvider client={qc}>
        <KbVideoEmbedMount workspaceId="ws-1" workspacePath="video/clip.mp4" />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(mountedVariants).toContain('inline'))
    embed.unmount()

    // Both consumers went through the ONE mocked module. If either held a
    // private duplicate instead, its variant would never appear here.
    expect(mountedVariants).toContain('pane')
    expect(mountedVariants).toContain('inline')

    vi.doUnmock('./LibraryVideoPreview')
    vi.doUnmock('./BasePreview')
    vi.doUnmock('./LibraryImagePreview')
    vi.doUnmock('./LibraryAudioPreview')
    vi.doUnmock('./LibraryPdfPreview')
    vi.doUnmock('./LibraryMarkdownPreview')
    vi.doUnmock('./LibraryMermaidPreview')
    vi.doUnmock('./LibraryCodePreview')
    vi.doUnmock('./LibraryTextPreview')
    vi.doUnmock('./LibraryDownloadCard')
    vi.doUnmock('@/lib/api')
    vi.resetModules()
  })
})
