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
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
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
    fetchAppState: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { ApiError } from '@/lib/api-error'
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

// GodModeStatus = { enabled, available, supported, persisted }
const STATE_OFF = { enabled: false, available: true, supported: true, persisted: false }
const STATE_ON = { enabled: true, available: true, supported: true, persisted: true }
// S0: fresh install, never armed. Build supports god mode, but this boot was
// never authorized (no --allow-god-mode, no prior UI-enable + restart), and
// nothing has ever been persisted. The toggle must still be clickable — this
// is the state the UI-driven enablement flow starts from — but it must NOT
// show the "restart to activate" note (D19): that note implies something WAS
// armed, which is false here.
const STATE_S0_NEVER_ARMED = { enabled: false, available: false, supported: true, persisted: false }
// S1: armed via the UI, pending restart. The config write succeeded
// (persisted=true) but this boot's authorization is frozen, so the override
// has no live effect yet (available=false, enabled=false). D1: the switch
// must render as ON/dangerous (bound to `persisted`, not `enabled`) and offer
// a disarm affordance, or an operator can never turn it back off from the UI.
const STATE_S1_ARMED_PENDING = { enabled: false, available: false, supported: true, persisted: true }
// nogodmode build — god mode does not exist in this binary at all.
const STATE_COMPILED_OUT = { enabled: false, available: false, supported: false, persisted: false }

