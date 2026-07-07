/**
 * GodModeControl.test.tsx — O14 god-mode switch (UI-driven enablement).
 *
 * Covers:
 *  - the toggle reflects live state from GodModeStatus ({ enabled, available, supported })
 *  - flipping the toggle opens the step-up (re-auth) dialog and does NOT call
 *    the god-mode endpoint until the password is confirmed
 *  - confirming the password replays the minted consent token into setGodMode
 *  - the toggle is clickable whenever `supported` is true, even when
 *    `available` is false (the UI-driven enablement flow)
 *  - a nogodmode build (supported=false) disables the toggle with an
 *    explanatory "compiled out" note
 *  - a supported-but-unauthorized boot (supported=true, available=false)
 *    shows a "restart to activate" note instead of "compiled out"
 *  - enabling with restart_required=true opens GatewayRestartModal
 *  - disabling (restart_required=false) never opens GatewayRestartModal
 *  - the active-state banner renders only when god-mode is on
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

// GatewayRestartModal (rendered by GodModeControl) reads the pending-restart
// hook; mock it so the nested modal doesn't fire a real network query.
const mockRefetchPending = vi.fn().mockResolvedValue(undefined)
vi.mock('@/hooks/restart', () => ({
  usePendingRestart: () => ({ refetch: mockRefetchPending }),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchGodMode: vi.fn(),
    setGodMode: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { GodModeControl, GodModeActiveBanner } from './GodModeControl'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderControl() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <GodModeControl />
    </QueryClientProvider>,
  )
}

function renderBanner() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <GodModeActiveBanner />
    </QueryClientProvider>,
  )
}

// GodModeStatus = { enabled, available, supported }
const STATE_OFF = { enabled: false, available: true, supported: true }
const STATE_ON = { enabled: true, available: true, supported: true }
// Build supports god mode, but this boot was never authorized (no
// --allow-god-mode, no prior UI-enable + restart). The toggle must still be
// clickable — this is the state the UI-driven enablement flow starts from.
const STATE_SUPPORTED_UNAVAILABLE = { enabled: false, available: false, supported: true }
// nogodmode build — god mode does not exist in this binary at all.
const STATE_COMPILED_OUT = { enabled: false, available: false, supported: false }

beforeEach(() => {
  vi.clearAllMocks()
})

describe('GodModeControl', () => {
  it('renders the toggle off when god-mode is disabled', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-toggle')).toHaveAttribute('aria-checked', 'false')
    })
  })

  it('renders the toggle on when god-mode is enabled', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_ON)
    renderControl()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-toggle')).toHaveAttribute('aria-checked', 'true')
    })
  })

  it('flipping the toggle opens the step-up dialog and does NOT call setGodMode yet', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(api.setGodMode).not.toHaveBeenCalled()
  })

  it('replays the minted consent token into setGodMode after password confirm', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: true, restart_required: false })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.setGodMode).toHaveBeenCalledWith(true, 'reauth_tok')
    })
  })

  it('is clickable when supported but not yet available (UI-driven enablement)', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_SUPPORTED_UNAVAILABLE)
    renderControl()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-restart-note')).toBeInTheDocument()
    })
    expect(screen.getByTestId('god-mode-toggle')).toBeEnabled()
    expect(screen.queryByTestId('god-mode-unavailable-note')).not.toBeInTheDocument()
  })

  it('disables the toggle and shows the compiled-out note on a nogodmode build', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_COMPILED_OUT)
    renderControl()
    // The note only renders once the query has settled (supported=false &&
    // !isLoading); wait for it, then assert the toggle is disabled.
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-unavailable-note')).toBeInTheDocument()
    })
    expect(screen.getByText(/compiled out/i)).toBeInTheDocument()
    expect(screen.getByTestId('god-mode-toggle')).toBeDisabled()
    expect(screen.queryByTestId('god-mode-restart-note')).not.toBeInTheDocument()
  })

  // Regression: a fetch failure must show a distinct error note (not the
  // "compiled out" copy, which implies a known, permanent build fact) and
  // must not disable the toggle purely because the fetch failed — the
  // operator should still be able to attempt the change; the mutation's own
  // onError handling covers a genuinely unsupported backend.
  it('shows a distinct fetch-error note (not "compiled out") and does not disable the toggle on fetch failure', async () => {
    vi.mocked(api.fetchGodMode).mockRejectedValue(new Error('network error'))
    renderControl()

    await waitFor(() => {
      expect(screen.getByTestId('god-mode-fetch-error-note')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('god-mode-unavailable-note')).not.toBeInTheDocument()
    expect(screen.getByTestId('god-mode-toggle')).toBeEnabled()
  })

  it('does not call setGodMode if the step-up dialog is cancelled', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    await waitFor(() => screen.getByTestId('reauth-confirm'))

    fireEvent.click(screen.getByRole('button', { name: /Cancel/i }))
    await waitFor(() => {
      expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    })
    expect(api.setGodMode).not.toHaveBeenCalled()
  })

  it('opens GatewayRestartModal when enabling returns restart_required=true', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_SUPPORTED_UNAVAILABLE)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: true, restart_required: true })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.setGodMode).toHaveBeenCalledWith(true, 'reauth_tok')
    })
    await waitFor(() => {
      expect(screen.getByText(/gateway restart required/i)).toBeInTheDocument()
    })
  })

  it('does NOT open GatewayRestartModal when disabling (restart_required=false)', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_ON)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: false, restart_required: false })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.setGodMode).toHaveBeenCalledWith(false, 'reauth_tok')
    })
    expect(screen.queryByText(/gateway restart required/i)).not.toBeInTheDocument()
  })
})

describe('GodModeActiveBanner', () => {
  it('renders the banner when god-mode is active', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_ON)
    renderBanner()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-active-banner')).toBeInTheDocument()
    })
    expect(screen.getByText(/God-mode is active/i)).toBeInTheDocument()
  })

  it('renders nothing when god-mode is off', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    const { container } = renderBanner()
    // Give the query a tick to settle.
    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-active-banner')).not.toBeInTheDocument()
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  // Regression: a fetch failure must NOT collapse to the same falsy state as
  // "god-mode is genuinely off" — the banner must show an explicit
  // status-unknown indicator instead of silently rendering nothing, since
  // silence here would look exactly like "sandboxing is confirmed on".
  it('shows a status-unknown banner (not nothing) when the fetch fails', async () => {
    vi.mocked(api.fetchGodMode).mockRejectedValue(new Error('network error'))
    renderBanner()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-status-unknown-banner')).toBeInTheDocument()
    })
    expect(screen.getByText(/god-mode status unavailable/i)).toBeInTheDocument()
    expect(screen.queryByTestId('god-mode-active-banner')).not.toBeInTheDocument()
  })
})
