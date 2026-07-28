// Tests for the workspace Media library tab (ADR-051 Rev 4 / Slice H).
//
// Covers: empty state, populated list rendering, the FR-008 delete flow
// (delete button → confirm dialog → deleteWorkspaceMedia → invalidation),
// and the D9/GAP4 QueryErrorState wiring: WorkspaceMediaTab.tsx:100-116 was
// migrated to the shared QueryErrorState with a new onRetry — this file
// previously only asserted the "workspace-media-error" testid still
// appeared (a hardcoded-error-div and a correctly-wired-retry component
// look identical on that assertion alone). The two describe blocks below
// close that gap, mirroring CalendarScreen.test.tsx's D9 "total query
// failure" suite (Retry re-invokes the fetch, error clears on success) and
// QueryErrorState.test.tsx's own forced-logout suppression coverage,
// applied here to prove WorkspaceMediaTab's actual onRetry prop is wired to
// refetch() rather than a no-op.

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

// Mirrors QueryErrorState.test.tsx's own mocking of isForceLoggingOut — used
// below to prove the suppression applies THROUGH WorkspaceMediaTab's actual
// QueryErrorState usage, not just the shared component in isolation.
const mockIsForceLoggingOut = vi.fn().mockReturnValue(false)
vi.mock('@/lib/authLogout', () => ({
  isForceLoggingOut: () => mockIsForceLoggingOut(),
}))

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
  mockIsForceLoggingOut.mockReturnValue(false)
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

// ── D9/GAP4: QueryErrorState onRetry wiring ─────────────────────────────────
//
// WorkspaceMediaTab.tsx:100-116 passes `onRetry={() => void refetch()}` to
// QueryErrorState. The test above only proves the error testid appears —
// that assertion cannot tell a correctly-wired onRetry apart from a
// hardcoded error div, or from `onRetry` silently omitted/mis-bound (which
// would still render the "workspace-media-error" div, just without a
// working Retry button). Mirrors CalendarScreen.test.tsx's D9 "total query
// failure" describe block (lines ~936-980: renders the error, Retry
// re-invokes the failed fetch(es), a successful retry clears the error and
// shows the real view) and WorkspaceTasksTab.test.tsx's identical pattern.
describe('WorkspaceMediaTab — error state Retry wiring (D9/GAP4)', () => {
  it('renders a working Retry action alongside the error state', async () => {
    mockedFetch.mockRejectedValue(new Error('boom'))
    renderTab()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-media-error')).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })

  it('clicking Retry re-invokes fetchWorkspaceMedia', async () => {
    mockedFetch.mockRejectedValue(new Error('boom'))
    renderTab()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-media-error')).toBeInTheDocument()
    })

    const callsBefore = mockedFetch.mock.calls.length
    mockedFetch.mockResolvedValueOnce([])

    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    await waitFor(() => {
      expect(mockedFetch.mock.calls.length).toBeGreaterThan(callsBefore)
    })
  })

  it('a successful retry clears the error state and shows the real view', async () => {
    mockedFetch.mockRejectedValueOnce(new Error('boom'))
    renderTab()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-media-error')).toBeInTheDocument()
    })

    mockedFetch.mockResolvedValueOnce([])
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    await waitFor(() => {
      expect(screen.queryByTestId('workspace-media-error')).not.toBeInTheDocument()
      expect(screen.getByTestId('workspace-media-empty')).toBeInTheDocument()
    })
  })
})

// ── D9/GAP4: forced-logout suppression ──────────────────────────────────────
//
// QueryErrorState.tsx renders null while isForceLoggingOut() is true (the
// deterministic-race fix: a 401 flips isError AND redirects to /login from
// the same event — this stops the media tab from flashing a stale
// actionable error for the beat before the bounce). QueryErrorState.test.tsx
// covers this for the shared component in isolation; these tests prove the
// suppression actually reaches the user THROUGH WorkspaceMediaTab's real
// usage — a regression that swapped in a differently-shaped error branch (or
// forgot to route through the shared component at all) would pass every
// other test in this file but fail here.
describe('WorkspaceMediaTab — forced-logout suppression (D9/GAP4)', () => {
  it('renders nothing (no stale error flash) while a forced logout is in flight', async () => {
    mockedFetch.mockRejectedValue(new Error('401 unauthorized'))
    mockIsForceLoggingOut.mockReturnValue(true)

    const { container } = renderTab()

    // The isLoading spinner is unaffected by isForceLoggingOut (it's a
    // separate branch, before QueryErrorState is even reached) — wait for
    // the query to actually settle into isError, THEN assert the error
    // branch itself renders nothing.
    await waitFor(() => {
      expect(screen.queryByText(/loading media library/i)).not.toBeInTheDocument()
    })
    expect(screen.queryByTestId('workspace-media-error')).not.toBeInTheDocument()
    expect(container.firstChild).toBeNull()
  })

  it('renders the real error state once isForceLoggingOut() returns false again', async () => {
    mockedFetch.mockRejectedValue(new Error('boom'))
    mockIsForceLoggingOut.mockReturnValue(false)

    renderTab()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-media-error')).toBeInTheDocument()
    })
  })
})
