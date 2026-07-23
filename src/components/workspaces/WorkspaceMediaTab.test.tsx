// Tests for the workspace Media library tab (ADR-051 Rev 4 / Slice H).
//
// Covers: empty state, populated list rendering, and the FR-008 delete flow
// (delete button → confirm dialog → deleteWorkspaceMedia → invalidation).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspaceMediaTab } from './WorkspaceMediaTab'
import { useUiStore } from '@/store/ui'
import type { MediaLibraryEntry } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaceMedia: vi.fn(),
    deleteWorkspaceMedia: vi.fn(),
  }
})

import { fetchWorkspaceMedia, deleteWorkspaceMedia } from '@/lib/api'

const mockedFetch = vi.mocked(fetchWorkspaceMedia)
const mockedDelete = vi.mocked(deleteWorkspaceMedia)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makeEntry(over: Partial<MediaLibraryEntry> = {}): MediaLibraryEntry {
  return {
    id: 'm-1',
    workspace_id: 'ws-1',
    filename: 'diagram.png',
    mime: 'image/png',
    size: 204800,
    sha256: 'a'.repeat(64),
    uploaded_at: '2026-07-22T14:22:00Z',
    source: 'user_upload',
    ...over,
  } as MediaLibraryEntry
}

function renderTab(workspaceId = 'ws-1') {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceMediaTab workspaceId={workspaceId} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => useUiStore.setState({ toasts: [] }))
})

describe('WorkspaceMediaTab', () => {
  it('renders the empty state when the library has no entries', async () => {
    mockedFetch.mockResolvedValue([])
    renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('workspace-media-empty')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('workspace-media-tab')).not.toBeInTheDocument()
  })

  it('lists library entries with filename + size', async () => {
    mockedFetch.mockResolvedValue([
      makeEntry({ id: 'm-1', filename: 'diagram.png', size: 204800 }),
      makeEntry({ id: 'm-2', filename: 'report.pdf', size: 1024, mime: 'application/pdf' }),
    ])
    renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('media-row-m-1')).toBeInTheDocument()
      expect(screen.getByTestId('media-row-m-2')).toBeInTheDocument()
    })
    expect(screen.getByText('diagram.png')).toBeInTheDocument()
    expect(screen.getByText('report.pdf')).toBeInTheDocument()
  })

  it('calls fetchWorkspaceMedia with the workspace id', async () => {
    mockedFetch.mockResolvedValue([])
    renderTab('ws-42')
    await waitFor(() => expect(mockedFetch).toHaveBeenCalledWith('ws-42'))
  })

  it('deletes an entry on confirm (FR-008) and removes it from the list', async () => {
    mockedFetch.mockResolvedValueOnce([makeEntry({ id: 'm-1', filename: 'diagram.png' })])
    // After invalidation the list refetches with the entry gone.
    mockedFetch.mockResolvedValueOnce([])
    mockedDelete.mockResolvedValue(undefined)

    renderTab()
    await waitFor(() => expect(screen.getByTestId('media-row-m-1')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('media-delete-m-1'))
    // Confirm dialog appears.
    await waitFor(() => expect(screen.getByTestId('media-delete-confirm')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('media-delete-confirm'))

    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith('ws-1', 'm-1'))
    // Row is gone after refetch.
    await waitFor(() => expect(screen.queryByTestId('media-row-m-1')).not.toBeInTheDocument())
  })

  it('shows an error state when the list fetch fails', async () => {
    mockedFetch.mockRejectedValue(new Error('boom'))
    renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('workspace-media-error')).toBeInTheDocument()
    })
  })
})
