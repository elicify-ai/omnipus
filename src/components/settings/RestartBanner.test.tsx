import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Mock the API layer before importing anything that uses it.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchPendingRestart: vi.fn(),
  }
})

// Mock auth store so we can control the role in each test.
vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn(),
}))

import { fetchPendingRestart } from '@/lib/api'
import { useAuthStore } from '@/store/auth'
import { RestartBanner } from './RestartBanner'
import type { PendingRestartEntry } from '@/lib/api'

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
    },
  })
}

function renderBanner(client: QueryClient = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <RestartBanner />
    </QueryClientProvider>
  )
}

function setRole(role: 'admin' | 'user' | null) {
  // Cast the implementation to never to satisfy the overloaded useAuthStore
  // signature; the selector receives only the fields the component reads.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.mocked(useAuthStore).mockImplementation(((selector: (s: { role: typeof role }) => unknown) =>
    selector({ role })) as any)
}

// ----------------------------------------------------------------------------
// Fixtures
// ----------------------------------------------------------------------------

const TWO_ENTRIES: PendingRestartEntry[] = [
  { key: 'sandbox.mode', applied_value: 'off', persisted_value: 'enforce' },
  { key: 'session.dm_scope', applied_value: 'per-channel-peer', persisted_value: 'main' },
]

// ----------------------------------------------------------------------------
// Setup
// ----------------------------------------------------------------------------

beforeEach(() => {
  vi.clearAllMocks()
  setRole('admin')
})

// ----------------------------------------------------------------------------
// TestRestartBanner_EmptyDiff_NotRendered
// ----------------------------------------------------------------------------

describe('RestartBanner — empty diff → not rendered', () => {
  it('does not mount the banner element when fetchPendingRestart returns []', async () => {
    vi.mocked(fetchPendingRestart).mockResolvedValue([])

    renderBanner()

    // Wait long enough for the query to settle.
    await waitFor(() => {
      expect(vi.mocked(fetchPendingRestart)).toHaveBeenCalled()
    })

    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})

// ----------------------------------------------------------------------------
// TestRestartBanner_NonEmptyDiff_RendersPlainSummary (US-B4)
// ----------------------------------------------------------------------------

describe('RestartBanner — non-empty diff → renders plain summary', () => {
  it('renders a plain one-line summary with change count', async () => {
    vi.mocked(fetchPendingRestart).mockResolvedValue(TWO_ENTRIES)

    renderBanner()

    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
    })

    // US-B4: plain summary is visible immediately
    const summary = screen.getByTestId('restart-banner-summary')
    expect(summary).toHaveTextContent(/2 changes? saved/i)
    expect(summary).toHaveTextContent(/restart to apply/i)
  })

  it('jargon diff rows are hidden until "Technical details" is expanded', async () => {
    vi.mocked(fetchPendingRestart).mockResolvedValue(TWO_ENTRIES)

    renderBanner()

    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
    })

    // The raw key diff (sandbox.mode: off → enforce) must NOT be visible yet.
    expect(screen.queryByText('sandbox.mode:')).not.toBeInTheDocument()
    expect(screen.queryByText('session.dm_scope:')).not.toBeInTheDocument()
    // The systemd/docker jargon must NOT be visible yet.
    expect(screen.queryByText(/systemd/i)).not.toBeInTheDocument()

    // Expand "Technical details"
    const techToggle = screen.getByTestId('restart-banner-tech-toggle')
    fireEvent.click(techToggle)

    // Now the diff rows are visible.
    expect(screen.getByText('sandbox.mode:')).toBeInTheDocument()
    expect(screen.getByText('off')).toBeInTheDocument()
    expect(screen.getByText('enforce')).toBeInTheDocument()

    expect(screen.getByText('session.dm_scope:')).toBeInTheDocument()
    expect(screen.getByText('per-channel-peer')).toBeInTheDocument()
    expect(screen.getByText('main')).toBeInTheDocument()

    // systemd text appears in the details
    expect(screen.getByText(/systemd/i)).toBeInTheDocument()
  })

  it('renders singular "1 change" when only one entry', async () => {
    vi.mocked(fetchPendingRestart).mockResolvedValue([TWO_ENTRIES[0]])

    renderBanner()

    await waitFor(() => {
      expect(screen.getByTestId('restart-banner-summary')).toBeInTheDocument()
    })

    const summary = screen.getByTestId('restart-banner-summary')
    expect(summary).toHaveTextContent(/1 change saved/i)
  })
})

