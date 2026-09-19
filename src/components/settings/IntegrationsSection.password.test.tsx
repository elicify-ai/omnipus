/**
 * IntegrationsSection.password.test.tsx — Spec-6 U5 (FR-12.1 / FR-12.2),
 * ADR-0010 WP3.
 *
 * The password-mode counterpart to IntegrationsSection.test.tsx's confirm-mode
 * coverage: with AppState.identity.mode = 'local', useStepUp() picks 'password'
 * and a configure action goes through useStepUp's dialog-first flow —
 * ReAuthDialog (restored verbatim from the engine merge base) opens before
 * anything is sent, and a successful re-auth sends the PUT exactly once,
 * carrying the minted consent token. Nothing is sent on dismissal.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchIntegrationProviders: vi.fn(),
    configureIntegrationProvider: vi.fn(),
    fetchAppState: vi.fn(),
    reAuth: vi.fn(),
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { IntegrationsSection } from './IntegrationsSection'

// Local mode (identity.mode: 'local') pins useStepUp() to 'password'.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

const CATALOGUE = {
  search: [
    { id: 'brave', kind: 'search', display_name: 'Brave Search', configured: false, requires_key: true, active: false },
  ],
  voice: [],
  active_search: '',
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <IntegrationsSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(CATALOGUE as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

describe('IntegrationsSection — password mode (local edition)', () => {
  it('opens ReAuthDialog before the gated PUT, then sends it once with the minted token', async () => {
    vi.mocked(api.configureIntegrationProvider)
      .mockResolvedValueOnce(CATALOGUE as never)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))

    // Dialog first: ReAuthDialog opens before anything is sent.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
    })
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.configureIntegrationProvider).mock.calls[0][2]).toBe('reauth_tok')
    })
  })

  it('dismissing the re-auth dialog sends nothing further', async () => {

    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))

    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    })
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })
})
