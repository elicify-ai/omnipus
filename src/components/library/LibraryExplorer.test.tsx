// LibraryExplorer.test.tsx — the file-explorer core both Library entry
// points render (library-spec.md D-2/D-3). Covers: virtual-root workspace
// list, drilling into a workspace, the D-8 "Show Hidden" toggle revealing
// work/.library/, and the destructive-action (delete) confirm step.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryWorkspaces: vi.fn(),
    fetchLibraryEntries: vi.fn(),
    deleteLibraryEntry: vi.fn(),
    renameLibraryEntry: vi.fn(),
    moveLibraryEntry: vi.fn(),
    copyLibraryEntry: vi.fn(),
    uploadLibraryFiles: vi.fn(),
    libraryDownloadUrl: vi.fn((wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`),
  }
})

import {
  fetchLibraryWorkspaces,
  fetchLibraryEntries,
  deleteLibraryEntry,
} from '@/lib/api'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)
const mockedDelete = vi.mocked(deleteLibraryEntry)

import { LibraryExplorer } from './LibraryExplorer'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderExplorer(initialWorkspaceId?: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <LibraryExplorer initialWorkspaceId={initialWorkspaceId} onClose={vi.fn()} onPopOut={vi.fn()} />
    </QueryClientProvider>,
  )
}

function makeWorkspaceNode(over: Partial<LibraryWorkspaceNode> = {}): LibraryWorkspaceNode {
  return { id: 'ws-1', name: 'Website API', entry_count: 3, ...over }
}

function makeEntry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'report.md',
    path: 'report.md',
    is_dir: false,
    is_hidden: false,
    size: 2048,
    modified_at: '2026-07-28T10:15:00Z',
    mime: 'text/markdown',
    is_text_editable: true,
    ...over,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  act_setToasts()
})

function act_setToasts() {
  useUiStore.setState({ toasts: [] })
}

describe('LibraryExplorer — virtual root (sidebar entry point, D-3)', () => {
  it('lists every workspace as a top-level node when opened with no initial workspace', async () => {
    mockedFetchWorkspaces.mockResolvedValue([
      makeWorkspaceNode({ id: 'ws-1', name: 'Website API', entry_count: 3 }),
      makeWorkspaceNode({ id: 'ws-2', name: 'Marketing', entry_count: 0 }),
    ])

    renderExplorer(undefined)

    await waitFor(() => {
      expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument()
    })
    expect(screen.getByText('Website API')).toBeInTheDocument()
    expect(screen.getByText('Marketing')).toBeInTheDocument()
    expect(mockedFetchEntries).not.toHaveBeenCalled()
  })

  it('drills into a workspace on click, scoping subsequent listing calls to it (same component, D-3)', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-1', name: 'Website API' })])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'notes', path: 'notes', is_dir: true })])

    renderExplorer(undefined)

    await waitFor(() => expect(screen.getByTestId('library-workspace-node-ws-1')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-workspace-node-ws-1'))

    await waitFor(() => {
      expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '', false)
    })
    expect(screen.getByText('Website API')).toBeInTheDocument() // now the breadcrumb workspace crumb
  })
})

describe('LibraryExplorer — scoped entry point (chat/header bar, D-3)', () => {
  it('lands directly inside the given workspace when opened with an initialWorkspaceId', async () => {
    mockedFetchWorkspaces.mockResolvedValue([makeWorkspaceNode({ id: 'ws-9', name: 'Ops' })])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-9')

    await waitFor(() => {
      expect(mockedFetchEntries).toHaveBeenCalledWith('ws-9', '', false)
    })
    // No workspace-node buttons rendered — we start scoped, not at the root.
    expect(screen.queryByTestId('library-workspace-node-ws-9')).not.toBeInTheDocument()
  })
})

describe('LibraryExplorer — Show Hidden toggle (D-8)', () => {
  it('defaults to hiding dot-prefixed entries and include_hidden=false is sent', async () => {
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedFetchWorkspaces.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByText('report.md')).toBeInTheDocument())
    expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '', false)
    expect(screen.queryByText('.library')).not.toBeInTheDocument()
  })

  it('reveals work/.library/ once Show Hidden is toggled on — the whole point of the toggle', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockImplementation((_wsId, _path, includeHidden) =>
      Promise.resolve(
        includeHidden
          ? [
              makeEntry({ name: 'report.md', path: 'report.md' }),
              makeEntry({ name: '.library', path: '.library', is_dir: true, is_hidden: true, is_text_editable: false }),
            ]
          : [makeEntry({ name: 'report.md', path: 'report.md' })],
      ),
    )

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByText('report.md')).toBeInTheDocument())
    expect(screen.queryByText('.library')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('library-show-hidden-toggle'))

    await waitFor(() => {
      expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', '', true)
    })
    await waitFor(() => expect(screen.getByText('.library')).toBeInTheDocument())
    // Styled distinctly when shown (library-spec.md D-8: is_hidden entries
    // must be visually distinguishable even when the toggle reveals them).
    expect(screen.getByTestId('library-hidden-badge-.library')).toBeInTheDocument()
  })
})

describe('LibraryExplorer — destructive-action confirm (delete)', () => {
  it('selecting a file opens the details strip; Delete requires an explicit confirm before deleteLibraryEntry fires', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValueOnce([makeEntry({ name: 'report.md', path: 'report.md' })])
    mockedFetchEntries.mockResolvedValueOnce([]) // after invalidation, refetch shows it gone
    mockedDelete.mockResolvedValue(undefined)

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-report.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-report.md'))

    await waitFor(() => expect(screen.getByTestId('library-details-strip')).toBeInTheDocument())
    const strip = screen.getByTestId('library-details-strip')
    fireEvent.click(within(strip).getByRole('button', { name: /delete/i }))

    // The delete has NOT happened yet — a confirm dialog must appear first.
    expect(mockedDelete).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('library-delete-confirm'))

    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith('ws-1', 'report.md'))
    await waitFor(() => expect(screen.queryByTestId('library-row-report.md')).not.toBeInTheDocument())
    // Details strip closes once its entry is gone.
    expect(screen.queryByTestId('library-details-strip')).not.toBeInTheDocument()
  })

  it('cancelling the confirm dialog does NOT call deleteLibraryEntry', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([makeEntry({ name: 'draft.md', path: 'draft.md' })])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-row-draft.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('library-row-draft.md'))

    await waitFor(() => expect(screen.getByTestId('library-details-strip')).toBeInTheDocument())
    fireEvent.click(within(screen.getByTestId('library-details-strip')).getByRole('button', { name: /delete/i }))

    await waitFor(() => expect(screen.getByTestId('library-delete-confirm')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    await waitFor(() => expect(screen.queryByTestId('library-delete-confirm')).not.toBeInTheDocument())
    expect(mockedDelete).not.toHaveBeenCalled()
    expect(screen.getByTestId('library-row-draft.md')).toBeInTheDocument()
  })
})

describe('LibraryExplorer — empty and error states', () => {
  it('shows an empty state when a workspace has no visible entries', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockResolvedValue([])

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByText(/no files in this workspace yet/i)).toBeInTheDocument())
  })

  it('shows a retryable error state when listing entries fails', async () => {
    mockedFetchWorkspaces.mockResolvedValue([])
    mockedFetchEntries.mockRejectedValue(new Error('boom'))

    renderExplorer('ws-1')

    await waitFor(() => expect(screen.getByTestId('library-entries-error')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })
})
