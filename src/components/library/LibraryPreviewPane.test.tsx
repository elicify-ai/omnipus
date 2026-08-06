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

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry, LibraryContentResponse } from '@/lib/api'

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

import { fetchLibraryContent, putLibraryContent } from '@/lib/api'
import { LibraryPreviewPane } from './LibraryPreviewPane'

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

interface RenderPaneHandlers {
  onClose?: () => void
  onDownload?: (entry: LibraryEntry) => void
}

function renderPane(entry: LibraryEntry, handlers: RenderPaneHandlers = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <LibraryPreviewPane
        workspaceId="ws-1"
        entry={entry}
        onClose={handlers.onClose ?? vi.fn()}
        onDownload={handlers.onDownload ?? vi.fn()}
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
