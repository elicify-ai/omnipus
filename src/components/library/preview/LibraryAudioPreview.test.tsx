// LibraryAudioPreview.test.tsx — ADR-083 embedded-content spec, Step 6
// (EMB-105/EMB-027): "There MUST be exactly one renderer per kind, shared
// between the full-screen pane and the inline embed. No inline-only copy may
// be created." This component was extracted OUT of LibraryPreviewPane.tsx,
// where it used to be a private, un-exported function — see this file's own
// header for why that made the extraction a real prerequisite rather than a
// tidy-up (Step 6's register: "a test file cannot even import it").
//
// Two properties, each with its own describe block:
//  1. The ordinary pane/inline variant contract every other extracted
//     renderer already has a `*.variant.test.tsx`-shaped test for
//     (LibraryImagePreview.test.tsx is the template).
//  2. THE property Step 6 names as the false-green risk: a copy left behind
//     in the pane. A test that only imports this module and renders it
//     proves the module exists — it does not prove the pane stopped using a
//     private copy of its own. The "single-definition-used-by-both-surfaces"
//     block below intercepts THIS module (not a second one) and asserts both
//     `LibraryPreviewPane`'s audio branch and the inline embed mount
//     (`KbAudioEmbedMount`) render through it. If either one imported (or
//     reintroduced) a private duplicate instead, the mock below would never
//     see that consumer's render, and the corresponding assertion fails —
//     this is the mutation-tested case in the task report.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

const ENTRY: LibraryEntry = {
  name: 'podcast.mp3',
  path: 'audio/podcast.mp3',
  is_dir: false,
  is_hidden: false,
  size: 4096,
  modified_at: '2026-08-22T10:15:00Z',
  is_text_editable: false,
}

describe('LibraryAudioPreview — pane variant (default, unchanged from before extraction)', () => {
  it('renders the download URL as the audio source', async () => {
    const { LibraryAudioPreview } = await import('./LibraryAudioPreview')
    render(<LibraryAudioPreview workspaceId="ws-1" entry={ENTRY} />)
    const wrap = screen.getByTestId('library-audio-preview')
    const audio = wrap.querySelector('audio')
    expect(audio).toHaveAttribute('controls')
    expect(audio).toHaveAttribute('src', libraryDownloadUrl('ws-1', ENTRY.path))
  })

  it('carries data-variant="pane" when no variant prop is given', async () => {
    const { LibraryAudioPreview } = await import('./LibraryAudioPreview')
    render(<LibraryAudioPreview workspaceId="ws-1" entry={ENTRY} />)
    expect(screen.getByTestId('library-audio-preview')).toHaveAttribute('data-variant', 'pane')
  })
})

describe('LibraryAudioPreview — inline variant (EMB-027: same renderer, EMB-028: layout only)', () => {
  it('renders the identical src as the pane variant', async () => {
    const { LibraryAudioPreview } = await import('./LibraryAudioPreview')
    render(<LibraryAudioPreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    expect(screen.getByTestId('library-audio-preview').querySelector('audio')).toHaveAttribute(
      'src',
      libraryDownloadUrl('ws-1', ENTRY.path),
    )
  })

  it('draws its outermost container with DIFFERENT layout classes than the pane variant', async () => {
    const { LibraryAudioPreview } = await import('./LibraryAudioPreview')
    const pane = render(<LibraryAudioPreview workspaceId="ws-1" entry={ENTRY} variant="pane" />)
    const paneClass = pane.getByTestId('library-audio-preview').className
    pane.unmount()

    const inline = render(<LibraryAudioPreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    const inlineClass = inline.getByTestId('library-audio-preview').className
    inline.unmount()

    // MUTATION THIS DIES ON: a component that computes the same className
    // regardless of `variant`.
    expect(inlineClass).not.toBe(paneClass)
    expect(inlineClass).not.toContain('flex-1')
  })
})

describe('LibraryAudioPreview — no width modifier surface (EMB-030)', () => {
  it('accepts no width prop at all — there is nothing here for a |400 segment to apply to', async () => {
    const { LibraryAudioPreview } = await import('./LibraryAudioPreview')
    // @ts-expect-error — width is not a prop of this component, by design (EMB-030).
    render(<LibraryAudioPreview workspaceId="ws-1" entry={ENTRY} variant="inline" width={400} />)
    // Passing it anyway (a stray prop) must not surface as visible text —
    // the bug this task's brief names ("the size eats the display text").
    expect(screen.queryByText('400')).not.toBeInTheDocument()
  })
})

describe('LibraryAudioPreview — single definition used by both surfaces (Step 6 register, test 80)', () => {
  it('the pane and the inline embed both render through the SAME module', async () => {
    vi.resetModules()
    const mountedVariants: string[] = []

    vi.doMock('./LibraryAudioPreview', async (importOriginal) => {
      const actual = await importOriginal<typeof import('./LibraryAudioPreview')>()
      return {
        ...actual,
        LibraryAudioPreview: (props: Parameters<typeof actual.LibraryAudioPreview>[0]) => {
          mountedVariants.push(props.variant ?? 'pane')
          return actual.LibraryAudioPreview(props)
        },
      }
    })
    // Every OTHER static preview surface LibraryPreviewPane imports, stubbed
    // to a no-op so mounting it for an AUDIO entry pulls in none of their
    // real dependencies (mermaid, shiki, CodeMirror, BasePreview's view
    // machinery, pdfjs-dist).
    vi.doMock('./BasePreview', () => ({ BasePreview: () => null }))
    vi.doMock('./LibraryImagePreview', () => ({ LibraryImagePreview: () => null }))
    vi.doMock('./LibraryVideoPreview', () => ({ LibraryVideoPreview: () => null }))
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
        libraryDownloadUrl: (wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`,
        mintLibraryPreviewToken: vi.fn(),
        fetchLibraryContent: vi.fn(),
        fetchLibraryEntries: vi.fn().mockResolvedValue([ENTRY]),
      }
    })

    const { LibraryPreviewPane } = await import('../LibraryPreviewPane')
    const { KbAudioEmbedMount } = await import('./KbAudioEmbedMount')

    const qc = new QueryClient()
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
        <KbAudioEmbedMount workspaceId="ws-1" workspacePath="audio/podcast.mp3" />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(mountedVariants).toContain('inline'))
    embed.unmount()

    // Both consumers went through the ONE mocked module. If either one held
    // a private duplicate instead, its variant would never appear here.
    expect(mountedVariants).toEqual(expect.arrayContaining(['pane', 'inline']))

    vi.doUnmock('./LibraryAudioPreview')
    vi.doUnmock('./BasePreview')
    vi.doUnmock('./LibraryImagePreview')
    vi.doUnmock('./LibraryVideoPreview')
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
