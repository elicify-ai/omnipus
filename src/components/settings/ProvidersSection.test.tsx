/**
 * ProvidersSection.test.tsx — Spec-6 FR-12.2 / FR-6.6.
 *
 * Covers the model/provider list and that saving an API key is gated by the
 * re-auth consent dialog: expanding a provider, typing a key, and clicking
 * "Save & Connect" opens the ReAuthDialog (the PUT does NOT fire yet), and only
 * a successful re-auth replays the consent token into configureProvider.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
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
    refreshProviderModels: vi.fn(),
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
        undefined,
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

  // ── UAT model-catalog: manual (endpoint-less) provider slug editor ──────────

  const MANUAL_PROVIDER = [
    {
      id: 'mygw',
      name: 'mygw',
      display_name: 'My Gateway',
      status: 'connected',
      has_models_endpoint: false,
      models: ['mygw/llama-3.3-70b'],
    },
  ]

  it('manual provider: shows the editable slug list and PUTs the edited models', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(MANUAL_PROVIDER as never)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureProvider).mockResolvedValue(MANUAL_PROVIDER[0] as never)

    renderSection()
    await waitFor(() => screen.getByText('My Gateway'))

    // Manual badge present, no live refresh action.
    expect(screen.getByText(/manual models/i)).toBeInTheDocument()
    expect(screen.queryByTestId('refresh-models-mygw')).not.toBeInTheDocument()

    // Open the editor — existing slug is shown in the editor list.
    fireEvent.click(screen.getByRole('button', { name: /edit/i }))
    await waitFor(() => screen.getByTestId('model-list-mygw'))
    expect(within(screen.getByTestId('model-list-mygw')).getByText('mygw/llama-3.3-70b')).toBeInTheDocument()

    // Add a new slug.
    fireEvent.change(screen.getByTestId('add-model-input-mygw'), { target: { value: 'mygw/mixtral-8x7b' } })
    fireEvent.click(screen.getByTestId('add-model-mygw'))
    expect(within(screen.getByTestId('model-list-mygw')).getByText('mygw/mixtral-8x7b')).toBeInTheDocument()

    // Save (no key change) → re-auth → PUT with the new models array.
    fireEvent.click(screen.getByTestId('save-provider-mygw'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'pw' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.configureProvider).toHaveBeenCalledWith(
        'mygw',
        undefined, // no key change → api_key omitted
        undefined,
        undefined,
        'reauth_tok',
        ['mygw/llama-3.3-70b', 'mygw/mixtral-8x7b'],
      )
    })
  })

  it('manual provider: removing a slug updates the list and PUTs the smaller set', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(MANUAL_PROVIDER as never)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureProvider).mockResolvedValue(MANUAL_PROVIDER[0] as never)

    renderSection()
    await waitFor(() => screen.getByText('My Gateway'))
    fireEvent.click(screen.getByRole('button', { name: /edit/i }))
    await waitFor(() => screen.getByTestId('model-list-mygw'))

    fireEvent.click(screen.getByTestId('remove-model-mygw-mygw/llama-3.3-70b'))
    // The only slug is gone → editor list disappears, empty state shown. (The
    // collapsed-row preview still reflects the server cache until save resolves.)
    expect(screen.queryByTestId('model-list-mygw')).not.toBeInTheDocument()
    expect(screen.getByText(/no models added yet/i)).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('save-provider-mygw'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'pw' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.configureProvider).toHaveBeenCalledWith(
        'mygw',
        undefined,
        undefined,
        undefined,
        'reauth_tok',
        [],
      )
    })
  })

  // ── UAT model-catalog: live provider refresh-models ─────────────────────────

  const LIVE_PROVIDER = [
    {
      id: 'openrouter',
      name: 'openrouter',
      display_name: 'OpenRouter',
      status: 'connected',
      has_models_endpoint: true,
      models: ['openrouter/auto'],
    },
  ]

  it('live provider: shows a Refresh models action and calls refresh-models', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(LIVE_PROVIDER as never)
    vi.mocked(api.refreshProviderModels).mockResolvedValue({
      ...LIVE_PROVIDER[0],
      models: ['openrouter/auto', 'anthropic/claude-sonnet-4-5'],
    } as never)

    renderSection()
    await waitFor(() => screen.getByText('OpenRouter'))

    expect(screen.getByText(/live model list/i)).toBeInTheDocument()
    const refreshBtn = screen.getByTestId('refresh-models-openrouter')
    fireEvent.click(refreshBtn)

    await waitFor(() => {
      expect(api.refreshProviderModels).toHaveBeenCalledWith('openrouter')
    })
    // No manual editor for a live provider.
    fireEvent.click(screen.getByRole('button', { name: /edit/i }))
    expect(screen.queryByTestId('model-list-openrouter')).not.toBeInTheDocument()
  })

  it('live provider: surfaces a refresh warning as an error toast', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(LIVE_PROVIDER as never)
    vi.mocked(api.refreshProviderModels).mockResolvedValue({
      ...LIVE_PROVIDER[0],
      warning: 'could not fetch upstream model list: status 429',
    } as never)

    renderSection()
    await waitFor(() => screen.getByText('OpenRouter'))
    fireEvent.click(screen.getByTestId('refresh-models-openrouter'))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringContaining('429') }),
      )
    })
  })
})
