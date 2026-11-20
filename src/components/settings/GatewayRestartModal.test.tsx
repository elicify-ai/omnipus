/**
 * GatewayRestartModal.test.tsx — O4 gateway restart UX.
 *
 * Settings redesign #1519 reconciliation: the completion check is FR-063 now.
 * The old oracle (first 2xx from /health, "restarted successfully", "did not
 * come back online") is replaced — completion must be proved by the page's
 * own GET /about showing an `uptime_seconds` smaller than the seconds since
 * Restart now was pressed (the first answer from the old process never
 * counts), then GET /config/pending-restart no longer listing the restarted
 * keys; a timeout must say the gateway did not come back or did not restart,
 * to check that it is running and read its log.
 *
 * Covers:
 *  - the FR-033 window's Later and Restart now buttons render when open.
 *  - POST failure surfaces an error message in the modal.
 *  - Timeout branch: an old process that keeps answering never counts as
 *    restarted (FR-063) — the modal says it did not come back, to check that
 *    it is running and read its log.
 *  - Happy path: old-process answer ignored → fresh uptime → pending-restart
 *    no longer lists the keys → "The gateway restarted." + toast + closes.
 *  - The authed gatewayRestart() helper is used (not a raw fetch).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mock the API module ────────────────────────────────────────────────────────
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    gatewayRestart: vi.fn(),
    fetchAboutInfo: vi.fn(),
    fetchPendingRestart: vi.fn(),
    isApiError: actual.isApiError,
  }
})

// ── Mock the restart hook ──────────────────────────────────────────────────────
const mockRefetchPending = vi.fn().mockResolvedValue(undefined)
vi.mock('@/hooks/restart', () => ({
  usePendingRestart: () => ({ refetch: mockRefetchPending }),
}))

// ── Mock the UI store ──────────────────────────────────────────────────────────
const addToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast }),
}))

import * as api from '@/lib/api'
import { GatewayRestartModal } from './GatewayRestartModal'

// ── Raw-fetch guard ────────────────────────────────────────────────────────────
// FR-063 moved completion checking to the authed GET /about and
// GET /config/pending-restart reads (mocked above). The stub stays as a
// guard: no raw fetch may carry the restart request itself, and only the
// port-change branch ("direct") may ever poll /health.
const fetchSpy = vi.fn()
vi.stubGlobal('fetch', fetchSpy)

function renderModal(props: { open: boolean; onClose: () => void }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <GatewayRestartModal {...props} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  // By default the page's own /about answers with a fresh process (FR-063:
  // uptime_seconds smaller than the seconds since Restart now was pressed)
  // and nothing is listed as waiting for a restart any more.
  vi.mocked(api.fetchAboutInfo).mockResolvedValue({
    version: '0.1.1',
    go_version: 'go1.24',
    os: 'linux',
    arch: 'amd64',
    uptime_seconds: 0,
  })
  vi.mocked(api.fetchPendingRestart).mockResolvedValue([])
})

afterEach(() => {
  vi.useRealTimers()
})

describe('GatewayRestartModal', () => {
  it('renders the restart now and later buttons when open', () => {
    renderModal({ open: true, onClose: vi.fn() })
    expect(screen.getByRole('button', { name: /restart now/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /later/i })).toBeInTheDocument()
  })

  it('POST failure surfaces an error message in the modal', async () => {
    const { ApiError } = api
    vi.mocked(api.gatewayRestart).mockRejectedValue(new ApiError(500, 'internal error'))
    const onClose = vi.fn()
    renderModal({ open: true, onClose })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /restart now/i }))
    })

    await waitFor(() => {
      // Error message is rendered in the modal.
      expect(screen.getByText(/internal error/i)).toBeInTheDocument()
    })
    // Modal stays open on error.
    expect(onClose).not.toHaveBeenCalled()
    // Toast is NOT shown for an error (error is surfaced inline).
    expect(addToast).not.toHaveBeenCalled()
  })

  it('timeout branch: an old process that keeps answering never counts — says it did not come back, to check the gateway and read its log (FR-063)', async () => {
    // Strategy: the page's own /about keeps answering with an OLD process's
    // uptime (uptime_seconds far above the seconds since the press — FR-063:
    // the first answer from the old process never counts), then the fake
    // clock jumps past the 60-second deadline so the poll loop exits.
    //
    // We use vi.useFakeTimers({ toFake: ['Date'] }) to control only the Date
    // object, leaving setTimeout/setInterval real so waitFor continues to work.
    vi.useFakeTimers({ toFake: ['Date'] })

    vi.mocked(api.gatewayRestart).mockResolvedValue({
      status: 'restarting',
      restart_id: 'test-id',
      drain_seconds: 1,
    })
    // An old process, still answering with a huge uptime, forever.
    vi.mocked(api.fetchAboutInfo).mockResolvedValue({
      version: '0.1.1',
      go_version: 'go1.24',
      os: 'linux',
      arch: 'amd64',
      uptime_seconds: 999_999,
    })

    const onClose = vi.fn()
    renderModal({ open: true, onClose })

    // Click restart. After the POST resolves, the fresh-process poll starts
    // and performs its first Date.now() call to set the deadline.
    fireEvent.click(screen.getByRole('button', { name: /restart now/i }))

    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
      // Advance the fake clock by 61 seconds so the while-condition evaluates
      // to false on the next Date.now() call — the 60-second window is spent.
      vi.advanceTimersByTime(61_000)
    })

    await waitFor(() => {
      // FR-063: say the gateway did not come back or did not restart, to
      // check that it is running and read its log.
      expect(screen.getByText(/did not come back/i)).toBeInTheDocument()
      expect(screen.getByText(/read its log/i)).toBeInTheDocument()
    }, { timeout: 5000 })

    expect(onClose).not.toHaveBeenCalled()
  })

  it('happy path: old-process answer ignored, fresh uptime proves the restart, pending list clears, toast + close (FR-063)', async () => {
    vi.mocked(api.gatewayRestart).mockResolvedValue({
      status: 'restarting',
      restart_id: 'restart-abc',
      drain_seconds: 2,
    })
    // The page's own /about: the FIRST answer is still the old process
    // (uptime far above the elapsed seconds — it never counts, FR-063); the
    // next answers come from the fresh process.
    vi.mocked(api.fetchAboutInfo)
      .mockResolvedValueOnce({
        version: '0.1.1',
        go_version: 'go1.24',
        os: 'linux',
        arch: 'amd64',
        uptime_seconds: 100_000,
      })
      .mockResolvedValue({
        version: '0.1.1',
        go_version: 'go1.24',
        os: 'linux',
        arch: 'amd64',
        uptime_seconds: 0,
      })
    // GET /config/pending-restart no longer lists the restarted keys.
    vi.mocked(api.fetchPendingRestart).mockResolvedValue([])

    const onClose = vi.fn()
    renderModal({ open: true, onClose })

    fireEvent.click(screen.getByRole('button', { name: /restart now/i }))

    // Wait for the modal to reach the success state.
    await waitFor(() => {
      expect(screen.getByText('The gateway restarted.')).toBeInTheDocument()
    }, { timeout: 5000 })

    // Success toast was shown.
    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'success', message: expect.stringMatching(/restarted/i) }),
    )
    // Pending restart list was refetched.
    expect(mockRefetchPending).toHaveBeenCalled()
    // Modal closes after the success pause (1.2 s in the component).
    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    }, { timeout: 3000 })
  })

  it('uses the authed gatewayRestart() helper, not raw fetch, for the POST', async () => {
    vi.mocked(api.gatewayRestart).mockResolvedValue({
      status: 'restarting',
      restart_id: 'x',
      drain_seconds: 1,
    })
    renderModal({ open: true, onClose: vi.fn() })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /restart now/i }))
    })

    // The authed helper was called.
    expect(api.gatewayRestart).toHaveBeenCalledTimes(1)
    // fetch() should only have been called for /health polling, never for
    // the restart endpoint directly.
    const restartFetchCalls = fetchSpy.mock.calls.filter(
      (args: unknown[]) => typeof args[0] === 'string' && (args[0] as string).includes('restart'),
    )
    expect(restartFetchCalls).toHaveLength(0)
  })

  it('Later button closes the modal without firing the restart', () => {
    vi.mocked(api.gatewayRestart).mockResolvedValue({
      status: 'restarting',
      restart_id: 'x',
      drain_seconds: 1,
    })
    const onClose = vi.fn()
    renderModal({ open: true, onClose })

    fireEvent.click(screen.getByRole('button', { name: /later/i }))

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(api.gatewayRestart).not.toHaveBeenCalled()
  })
})
