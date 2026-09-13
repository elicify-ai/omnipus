// LibraryPreviewPane.refetchError.test.tsx — UAT D-98 (2026-09-13), the
// second half: a background refetch of the content query that FAILS while
// an editor is open must not replace the editor (and the reader's unsaved
// text) with "Could not load this file. / Retry".
//
// TanStack keeps `data` and flips `isError` on a failed refetch; the pane
// used to key its error state on `isError` alone. This test opens the
// editor, types, makes the next read fail, forces a refetch, and asserts the
// textarea — with the typed text — is still there.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry, LibraryContentResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryContent: vi.fn(),
    putLibraryContent: vi.fn(),
    fetchLibraryContentVersioned: vi.fn(),
    mintLibraryPreviewToken: vi.fn(),
  }
})

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))

vi.mock('@uiw/react-codemirror', () => ({
  default: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea data-testid="library-editor-textarea" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}))

import { fetchLibraryContent, fetchLibraryContentVersioned, libraryQueryKeys } from '@/lib/api'
import { LibraryPreviewPane } from './LibraryPreviewPane'

const mockedFetchContent = vi.mocked(fetchLibraryContent)
const mockedFetchContentVersioned = vi.mocked(fetchLibraryContentVersioned)

const entry: LibraryEntry = {
  name: 'notes.txt',
  path: 'notes.txt',
  is_dir: false,
  is_hidden: false,
  size: 20,
  modified_at: '2026-07-28T10:15:00Z',
  mime: 'text/plain',
  is_text_editable: true,
}

function content(): LibraryContentResponse {
  return { path: 'notes.txt', content: 'first draft', size: 11, is_text: true, too_large: false }
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  mockedFetchContentVersioned.mockImplementation(async (ws, path) => ({
    data: await mockedFetchContent(ws, path),
    version: 'v1:default',
  }))
})

describe('LibraryPreviewPane — D-98 a failed background refetch keeps the editor', () => {
  it('does not replace an open editor with the load-error state when a refetch fails', async () => {
    mockedFetchContent.mockResolvedValue(content())
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <LibraryPreviewPane workspaceId="ws-1" entry={entry} onClose={vi.fn()} onDownload={vi.fn()} mintPreviewToken={null} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(screen.getByTestId('library-preview-mode-edit')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-preview-mode-edit'))
    const textarea = (await screen.findByTestId('library-editor-textarea')) as HTMLTextAreaElement
    fireEvent.change(textarea, { target: { value: 'first draft — plus the paragraph typed offline' } })

    // The network goes away; the next read of this file fails.
    mockedFetchContent.mockRejectedValue(new Error('offline'))
    await act(async () => {
      await client.refetchQueries({ queryKey: libraryQueryKeys.content('ws-1', 'notes.txt') })
    })
    await waitFor(() => expect(client.getQueryState(libraryQueryKeys.content('ws-1', 'notes.txt'))?.status).toBe('error'))

    // The editor and the typed text survive; the error state never mounts.
    expect(screen.queryByTestId('library-content-error')).not.toBeInTheDocument()
    const still = screen.getByTestId('library-editor-textarea') as HTMLTextAreaElement
    expect(still.value).toBe('first draft — plus the paragraph typed offline')
  })

  it('still shows the load-error state when the FIRST read fails (nothing on screen to protect)', async () => {
    mockedFetchContent.mockRejectedValue(new Error('offline'))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <LibraryPreviewPane workspaceId="ws-1" entry={entry} onClose={vi.fn()} onDownload={vi.fn()} mintPreviewToken={null} />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('library-content-error')).toBeInTheDocument())
  })
})