// ----------------------------------------------------------------------------
// TestRestartBanner_SetThenRevert_Clears
// ----------------------------------------------------------------------------

describe('RestartBanner — set-then-revert clears banner', () => {
  it('hides the banner when a subsequent poll returns []', async () => {
    // First call returns a diff; second call returns empty.
    vi.mocked(fetchPendingRestart)
      .mockResolvedValueOnce(TWO_ENTRIES)
      .mockResolvedValue([])

    const client = makeClient()

    renderBanner(client)

    // Banner appears on first fetch.
    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
    })

    // Manually invalidate to trigger a re-fetch (simulates the 10s poll).
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['pending-restart'] })
    })

    // Banner disappears when diff is empty.
    await waitFor(() => {
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    })
  })
})

// ----------------------------------------------------------------------------
// TestRestartBanner_NonAdmin_Hidden
// ----------------------------------------------------------------------------

describe('RestartBanner — non-admin → hidden', () => {
  it('renders nothing for role "user" even with a non-empty diff', async () => {
    setRole('user')
    vi.mocked(fetchPendingRestart).mockResolvedValue(TWO_ENTRIES)

    renderBanner()

    // The inner query component is not mounted for non-admin users so the
    // API is never called and the banner is never in the DOM.
    expect(fetchPendingRestart).not.toHaveBeenCalled()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})

// ----------------------------------------------------------------------------
// TestRestartBanner_DevModeBypass_Hidden
// ----------------------------------------------------------------------------

describe('RestartBanner — dev_mode_bypass → hidden', () => {
  it('hides banner when fetchPendingRestart rejects (503 under dev_mode_bypass)', async () => {
    vi.mocked(fetchPendingRestart).mockRejectedValue(new Error('Service Unavailable'))

    renderBanner()

    await waitFor(() => {
      expect(vi.mocked(fetchPendingRestart)).toHaveBeenCalled()
    })

    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})

// ----------------------------------------------------------------------------
// TestRestartBanner_ComplexValue_ShowsModified
// ----------------------------------------------------------------------------

describe('RestartBanner — complex value → (modified)', () => {
  it('shows "(modified)" for object persisted_value instead of stringifying it', async () => {
    const complexEntry: PendingRestartEntry[] = [
      {
        key: 'gateway.users',
        applied_value: { count: 3 },
        persisted_value: { count: 4 },
      },
    ]
    vi.mocked(fetchPendingRestart).mockResolvedValue(complexEntry)

    renderBanner()

    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
    })

    // Expand technical details to see the diff rows.
    const techToggle = screen.getByTestId('restart-banner-tech-toggle')
    fireEvent.click(techToggle)

    // Both applied and persisted are objects → both shown as "(modified)".
    const modifiedTexts = screen.getAllByText('(modified)')
    expect(modifiedTexts.length).toBe(2)

    // Should NOT contain raw JSON.
    expect(screen.queryByText(/\{/)).not.toBeInTheDocument()
  })
})

// ----------------------------------------------------------------------------
// TestRestartBanner_RefetchAfterSave
// ----------------------------------------------------------------------------

describe('RestartBanner — refetches after save action', () => {
  it('calls fetchPendingRestart again within 500ms when queryClient.invalidateQueries is called', async () => {
    vi.mocked(fetchPendingRestart).mockResolvedValue(TWO_ENTRIES)

    const client = makeClient()
    renderBanner(client)

    // Wait for initial fetch.
    await waitFor(() => {
      expect(vi.mocked(fetchPendingRestart)).toHaveBeenCalledTimes(1)
    })

    const before = Date.now()

    // Simulate "save action" by invalidating the query (same mechanism the parent
    // save-action handlers would call).
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['pending-restart'] })
    })

    await waitFor(() => {
      expect(vi.mocked(fetchPendingRestart)).toHaveBeenCalledTimes(2)
    })

    expect(Date.now() - before).toBeLessThan(500)
  })
})
