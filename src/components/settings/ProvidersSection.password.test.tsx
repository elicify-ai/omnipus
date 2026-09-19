/**
 * ProvidersSection.password.test.tsx — ADR-0010 WP3.
 *
 * The password-mode counterpart to ProvidersSection.test.tsx's confirm-mode
 * coverage: with AppState.identity.mode = 'local', useStepUp() picks
 * 'password' and a provider API-key PUT goes through useStepUp's
 * dialog-first flow — ReAuthDialog (restored verbatim from the engine merge
 * base) opens before anything is sent, and a successful re-auth sends the PUT
 * exactly once, carrying the minted consent token. Nothing is sent on dismissal.
 */

import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchProviders: vi.fn(),
    fetchProvidersCatalog: vi.fn(),
    configureProvider: vi.fn(),
    testProvider: vi.fn(),
    deleteProvider: vi.fn(),
    getDefaultModel: vi.fn(),
    putDefaultModel: vi.fn(),
    checkEntitlement: vi.fn(),
    signOutProvider: vi.fn(),
    fetchSignInStatus: vi.fn(),
    fetchAppState: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { ProvidersSection } from './ProvidersSection'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'

// Local mode (identity.mode: 'local') pins useStepUp() to 'password'.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ProvidersSection />
    </QueryClientProvider>,
  )
}

const ANTHROPIC_PROVIDER = {
  status: 'connected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  has_models_endpoint: true,
  models: [],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
  vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
  vi.mocked(api.getDefaultModel).mockResolvedValue(null as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

describe('ProvidersSection — password mode (local edition)', () => {
  it('opens ReAuthDialog before the gated PUT, then sends it once with the minted token', async () => {
    vi.mocked(api.configureProvider)
      .mockResolvedValueOnce(ANTHROPIC_PROVIDER as never)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.change(screen.getByTestId('api-key-input-anthropic'), { target: { value: 'sk-ant-secret' } })
    fireEvent.click(screen.getByTestId('save-provider-anthropic'))

    // Dialog first: ReAuthDialog opens before anything is sent.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
    })
    expect(api.configureProvider).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.configureProvider).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.configureProvider).mock.calls[0][4]).toBe('reauth_tok')
    })
  })

  it('dismissing the re-auth dialog sends nothing further', async () => {

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.change(screen.getByTestId('api-key-input-anthropic'), { target: { value: 'sk-ant-secret' } })
    fireEvent.click(screen.getByTestId('save-provider-anthropic'))

    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    })
    expect(api.configureProvider).not.toHaveBeenCalled()
  })
})
