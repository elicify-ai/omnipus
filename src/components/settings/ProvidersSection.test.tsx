/**
 * ProvidersSection.test.tsx — Provider UX fixes (docs/internal/specs/provider-ux-fixes-plan.md)
 * + ADR-031 Track 1 Providers redesign tests it supersedes where they conflict.
 *
 * Covers:
 *   - Original re-auth / manual-model / live-model tests (updated for Sheet)
 *   - FIX-1: no "Add another…" control anywhere
 *   - FIX-2: flat row for a single configured company variant; group header only for ≥2
 *   - FIX-3: configured-only list, empty state, always-visible "Connect a provider",
 *            picker Sheet (search + catalog grouped by company, excludes configured)
 *   - FIX-4: real terminology ("Pay-as-you-go API" / "Coding Plan", no "Standard API")
 *   - Migration dataset (resolveCatalogEntry over the fetched catalog)
 *   - Settings-side catalog label consistency (US-7)
 *
 * Catalog source (ADR-068 FR-037 / T068-05): the registry-fed document from
 * GET /providers/catalog, mocked here via fetchProvidersCatalog with the
 * shared stub fixture — never a bundled catalog. GET /providers returns
 * configured rows only (FR-011a), so every fixture row carries the required
 * `auth_method` / `dependents` / `backs_default` fields.
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
    fetchProvidersCatalog: vi.fn(),
    configureProvider: vi.fn(),
    testProvider: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { ProvidersSection } from './ProvidersSection'
import { resolveCatalogEntry, SELF_HOSTED_CUSTOM_GROUP, GENERIC_GROUP } from '@/lib/providerMigration'
import { PROVIDERS_CATALOG, CATALOG_PROVIDERS } from '@/test/fixtures/providersCatalog'
import { catalogLabel, catalogSubtitle } from '@/lib/catalogDisplay'

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

// Configured providers for grouping tests — the FR-011a shape: every row
// GET /providers returns is a configured one, with the contract's required
// `auth_method` / `dependents` / `backs_default` present.
const CONFIGURED_BASE = {
  status: 'connected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
}

const ANTHROPIC_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  models: [],
}

const OPENROUTER_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  has_models_endpoint: true,
  models: ['openrouter/auto'],
}

const ZHIPU_STD_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'zai',
  name: 'zai',
  display_name: 'zai',
  has_models_endpoint: true,
  models: ['glm-5.2'],
}

const ZHIPU_CODING_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'zai-coding-plan',
  name: 'zai-coding-plan',
  display_name: 'zai-coding-plan',
  has_models_endpoint: true,
  models: ['glm-5.2'],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
  vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
})

// ---------------------------------------------------------------------------
// FIX-3 — empty state: compact message + "Connect a provider", no roster
// ---------------------------------------------------------------------------

describe('ProvidersSection — FIX-3 empty state', () => {
  it('shows a compact message and a single "Connect a provider" CTA, no default-visible roster', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('providers-empty-state')).toBeInTheDocument()
    })
    expect(screen.getByText(/no providers configured yet/i)).toBeInTheDocument()
    expect(screen.getByTestId('connect-provider-btn')).toBeInTheDocument()
    // No wall-of-roster entries rendered by default.
    expect(screen.queryByTestId('roster-entry-openai')).not.toBeInTheDocument()
    expect(screen.queryByTestId('provider-picker-sheet')).not.toBeInTheDocument()
  })

  it('clicking "Connect a provider" opens the picker Sheet (not the connect form directly)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-picker-sheet')).toBeInTheDocument()
    })
    // The connect form (config Sheet) is not open yet — only the picker.
    expect(screen.queryByTestId('provider-config-sheet')).not.toBeInTheDocument()
    expect(screen.getByTestId('picker-entry-openai')).toBeInTheDocument()
  })

  it('selecting a picker entry transitions to the connect form Sheet', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-entry-openai'))
    fireEvent.click(screen.getByTestId('picker-entry-openai'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    })
    expect(screen.getByTestId('api-key-input-openai')).toBeInTheDocument()
    // The picker Sheet closed once an entry was chosen.
    expect(screen.queryByTestId('provider-picker-sheet')).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// FIX-3 — configured-only list; "Connect a provider" always available
// ---------------------------------------------------------------------------

describe('ProvidersSection — FIX-3 configured-only list', () => {
  it('shows only configured providers, not the full catalog', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('provider-row-anthropic')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('providers-empty-state')).not.toBeInTheDocument()
    // A catalog entry not in the response is not rendered
    expect(screen.queryByTestId('provider-row-openai')).not.toBeInTheDocument()
  })

  it('"Connect a provider" is visible at the section header when providers exist (previously no way to add more)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-anthropic'))
    expect(screen.getByTestId('connect-provider-btn')).toBeInTheDocument()
  })

  it('the picker excludes already-configured catalog entries', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('provider-picker-sheet'))
    // anthropic is configured — excluded from the picker.
    expect(screen.queryByTestId('picker-entry-anthropic')).not.toBeInTheDocument()
    // openai is not configured — still offered.
    expect(screen.getByTestId('picker-entry-openai')).toBeInTheDocument()
  })

  it('the picker excludes the canonical entry for an alias-stored provider (z-ai → zai)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...CONFIGURED_BASE, id: 'z-ai', name: 'z-ai', display_name: 'z-ai', models: [] },
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('provider-picker-sheet'))
    // Stored under the alias 'z-ai' — resolves to the canonical 'zai' entry,
    // which must be excluded (not offered a second time under its canonical id).
    expect(screen.queryByTestId('picker-entry-zai')).not.toBeInTheDocument()
    expect(screen.getByTestId('picker-entry-openai')).toBeInTheDocument()
  })

  it('search filters the picker catalog by company/label', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('provider-picker-sheet'))
    fireEvent.change(screen.getByTestId('picker-search-input'), { target: { value: 'zhipu' } })
    await waitFor(() => {
      expect(screen.getByTestId('picker-entry-zai')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('picker-entry-openai')).not.toBeInTheDocument()
  })

  it('shows an "all configured" message when every catalog entry is already configured', async () => {
    const allConfigured = CATALOG_PROVIDERS.map((e) => ({
      ...CONFIGURED_BASE,
      id: e.id,
      name: e.id,
      display_name: catalogLabel(e),
      models: [],
    }))
    vi.mocked(api.fetchProviders).mockResolvedValue(allConfigured as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('provider-picker-sheet'))
    expect(screen.getByText(/all available providers are already configured/i)).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// FIX-1 — no "Add another…" control anywhere
// ---------------------------------------------------------------------------

describe('ProvidersSection — FIX-1 no Add another', () => {
  it('never renders an "Add another…" affordance, even for a grouped multi-variant company', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-group-Zhipu AI'))
    expect(screen.queryByText(/add another/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('add-another-Zhipu AI')).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// FIX-2 — flat row for a single configured variant; group header only for ≥2
// ---------------------------------------------------------------------------

describe('ProvidersSection — FIX-2 flat vs grouped rows', () => {
  it('a single configured company variant renders as a FLAT row (no group wrapper)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-anthropic'))
    // No group-header wrapper for a lone configured provider.
    expect(screen.queryByTestId('provider-group-Anthropic')).not.toBeInTheDocument()
    expect(screen.queryByTestId('group-header-Anthropic')).not.toBeInTheDocument()
    // The flat row title is the full catalog label (company context, since no
    // group header supplies it).
    expect(screen.getByTestId('provider-row-title-anthropic').textContent).toBe('Anthropic')
  })

  it('two Zhipu variants render under one Zhipu AI group header', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-group-Zhipu AI'))
    const group = screen.getByTestId('provider-group-Zhipu AI')
    // Both rows inside the group
    expect(within(group).getByTestId('provider-row-zai')).toBeInTheDocument()
    expect(within(group).getByTestId('provider-row-zai-coding-plan')).toBeInTheDocument()
    // Group header label
    expect(screen.getByTestId('group-header-Zhipu AI')).toBeInTheDocument()
  })

  it('grouped row title omits the company prefix (already in the group header)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-title-zai-coding-plan'))
    const title = screen.getByTestId('provider-row-title-zai-coding-plan').textContent
    expect(title).toBe('Coding Plan · International')
    expect(title).not.toMatch(/zhipu/i)
  })

  it('a lone provider that mixes with a grouped company still renders flat for its own company', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ANTHROPIC_PROVIDER,
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-anthropic'))
    // Anthropic (1 variant) is flat.
    expect(screen.queryByTestId('provider-group-Anthropic')).not.toBeInTheDocument()
    // Zhipu AI (2 variants) is grouped.
    expect(screen.getByTestId('provider-group-Zhipu AI')).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Configure opens a Sheet; invalid-key / zero-model providers stay listed
// ---------------------------------------------------------------------------

describe('ProvidersSection — configure Sheet + status visibility', () => {
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

  it('a connected provider with zero models is not hidden and shows a connected badge', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...OPENROUTER_PROVIDER, models: [], status: 'connected' },
    ] as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('provider-row-openrouter')).toBeInTheDocument()
    })
    expect(screen.getByTestId('connected-badge-openrouter')).toBeInTheDocument()
  })

  it('clicking Configure opens a Sheet panel, not an inline expand section', async () => {
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    })
    expect(screen.getByTestId(`api-key-input-anthropic`)).toBeInTheDocument()
  })

  it('view-only Sheet fields: Plan/Region/Endpoint are read-only; API key input is editable', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ZHIPU_CODING_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-zai-coding-plan'))
    fireEvent.click(screen.getByTestId('configure-btn-zai-coding-plan'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    expect(screen.getByTestId('variant-info')).toBeInTheDocument()
    expect(screen.getByTestId('variant-plan').tagName).not.toBe('INPUT')
    expect(screen.getByTestId('variant-plan').textContent).toBe('Coding Plan')
    expect(screen.getByTestId('variant-region').textContent).toBe('International')
    expect(screen.getByTestId('variant-endpoint')).toBeInTheDocument()

    const variantInfo = screen.getByTestId('variant-info')
    expect(variantInfo.querySelectorAll('input')).toHaveLength(0)
    expect(variantInfo.querySelectorAll('textarea')).toHaveLength(0)
    expect(variantInfo.querySelectorAll('[contenteditable]')).toHaveLength(0)

    const apiKeyInput = screen.getByTestId('api-key-input-zai-coding-plan')
    expect(apiKeyInput.tagName).toBe('INPUT')
  })

  it('the Sheet never shows a "Wire" field — wire is not a display row (FIX-5)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))
    expect(screen.queryByTestId('variant-wire-badge')).not.toBeInTheDocument()
    expect(screen.queryByText(/^Wire$/)).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// FIX-4 — real terminology: "Pay-as-you-go API" / "Coding Plan"
// ---------------------------------------------------------------------------

describe('ProvidersSection — FIX-4 real terminology', () => {
  it('variant row titles use "Pay-as-you-go API" / "Coding Plan"; never "Standard API"', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-zai'))
    const allText = document.body.textContent ?? ''
    expect(allText).not.toMatch(/Standard API/)
    expect(allText).not.toMatch(/Anthropic-compatible/)
    expect(allText).toMatch(/Pay-as-you-go API/)
    expect(allText).toMatch(/Coding Plan/)
    expect(screen.getByTestId('provider-row-title-zai').textContent).toBe('Pay-as-you-go API · International')
  })
})

// ---------------------------------------------------------------------------
// #24 (settings-side) — renders catalog label verbatim
// ---------------------------------------------------------------------------

describe('ProvidersSection — #24 settings-side catalog label', () => {
  it('displays the catalog subtitle for the openrouter provider', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([OPENROUTER_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-openrouter'))

    const catalogEntry = CATALOG_PROVIDERS.find((e) => e.id === 'openrouter')
    expect(catalogEntry).toBeDefined()
    // Subtitle derived from the fetched catalog entry should appear in the row
    expect(screen.getByText(catalogSubtitle(catalogEntry!))).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Migration dataset — resolveCatalogEntry over the fetched catalog (spec test #12)
// ---------------------------------------------------------------------------

describe('resolveCatalogEntry — migration dataset', () => {
  const resolve = (id: string) => resolveCatalogEntry(CATALOG_PROVIDERS, id)

  it('#1 zai → Zhipu AI (canonical)', () => {
    const result = resolve('zai')
    expect(result.group).toBe('Zhipu AI')
    expect(result.entry?.id).toBe('zai')
  })

  it('#2 z-ai → Zhipu AI (alias)', () => {
    const result = resolve('z-ai')
    expect(result.group).toBe('Zhipu AI')
    expect(result.entry?.id).toBe('zai')
  })

  it('#3 zhipu → Zhipu AI (alias)', () => {
    const result = resolve('zhipu')
    expect(result.group).toBe('Zhipu AI')
    expect(result.entry?.id).toBe('zai')
  })

  it('#4 glm-coding → Zhipu AI (alias for zai-coding-plan)', () => {
    const result = resolve('glm-coding')
    expect(result.group).toBe('Zhipu AI')
    expect(result.entry?.id).toBe('zai-coding-plan')
  })

  it('#5 ollama → Ollama [first-class catalog provider, NOT Self-hosted/Custom]', () => {
    const result = resolve('ollama')
    expect(result.group).toBe('Ollama')
    expect(result.entry?.id).toBe('ollama')
  })

  it('#6 vllm → Self-hosted / Custom', () => {
    const result = resolve('vllm')
    expect(result.group).toBe(SELF_HOSTED_CUSTOM_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#7 litellm → Self-hosted / Custom', () => {
    const result = resolve('litellm')
    expect(result.group).toBe(SELF_HOSTED_CUSTOM_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#8 empty string → Generic (no crash)', () => {
    const result = resolve('')
    expect(result.group).toBe(GENERIC_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#9 zzz-unknown → Generic (raw id)', () => {
    const result = resolve('zzz-unknown')
    expect(result.group).toBe(GENERIC_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#10 z-ai-legacy-removed → Other (alias in no catalog entry, no throw)', () => {
    const result = resolve('z-ai-legacy-removed')
    expect(result.group).toBe(GENERIC_GROUP)
    expect(result.entry).toBeUndefined()
  })

  it('#11 an empty catalog (GET not yet resolved) never crashes and resolves to Other', () => {
    const result = resolveCatalogEntry([], 'zai')
    expect(result.group).toBe(GENERIC_GROUP)
    expect(result.entry).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// BrandDisclaimer visibility — FR-014 requires the trademark notice wherever
// brand marks appear (populated list AND the picker Sheet, which now carries
// the logos that used to live in the always-visible roster).
// ---------------------------------------------------------------------------

import { BRAND_DISCLAIMER_TEXT } from '@/components/ui/brand-disclaimer'

describe('ProvidersSection — BrandDisclaimer present wherever marks appear', () => {
  it('disclaimer renders on the populated configured-providers list', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-anthropic'))
    expect(screen.getByText(BRAND_DISCLAIMER_TEXT)).toBeInTheDocument()
  })

  it('disclaimer renders inside the picker Sheet (brand logos shown per catalog entry)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('provider-picker-sheet'))
    expect(screen.getByText(BRAND_DISCLAIMER_TEXT)).toBeInTheDocument()
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

  it('on a fetch error, does NOT also show the empty-state message — and "Connect a provider" stays reachable', async () => {
    vi.mocked(api.fetchProviders).mockRejectedValue(new Error('boom'))
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('providers-error')).toBeInTheDocument()
    })
    // The empty-state's own "No providers configured yet" copy must not render
    // alongside the error (previously both showed at once).
    expect(screen.queryByTestId('providers-empty-state')).not.toBeInTheDocument()
    expect(screen.queryByText(/no providers configured yet/i)).not.toBeInTheDocument()
    // The header "Connect a provider" CTA remains reachable despite the error.
    expect(screen.getByTestId('connect-provider-btn')).toBeInTheDocument()
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

  it('Test button → thrown ApiError shows the clean userMessage, not the legacy "status: message" string', async () => {
    vi.mocked(api.testProvider).mockRejectedValue(
      new api.ApiError(500, 'The server is unavailable. Please try again in a moment.'),
    )

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({
          variant: 'error',
          message: 'The server is unavailable. Please try again in a moment.',
        }),
      )
    })
    expect(addToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: expect.stringContaining('500:') }),
    )
  })

  it('Test button → a non-Error rejection falls back to a readable message, never the literal "undefined"', async () => {
    vi.mocked(api.testProvider).mockRejectedValue('boom')

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.click(screen.getByRole('button', { name: /^test$/i }))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: 'Connection test failed' }),
      )
    })
    expect(addToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: 'undefined' }),
    )
  })
})
