/**
 * ProvidersSection.test.tsx — Spec-6 FR-12.2 / FR-6.6.
 *
 * Covers the model/provider list and that saving an API key is gated by the
 * re-auth consent dialog: expanding a provider, typing a key, and clicking
 * "Save & Connect" opens the ReAuthDialog (the PUT does NOT fire yet), and only
 * a successful re-auth replays the consent token into configureProvider.
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
    fetchProviders: vi.fn(),
    configureProvider: vi.fn(),
    testProvider: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { ProvidersSection } from './ProvidersSection'

const PROVIDERS = [
  {
    id: 'anthropic',
    name: 'anthropic',
    display_name: 'Anthropic',
    status: 'disconnected',
    models: [],
  },
]

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

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchProviders).mockResolvedValue(PROVIDERS as never)
})

describe('ProvidersSection', () => {
  it('lists providers from the API', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByText('Anthropic')).toBeInTheDocument()
    })
  })

  it('opens the re-auth dialog before configuring (does NOT call PUT directly)', async () => {
    renderSection()
    await waitFor(() => screen.getByText('Anthropic'))

    // Expand the config form, type a key, click Save & Connect.
    fireEvent.click(screen.getByRole('button', { name: /configure/i }))
    fireEvent.change(screen.getByPlaceholderText(/starts with sk-ant/i), { target: { value: 'sk-ant-secret' } })
    fireEvent.click(screen.getByTestId('save-provider-anthropic'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(api.configureProvider).not.toHaveBeenCalled()
  })

  it('replays the consent token into configureProvider after re-auth', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureProvider).mockResolvedValue(PROVIDERS[0] as never)

    renderSection()
    await waitFor(() => screen.getByText('Anthropic'))

    fireEvent.click(screen.getByRole('button', { name: /configure/i }))
    fireEvent.change(screen.getByPlaceholderText(/starts with sk-ant/i), { target: { value: 'sk-ant-secret' } })
    fireEvent.click(screen.getByTestId('save-provider-anthropic'))

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.configureProvider).toHaveBeenCalledWith(
        'anthropic',
        'sk-ant-secret',
        undefined,
        undefined,
        'reauth_tok',
      )
    })
  })

  it('shows an error when the providers query fails', async () => {
    vi.mocked(api.fetchProviders).mockRejectedValue(new Error('boom'))
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/failed to load providers/i)).toBeInTheDocument()
    })
  })
})
