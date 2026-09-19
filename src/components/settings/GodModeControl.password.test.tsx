/**
 * GodModeControl.password.test.tsx — Spec-6 FR-12.2, ADR-0010 WP3.
 *
 * The password-mode counterpart to GodModeControl.test.tsx's confirm-mode
 * coverage: with AppState.identity.mode = 'local', useStepUp() picks
 * 'password' and the god-mode POST goes through useStepUp's dialog-first
 * flow — ReAuthDialog (restored verbatim from the engine merge base) opens
 * before anything is sent, and a successful re-auth sends the POST exactly
 * once, carrying the minted consent token. Nothing is sent on dismissal.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

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
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { GodModeControl } from './GodModeControl'

const STATE_OFF = { enabled: false, available: false, supported: true, persisted: false }

// Local mode (identity.mode: 'local') pins useStepUp() to 'password'.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as never

function renderControl() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <GodModeControl />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
  vi.mocked(api.fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

describe('GodModeControl — password mode (local edition)', () => {
  it('opens ReAuthDialog before the gated POST, then sends it once with the minted token', async () => {
    vi.mocked(api.setGodMode)
      .mockResolvedValueOnce({ enabled: true, restart_required: false })
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())
    fireEvent.click(screen.getByTestId('god-mode-toggle'))

    // Dialog first: ReAuthDialog opens before anything is sent.
    await waitFor(() => expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument())
    expect(api.setGodMode).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.setGodMode).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.setGodMode).mock.calls[0]).toEqual([true, 'reauth_tok'])
    })
  })

  it('dismissing the re-auth dialog sends nothing further', async () => {

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())
    fireEvent.click(screen.getByTestId('god-mode-toggle'))

    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument())
    expect(api.setGodMode).not.toHaveBeenCalled()
    expect(api.reAuth).not.toHaveBeenCalled()
  })
})
