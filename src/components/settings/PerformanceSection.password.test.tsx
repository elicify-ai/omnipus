/**
 * PerformanceSection.password.test.tsx — Spec-3 FR-6.6, ADR-0010 WP3.
 *
 * The password-mode counterpart to PerformanceSection.test.tsx's confirm-mode
 * coverage: with AppState.identity.mode = 'local', useStepUp() picks
 * 'password' and the autosave-triggered PUT goes through useStepUp's
 * dialog-first flow — ReAuthDialog opens before anything is sent, and a
 * successful re-auth sends the PUT exactly once, carrying the minted consent
 * token. Dismissal sends nothing and leaves the change unsaved.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchPerformanceSettings: vi.fn(),
    updatePerformanceSettings: vi.fn(),
    fetchAppState: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'

// Local mode (identity.mode: 'local') pins useStepUp() to 'password'.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

const SETTINGS = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — password mode (local edition)', () => {
  it('opens ReAuthDialog before the autosave PUT, then sends it once with the minted token', async () => {
    vi.mocked(api.updatePerformanceSettings)
      .mockResolvedValueOnce({ ...SETTINGS, max_parallel_agents: 8 } as never)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    // Dialog first: ReAuthDialog opens before anything is sent.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0][1]).toBe('reauth_tok')
    })
  })

  it('dismissing the re-auth dialog leaves the change unsaved', async () => {

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })
})
