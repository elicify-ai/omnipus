/**
 * GatewayRestartModal.test.tsx — O4 gateway restart UX.
 *
 * Covers:
 *  - POST failure surfaces an error message in the modal.
 *  - Poll timeout branch: the "did not come back online" error path.
 *  - Happy path: down→up resolves + shows success toast + closes modal.
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

// ── Fake /health poller ────────────────────────────────────────────────────────
// The modal calls fetch('/health') to poll liveness. We control it via a spy.
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
  // By default, /health comes back up quickly.
  fetchSpy.mockResolvedValue({ ok: true })
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

  it('poll timeout branch: surfaces "did not come back online" error', async () => {
    // Strategy: make /health immediately reject, then use vi.setSystemTime
    // to place the clock 61 seconds ahead between the first and second
    // Date.now() calls inside pollUntilOnline so the while-condition fails.
    //
    // pollUntilOnline flow:
    //  1. deadline = Date.now() + 60_000      ← call at T
    //  2. while (Date.now() < deadline)       ← call at T+61_000 → false → exit
    //  3. throw new Error(...)
    //
    // We use vi.useFakeTimers({ toFake: ['Date'] }) to control only the Date
    // object, leaving setTimeout/setInterval real so waitFor continues to work.
    vi.useFakeTimers({ toFake: ['Date'] })

    vi.mocked(api.gatewayRestart).mockResolvedValue({
      status: 'restarting',
      restart_id: 'test-id',
      drain_seconds: 1,
    })
    // /health always rejects — gateway never comes back.
    fetchSpy.mockRejectedValue(new Error('ECONNREFUSED'))

    const onClose = vi.fn()
    renderModal({ open: true, onClose })

    // Click restart. After the POST resolves, pollUntilOnline is called and
    // performs its first Date.now() call to set the deadline.
    fireEvent.click(screen.getByRole('button', { name: /restart now/i }))

    // Wait briefly for gatewayRestart to resolve and pollUntilOnline to start
    // (i.e. for the first Date.now() to be called and the deadline to be set).
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
      // Advance the fake clock by 61 seconds so the while-condition evaluates
      // to false on the NEXT Date.now() call (the while check after the first
      // failed /health attempt).
      vi.advanceTimersByTime(61_000)
    })

    await waitFor(() => {
      expect(screen.getByText(/did not come back online/i)).toBeInTheDocument()
    }, { timeout: 5000 })

    expect(onClose).not.toHaveBeenCalled()
  })

  it('happy path: down→up resolves, shows success toast, closes modal', async () => {
    vi.mocked(api.gatewayRestart).mockResolvedValue({
      status: 'restarting',
      restart_id: 'restart-abc',
      drain_seconds: 2,
    })
    // /health: first call fails (gateway going down), second call succeeds.
    fetchSpy
      .mockRejectedValueOnce(new Error('ECONNREFUSED'))
      .mockResolvedValue({ ok: true })

    const onClose = vi.fn()
    renderModal({ open: true, onClose })

    fireEvent.click(screen.getByRole('button', { name: /restart now/i }))

    // Wait for the modal to reach the success state.
    await waitFor(() => {
      expect(screen.getByText(/restarted successfully/i)).toBeInTheDocument()
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
