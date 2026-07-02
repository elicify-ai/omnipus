/**
 * ProvidersSection.test.tsx — ADR-031 Track 1 Providers redesign tests.
 *
 * Covers:
 *   - Original re-auth / manual-model / live-model tests (updated for Sheet)
 *   - Spec tests #14–#24 (configured-only, roster, Sheet, grouped rows, etc.)
 *   - Migration dataset (9-row resolveCatalogEntry tests)
 *   - Settings-side catalog label consistency (US-7)
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
import { resolveCatalogEntry, SELF_HOSTED_CUSTOM_GROUP, GENERIC_GROUP } from '@/lib/providerMigration'
import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

// Standard connected providers for grouping tests.
const ANTHROPIC_PROVIDER = {
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  status: 'disconnected',
  models: [],
}

const OPENROUTER_PROVIDER = {
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  status: 'connected',
  has_models_endpoint: true,
  models: ['openrouter/auto'],
}

const ZHIPU_STD_PROVIDER = {
  id: 'z-ai',
  name: 'z-ai',
  display_name: 'z-ai',
  status: 'connected',
  has_models_endpoint: true,
  models: ['glm-4'],
}

const ZHIPU_CODING_PROVIDER = {
  id: 'z-ai-coding',
  name: 'z-ai-coding',
  display_name: 'z-ai-coding',
  status: 'connected',
  has_models_endpoint: true,
  models: ['codegeex-4'],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
})

// ---------------------------------------------------------------------------
// Spec test #14 — empty-state roster
// ---------------------------------------------------------------------------

describe('ProvidersSection — #14 empty roster', () => {
  it('shows the catalog roster when no providers are configured', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => {
      // roster container is present
      expect(screen.getByTestId('provider-roster')).toBeInTheDocument()
    })
    // OpenAI entry is in the roster
    expect(screen.getByTestId('roster-entry-openai')).toBeInTheDocument()
    // Connect button present
    expect(screen.getByTestId('connect-btn-openai')).toBeInTheDocument()
  })

  it('clicking Connect on a roster entry opens a Sheet (not inline expand)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-btn-openai'))
    fireEvent.click(screen.getByTestId('connect-btn-openai'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    })
    // No inline expand section
    expect(screen.queryByTestId('expandedProvider')).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Spec test #15 — configured-only list
// ---------------------------------------------------------------------------

describe('ProvidersSection — #15 configured-only list', () => {
  it('shows only configured providers, not the full catalog', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('provider-row-anthropic')).toBeInTheDocument()
    })
    // No roster shown
    expect(screen.queryByTestId('provider-roster')).not.toBeInTheDocument()
    // A catalog entry not in the response is not rendered
    expect(screen.queryByTestId('provider-row-openai')).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Spec test #16 — invalid-key provider stays listed
// ---------------------------------------------------------------------------

describe('ProvidersSection — #16 invalid-key provider stays listed', () => {
  it('an error-status provider remains in the list with an error badge', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...ANTHROPIC_PROVIDER, status: 'error' },
    ] as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('provider-row-anthropic')).toBeInTheDocument()
    })
    expect(screen.getByTestId('error-badge-anthropic')).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Spec test #17 — zero-model provider stays listed
// ---------------------------------------------------------------------------

describe('ProvidersSection — #17 zero-model provider stays listed', () => {
  it('a connected provider with zero models is not hidden', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...OPENROUTER_PROVIDER, models: [] },
    ] as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('provider-row-openrouter')).toBeInTheDocument()
    })
  })
})

// ---------------------------------------------------------------------------
// Spec test #18 — Configure opens a Sheet, not inline expand
// ---------------------------------------------------------------------------

describe('ProvidersSection — #18 configure opens Sheet', () => {
  it('clicking Configure opens a Sheet panel, not an inline expand section', async () => {
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    })
    // There must be no old-style inline expand
    // (the form content is inside the sheet, not a separate section in the row)
    expect(screen.getByTestId(`api-key-input-anthropic`)).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Spec test #19 — connect from roster uses same Sheet
// ---------------------------------------------------------------------------

describe('ProvidersSection — #19 connect uses Sheet', () => {
  it('clicking Connect on a roster entry opens the Sheet, not a modal Dialog', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-btn-openai'))
    fireEvent.click(screen.getByTestId('connect-btn-openai'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    })
    // The sheet should have an API key input
    expect(screen.getByTestId('api-key-input-openai')).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Spec test #20 — grouped variant rows
// ---------------------------------------------------------------------------

describe('ProvidersSection — #20 grouped variant rows', () => {
  it('two Zhipu variants render under one Zhipu / GLM group header', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-group-Zhipu / GLM'))
    const group = screen.getByTestId('provider-group-Zhipu / GLM')
    // Both rows inside the group
    expect(within(group).getByTestId('provider-row-z-ai')).toBeInTheDocument()
    expect(within(group).getByTestId('provider-row-z-ai-coding')).toBeInTheDocument()
    // Group header label
    expect(screen.getByTestId('group-header-Zhipu / GLM')).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Spec test #21 — row title omits company prefix
// ---------------------------------------------------------------------------

describe('ProvidersSection — #21 row title no company prefix', () => {
  it('Zhipu Coding Plan row shows "Coding Plan · International", not "Zhipu — Coding Plan"', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ZHIPU_CODING_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-title-z-ai-coding'))
    const title = screen.getByTestId('provider-row-title-z-ai-coding').textContent
    expect(title).toBe('Coding Plan · International')
    expect(title).not.toMatch(/zhipu/i)
  })

  it('z-ai (Standard API intl) row shows "Standard API · International"', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ZHIPU_STD_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-title-z-ai'))
    expect(screen.getByTestId('provider-row-title-z-ai').textContent).toBe('Standard API · International')
  })
})

// ---------------------------------------------------------------------------
// Spec test #22 — view-only variant fields, key editable
// ---------------------------------------------------------------------------

describe('ProvidersSection — #22 view-only variant, key editable', () => {
  it('Sheet shows Plan/Region/Wire/Endpoint as read-only; API key input is editable', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ZHIPU_CODING_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-z-ai-coding'))
    fireEvent.click(screen.getByTestId('configure-btn-z-ai-coding'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    // Variant info section must be present
    expect(screen.getByTestId('variant-info')).toBeInTheDocument()
    // Plan is shown as text, not an input
    expect(screen.getByTestId('variant-plan').tagName).not.toBe('INPUT')
    expect(screen.getByTestId('variant-plan').textContent).toBe('Coding Plan')
    // Region shown
    expect(screen.getByTestId('variant-region').textContent).toBe('International')
    // Wire badge shown
    expect(screen.getByTestId('variant-wire-badge')).toBeInTheDocument()
    // Endpoint shown as text
    expect(screen.getByTestId('variant-endpoint')).toBeInTheDocument()
    // API key input IS editable
    const apiKeyInput = screen.getByTestId('api-key-input-z-ai-coding')
    expect(apiKeyInput.tagName).toBe('INPUT')
  })
})

// ---------------------------------------------------------------------------
// Spec test #23 — plan labels from catalog, no "Anthropic API"/"Token Plan"
// ---------------------------------------------------------------------------

describe('ProvidersSection — #23 plan labels from catalog', () => {
  it('variant row title uses "Standard API" and "Coding Plan", not "Anthropic API"', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-z-ai'))
    const allText = document.body.textContent ?? ''
    expect(allText).not.toMatch(/Anthropic API/)
    expect(allText).not.toMatch(/Token Plan/)
    expect(allText).toMatch(/Standard API/)
    expect(allText).toMatch(/Coding Plan/)
  })
})

// ---------------------------------------------------------------------------
// Spec test #24 (settings-side) — renders catalog label verbatim
// ---------------------------------------------------------------------------

describe('ProvidersSection — #24 settings-side catalog label', () => {
  it('displays the catalog subtitle for the openrouter provider', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([OPENROUTER_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-openrouter'))

    const catalogEntry = PROVIDER_CATALOG.find((e) => e.id === 'openrouter')
    expect(catalogEntry).toBeDefined()
    // Subtitle from catalog should appear in the row
    expect(screen.getByText(catalogEntry!.subtitle)).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Migration dataset — resolveCatalogEntry (spec test #12)
// ---------------------------------------------------------------------------

describe('resolveCatalogEntry — migration dataset', () => {
  it('#1 z-ai → Zhipu / GLM (canonical)', () => {
    const result = resolveCatalogEntry('z-ai')
    expect(result.group).toBe('Zhipu / GLM')
    expect(result.entry?.id).toBe('z-ai')
  })

  it('#2 z.ai → Zhipu / GLM (alias)', () => {
    const result = resolveCatalogEntry('z.ai')
    expect(result.group).toBe('Zhipu / GLM')
    expect(result.entry?.id).toBe('z-ai')
  })

  it('#3 zai → Zhipu / GLM (alias)', () => {
    const result = resolveCatalogEntry('zai')
    expect(result.group).toBe('Zhipu / GLM')
    expect(result.entry?.id).toBe('z-ai')
  })

  it('#4 glm-coding → Zhipu / GLM (alias for z-ai-coding)', () => {
    const result = resolveCatalogEntry('glm-coding')
    expect(result.group).toBe('Zhipu / GLM')
    expect(result.entry?.id).toBe('z-ai-coding')
  })

  it('#5 ollama → Self-hosted / Custom', () => {
    const result = resolveCatalogEntry('ollama')
    expect(result.group).toBe(SELF_HOSTED_CUSTOM_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#6 vllm → Self-hosted / Custom', () => {
    const result = resolveCatalogEntry('vllm')
    expect(result.group).toBe(SELF_HOSTED_CUSTOM_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#7 litellm → Self-hosted / Custom', () => {
    const result = resolveCatalogEntry('litellm')
    expect(result.group).toBe(SELF_HOSTED_CUSTOM_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#8 empty string → Generic (no crash)', () => {
    const result = resolveCatalogEntry('')
    expect(result.group).toBe(GENERIC_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#9 zzz-unknown → Generic (raw id)', () => {
    const result = resolveCatalogEntry('zzz-unknown')
    expect(result.group).toBe(GENERIC_GROUP)
    expect(result.entry).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// Original re-auth tests (updated for Sheet — save button is inside sheet)
// ---------------------------------------------------------------------------

describe('ProvidersSection — original re-auth tests', () => {
  it('lists providers from the API', async () => {
    renderSection()
    await waitFor(() => {
      // The provider row is present
      expect(screen.getByTestId('provider-row-anthropic')).toBeInTheDocument()
    })
  })

  it('opens the re-auth dialog before configuring (does NOT call PUT directly)', async () => {
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    // Open the Sheet
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    // Type a key and save
    fireEvent.change(screen.getByTestId('api-key-input-anthropic'), { target: { value: 'sk-ant-secret' } })
    fireEvent.click(screen.getByTestId('save-provider-anthropic'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(api.configureProvider).not.toHaveBeenCalled()
  })

  it('replays the consent token into configureProvider after re-auth', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureProvider).mockResolvedValue(ANTHROPIC_PROVIDER as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.change(screen.getByTestId('api-key-input-anthropic'), { target: { value: 'sk-ant-secret' } })
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
})

// ---------------------------------------------------------------------------
// Manual provider slug editor (updated for Sheet)
// ---------------------------------------------------------------------------

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

describe('ProvidersSection — manual provider (Sheet)', () => {
  it('shows the editable slug list and PUTs the edited models', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(MANUAL_PROVIDER as never)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureProvider).mockResolvedValue(MANUAL_PROVIDER[0] as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-mygw'))
    // Open the Sheet
    fireEvent.click(screen.getByTestId('configure-btn-mygw'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    // Existing slug is shown in the editor list
    await waitFor(() => screen.getByTestId('model-list-mygw'))
    expect(within(screen.getByTestId('model-list-mygw')).getByText('mygw/llama-3.3-70b')).toBeInTheDocument()

    // Add a new slug
    fireEvent.change(screen.getByTestId('add-model-input-mygw'), { target: { value: 'mygw/mixtral-8x7b' } })
    fireEvent.click(screen.getByTestId('add-model-mygw'))
    expect(within(screen.getByTestId('model-list-mygw')).getByText('mygw/mixtral-8x7b')).toBeInTheDocument()

    // Save → re-auth → PUT with the new models array
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
        ['mygw/llama-3.3-70b', 'mygw/mixtral-8x7b'],
      )
    })
  })

  it('removing a slug updates the list and PUTs the smaller set', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(MANUAL_PROVIDER as never)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureProvider).mockResolvedValue(MANUAL_PROVIDER[0] as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-mygw'))
    fireEvent.click(screen.getByTestId('configure-btn-mygw'))
    await waitFor(() => screen.getByTestId('model-list-mygw'))

    fireEvent.click(screen.getByTestId('remove-model-mygw-mygw/llama-3.3-70b'))
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
})

// ---------------------------------------------------------------------------
// Live provider refresh-models (updated for Sheet)
// ---------------------------------------------------------------------------

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

describe('ProvidersSection — live provider refresh-models', () => {
  it('shows Refresh models inside the Sheet and calls refresh-models', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(LIVE_PROVIDER as never)
    vi.mocked(api.refreshProviderModels).mockResolvedValue({
      ...LIVE_PROVIDER[0],
      models: ['openrouter/auto', 'anthropic/claude-sonnet-4-5'],
    } as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    // Open the Sheet
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    const refreshBtn = screen.getByTestId('refresh-models-openrouter')
    fireEvent.click(refreshBtn)

    await waitFor(() => {
      expect(api.refreshProviderModels).toHaveBeenCalledWith('openrouter')
    })
    // No manual editor for a live provider
    expect(screen.queryByTestId('model-list-openrouter')).not.toBeInTheDocument()
  })

  it('surfaces a refresh warning as an error toast', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue(LIVE_PROVIDER as never)
    vi.mocked(api.refreshProviderModels).mockResolvedValue({
      ...LIVE_PROVIDER[0],
      warning: 'could not fetch upstream model list: status 429',
    } as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))
    fireEvent.click(screen.getByTestId('refresh-models-openrouter'))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringContaining('429') }),
      )
    })
  })
})

// ---------------------------------------------------------------------------
// Validation integration (MAJOR-3 / US8) — updated for Sheet
// ---------------------------------------------------------------------------

describe('ProvidersSection — validation integration (MAJOR-3 / US8)', () => {
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

  async function openSheetAndSave(key = 'bad-key-123') {
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))
    fireEvent.change(screen.getByTestId('api-key-input-openrouter'), { target: { value: key } })
    fireEvent.click(screen.getByTestId('save-provider-openrouter'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))
  }

  it('US8.1 — 422 (InvalidKey) shows blocking error toast and Sheet stays open', async () => {
    const errMsg = 'The API key was rejected by OpenRouter. Check you copied the whole key.'
    vi.mocked(api.configureProvider).mockRejectedValue(
      new api.ApiError(422, errMsg),
    )

    await openSheetAndSave('definitely-wrong-key')

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: errMsg }),
      )
    })

    // Sheet stays open — API key input still present
    expect(screen.getByTestId('api-key-input-openrouter')).toBeInTheDocument()
    expect(screen.queryByTestId('save-validation-banner-openrouter')).not.toBeInTheDocument()
  })

  it('US8.2 — 200 + no_credit outcome → amber banner, Sheet stays open', async () => {
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      validation: {
        outcome: 'no_credit',
        message: 'Your OpenRouter key works, but the account has no credit.',
      },
    } as never)

    await openSheetAndSave('no-credit-key')

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Provider saved' }),
      )
    })

    await waitFor(() => {
      expect(screen.getByTestId('save-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('save-validation-banner-openrouter')).toHaveAttribute('data-outcome', 'no_credit')
    expect(
      screen.getByText('Your OpenRouter key works, but the account has no credit.'),
    ).toBeInTheDocument()

    // Sheet stays open
    expect(screen.getByTestId('api-key-input-openrouter')).toBeInTheDocument()
  })

  it('US8.3 — 200 + unreachable outcome → banner with unreachable outcome', async () => {
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      validation: {
        outcome: 'unreachable',
        message: "Couldn't reach OpenRouter to check the key.",
      },
    } as never)

    await openSheetAndSave('key-no-reach')

    await waitFor(() => {
      expect(screen.getByTestId('save-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('save-validation-banner-openrouter')).toHaveAttribute(
      'data-outcome',
      'unreachable',
    )
  })

  it('US8.3 — 200 + restricted outcome → banner with restricted outcome', async () => {
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      validation: {
        outcome: 'restricted',
        message: 'The request was blocked in your region.',
      },
    } as never)

    await openSheetAndSave('restricted-key')

    await waitFor(() => {
      expect(screen.getByTestId('save-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('save-validation-banner-openrouter')).toHaveAttribute(
      'data-outcome',
      'restricted',
    )
  })

  it('US8.4 — 200 with no validation → no banner, Sheet closes', async () => {
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
    } as never)

    await openSheetAndSave('valid-key')

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Provider saved' }),
      )
    })

    expect(screen.queryByTestId('save-validation-banner-openrouter')).not.toBeInTheDocument()

    // Sheet closes after clean success
    await waitFor(() => {
      expect(screen.queryByTestId('api-key-input-openrouter')).not.toBeInTheDocument()
    })
  })

  it('Test button → no_credit validation → banner appears below the row', async () => {
    vi.mocked(api.testProvider).mockResolvedValue({
      success: true,
      validation: {
        outcome: 'no_credit',
        message: 'Your OpenRouter key works, but the account has no credit.',
      },
    } as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    // Open the Sheet to access the Test button
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    // Click the Test button (inside the Sheet)
    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))

    await waitFor(() => {
      expect(screen.getByTestId('test-validation-banner-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('test-validation-banner-openrouter')).toHaveAttribute(
      'data-outcome',
      'no_credit',
    )
    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'success', message: 'Connection successful' }),
    )
  })

  it('Test button → success=true + no validation → no banner, toast only', async () => {
    vi.mocked(api.testProvider).mockResolvedValue({
      success: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Connection successful' }),
      )
    })
    expect(screen.queryByTestId('test-validation-banner-openrouter')).not.toBeInTheDocument()
  })
})
