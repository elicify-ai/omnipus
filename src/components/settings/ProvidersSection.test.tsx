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

// ── MAJOR-3 — validation integration (US8.1 / 422 + banner) ──────────────────
//
// Spec: provider-validation-centralization-spec.md, US8 / MAJOR-3.
//
// These tests assert the integration between configureProvider and the
// ProvidersSection UI for the validation flows:
//
//   1. 422 (InvalidKey) → blocking error toast, form stays open (US8.1).
//   2. 200 + validation.outcome=no_credit → banner appears (wallet icon, amber).
//   3. Test button → 200 + validation → banner appears below the provider row.

describe('ProvidersSection — validation integration (MAJOR-3 / US8)', () => {
  // Shared provider fixture for all validation tests (live provider, already connected).
  const CONNECTED_PROVIDER = [
    {
      id: 'openrouter',
      name: 'openrouter',
      display_name: 'OpenRouter',
      status: 'connected',
      has_models_endpoint: true,
      models: ['openrouter/auto'],
    },
  ]

  beforeEach(() => {
    vi.mocked(api.fetchProviders).mockResolvedValue(CONNECTED_PROVIDER as never)
  })

  // Helper: expand the config form, type a key, click Save & Connect, confirm re-auth.
  async function expandAndInitiateSave(key = 'bad-key-123') {
    renderSection()
    await waitFor(() => screen.getByText('OpenRouter'))
    fireEvent.click(screen.getByRole('button', { name: /edit/i }))
    fireEvent.change(screen.getByPlaceholderText(/sk-or/i), { target: { value: key } })
    fireEvent.click(screen.getByTestId('save-provider-openrouter'))
    // Re-auth dialog opens — fill password and confirm.
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))
  }

  it('US8.1 — 422 (InvalidKey) shows a blocking error toast and form stays open', async () => {
    // Mock configureProvider to throw an ApiError with status 422.
    const errMsg = 'The API key was rejected by OpenRouter. Check you copied the whole key.'
    vi.mocked(api.configureProvider).mockRejectedValue(
      new api.ApiError(422, errMsg),
    )

    await expandAndInitiateSave('definitely-wrong-key')

    await waitFor(() => {
      // A blocking error toast must appear with the server message.
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: errMsg }),
      )
    })

    // The form MUST stay open (US8.1 — user can correct key and retry).
    // The expanded form contains the API Key input; if it's in the DOM the form is open.
    expect(screen.getByPlaceholderText(/sk-or/i)).toBeInTheDocument()

    // No save-validation banner should be shown (it's a blocking error, not a warning).
    expect(screen.queryByTestId('save-validation-banner-openrouter')).not.toBeInTheDocument()
  })

  it('US8.2 — 200 + no_credit outcome → amber banner with wallet icon, form stays open', async () => {
    // Save succeeds but the key has no credit.
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      validation: {
        outcome: 'no_credit',
        message: 'Your OpenRouter key works, but the account has no credit.',
      },
    } as never)

    await expandAndInitiateSave('no-credit-key')

    await waitFor(() => {
      // Success toast fires (save did succeed).
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Provider saved' }),
      )
    })

    // The save-validation banner MUST appear with the no_credit outcome.
    await waitFor(() => {
      expect(screen.getByTestId('save-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('save-validation-banner-openrouter')).toHaveAttribute('data-outcome', 'no_credit')

    // The server message is displayed in the banner.
    expect(
      screen.getByText('Your OpenRouter key works, but the account has no credit.'),
    ).toBeInTheDocument()

    // Form stays open so the user sees the banner (US8.2 — non-blocking).
    expect(screen.getByPlaceholderText(/sk-or/i)).toBeInTheDocument()
  })

  it('US8.3 — 200 + unreachable outcome → amber banner with wifi-slash icon', async () => {
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      validation: {
        outcome: 'unreachable',
        message: "Couldn't reach OpenRouter to check the key.",
      },
    } as never)

    await expandAndInitiateSave('key-no-reach')

    await waitFor(() => {
      expect(screen.getByTestId('save-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('save-validation-banner-openrouter')).toHaveAttribute(
      'data-outcome',
      'unreachable',
    )
  })

  it('US8.3 — 200 + restricted outcome → amber banner with lock icon', async () => {
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      validation: {
        outcome: 'restricted',
        message: 'The request was blocked in your region.',
      },
    } as never)

    await expandAndInitiateSave('restricted-key')

    await waitFor(() => {
      expect(screen.getByTestId('save-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('save-validation-banner-openrouter')).toHaveAttribute(
      'data-outcome',
      'restricted',
    )
  })

  it('US8.4 — 200 with no validation → no banner, form closes on success', async () => {
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      // No validation field = clean success
    } as never)

    await expandAndInitiateSave('valid-key')

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Provider saved' }),
      )
    })

    // No banner should appear.
    expect(screen.queryByTestId('save-validation-banner-openrouter')).not.toBeInTheDocument()

    // Form closes after a clean success.
    await waitFor(() => {
      expect(screen.queryByPlaceholderText(/sk-or/i)).not.toBeInTheDocument()
    })
  })

  it('Test button → mocked 200 + no_credit validation → banner appears below the row', async () => {
    vi.mocked(api.testProvider).mockResolvedValue({
      success: true,
      validation: {
        outcome: 'no_credit',
        message: 'Your OpenRouter key works, but the account has no credit.',
      },
    } as never)

    renderSection()
    await waitFor(() => screen.getByText('OpenRouter'))

    // Click the Test button (only visible when connected).
    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))

    await waitFor(() => {
      // Test-validation banner appears below the row.
      expect(screen.getByTestId('test-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('test-validation-banner-openrouter')).toHaveAttribute(
      'data-outcome',
      'no_credit',
    )
    // Success toast also fires.
    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'success', message: 'Connection successful' }),
    )
  })

  it('Test button → success=true + no validation → no banner, toast only', async () => {
    vi.mocked(api.testProvider).mockResolvedValue({
      success: true,
      // No validation field
    } as never)

    renderSection()
    await waitFor(() => screen.getByText('OpenRouter'))

    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Connection successful' }),
      )
    })
    expect(screen.queryByTestId('test-validation-banner-openrouter')).not.toBeInTheDocument()
  })
})