// Platform mode (identity.mode: 'platform') pins useStepUp() to 'confirm' —
// the ConfirmDialog flow these tests cover. GodModeControl.password.test.tsx
// covers local mode.
const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as never

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
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

  // FR-OB-041/042: exactly Cancel and one confirm, no input of any kind, and
  // the confirm is not the destructive variant.
  it('flipping the toggle opens the confirmation and does NOT call setGodMode yet', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Enable god mode?')
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    const acceptButton = within(dialog).getByRole('button', { name: 'Enable god mode' })
    expect(acceptButton).toBeInTheDocument()
    expect(dialog.querySelectorAll('input, textarea, select')).toHaveLength(0)
    expect(acceptButton.className).not.toMatch(/color-error/)
    expect(api.setGodMode).not.toHaveBeenCalled()
  })

  it('confirming performs the save', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: true, restart_required: false })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    fireEvent.click(await screen.findByRole('button', { name: 'Enable god mode' }))

    await waitFor(() => {
      expect(api.setGodMode).toHaveBeenCalledWith(true)
    })
  })

  it('is clickable when supported but not yet available, and shows NO restart note when never armed (S0)', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_S0_NEVER_ARMED)
    renderControl()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-toggle')).toBeEnabled()
    })
    // D19: a fresh, never-touched install must not falsely claim god-mode is
    // "authorized but not yet active" — nothing has ever been armed.
    expect(screen.queryByTestId('god-mode-restart-note')).not.toBeInTheDocument()
    expect(screen.queryByTestId('god-mode-unavailable-note')).not.toBeInTheDocument()
    expect(screen.queryByTestId('god-mode-cancel-authorization')).not.toBeInTheDocument()
    expect(screen.getByTestId('god-mode-toggle')).toHaveAttribute('aria-checked', 'false')
  })

  it('shows the restart note AND a disarm affordance when armed via the UI but not yet available (S1)', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_S1_ARMED_PENDING)
    renderControl()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-restart-note')).toBeInTheDocument()
    })
    // D1: the switch reflects the persisted (armed) intent, not the inert
    // `enabled` flag — otherwise an armed-pending switch looks OFF.
    expect(screen.getByTestId('god-mode-toggle')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('god-mode-toggle')).toBeEnabled()
    expect(screen.queryByTestId('god-mode-unavailable-note')).not.toBeInTheDocument()
    expect(screen.getByTestId('god-mode-cancel-authorization')).toBeInTheDocument()
  })

  it('D1: clicking the main toggle from S1 (armed/pending) disarms — emits {enabled:false}, not a re-arm', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_S1_ARMED_PENDING)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: false, restart_required: false })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    // Before the fix, requestToggle computed `!enabled` — since `enabled` is
    // always false while `available` is false, this click would have staged
    // `true` again (a re-arm), even though the switch visually reads as ON.
    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    fireEvent.click(await screen.findByRole('button', { name: 'Disable god mode' }))

    await waitFor(() => {
      expect(api.setGodMode).toHaveBeenCalledWith(false)
    })
  })

  it('S1: "Cancel authorization" disarms via the existing setGodMode(false) confirmation flow', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_S1_ARMED_PENDING)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: false, restart_required: false })

    renderControl()
    await waitFor(() => screen.getByTestId('god-mode-cancel-authorization'))

    fireEvent.click(screen.getByTestId('god-mode-cancel-authorization'))
    fireEvent.click(await screen.findByRole('button', { name: 'Disable god mode' }))

    await waitFor(() => {
      expect(api.setGodMode).toHaveBeenCalledWith(false)
    })
    // No new backend endpoint — this replays the exact same setGodMode call
    // the main toggle would, just from a more discoverable affordance.
    expect(api.setGodMode).toHaveBeenCalledTimes(1)
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

  // pkg/gateway/rest_god_mode.go:49 gates GET god-mode with RequireNotBypass,
  // which returns 503 before the handler ever runs when dev_mode_bypass is
  // on — an expected "not available in this mode" response, not a transport
  // failure. Must show a distinct, quiet note, never the alarming
  // "gateway may be offline" fetch-error copy (which used to fire on every
  // dev-mode-bypass install, permanently).
  it('shows a quiet "not available while bypass is active" note (not the offline error) on a 503', async () => {
    vi.mocked(api.fetchGodMode).mockRejectedValue(
      new ApiError(503, 'this action is disabled while dev_mode_bypass is active'),
    )
    renderControl()

    await waitFor(() => {
      expect(screen.getByTestId('god-mode-bypass-unavailable-note')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('god-mode-fetch-error-note')).not.toBeInTheDocument()
  })

  it('cancelling performs nothing', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    const dialog = await screen.findByRole('alertdialog')

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).toBeNull()
    })
    expect(api.setGodMode).not.toHaveBeenCalled()
  })

  it('opens GatewayRestartModal when enabling returns restart_required=true', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_S0_NEVER_ARMED)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: true, restart_required: true })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    fireEvent.click(await screen.findByRole('button', { name: 'Enable god mode' }))

    await waitFor(() => {
      expect(api.setGodMode).toHaveBeenCalledWith(true)
    })
    await waitFor(() => {
      expect(screen.getByText(/gateway restart required/i)).toBeInTheDocument()
    })
  })

  it('does NOT open GatewayRestartModal when disabling (restart_required=false)', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_ON)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: false, restart_required: false })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    fireEvent.click(await screen.findByRole('button', { name: 'Disable god mode' }))

    await waitFor(() => {
      expect(api.setGodMode).toHaveBeenCalledWith(false)
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

  // Regression for the false "gateway may be offline" banner reported on
  // every screen under dev_mode_bypass (bypass_gate.go 503s this endpoint by
  // design). Renders nothing — the dedicated dev-mode-bypass banner in
  // AppShell already covers this case; a real error must still show.
  it('renders nothing (not the status-unknown banner) when the fetch fails with a bypass-gate 503', async () => {
    vi.mocked(api.fetchGodMode).mockRejectedValue(
      new ApiError(503, 'this action is disabled while dev_mode_bypass is active'),
    )
    const { container } = renderBanner()

    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-status-unknown-banner')).not.toBeInTheDocument()
    expect(screen.queryByTestId('god-mode-active-banner')).not.toBeInTheDocument()
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  it('still shows the status-unknown banner for a real (non-503) transport failure', async () => {
    vi.mocked(api.fetchGodMode).mockRejectedValue(new ApiError(0, 'Network unavailable.'))
    renderBanner()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-status-unknown-banner')).toBeInTheDocument()
    })
  })
})
