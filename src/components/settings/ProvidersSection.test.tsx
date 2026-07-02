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
 *   - FIX-5: Endpoint-format toggle + "Anthropic endpoint" row chip
 *   - Migration dataset (resolveCatalogEntry, incl. anthropic_id case)
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

  it('search filters the picker catalog by company/label', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('provider-picker-sheet'))
    fireEvent.change(screen.getByTestId('picker-search-input'), { target: { value: 'zhipu' } })
    await waitFor(() => {
      expect(screen.getByTestId('picker-entry-z-ai')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('picker-entry-openai')).not.toBeInTheDocument()
  })

  it('shows an "all configured" message when every catalog entry is already configured', async () => {
    const allConfigured = PROVIDER_CATALOG.map((e) => ({
      id: e.id,
      name: e.id,
      display_name: e.label,
      status: 'connected',
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
    await waitFor(() => screen.getByTestId('provider-group-Zhipu / GLM'))
    expect(screen.queryByText(/add another/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('add-another-Zhipu / GLM')).not.toBeInTheDocument()
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

  it('grouped row title omits the company prefix (already in the group header)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      ZHIPU_STD_PROVIDER,
      ZHIPU_CODING_PROVIDER,
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-title-z-ai-coding'))
    const title = screen.getByTestId('provider-row-title-z-ai-coding').textContent
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
    // Zhipu / GLM (2 variants) is grouped.
    expect(screen.getByTestId('provider-group-Zhipu / GLM')).toBeInTheDocument()
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
    await waitFor(() => screen.getByTestId('configure-btn-z-ai-coding'))
    fireEvent.click(screen.getByTestId('configure-btn-z-ai-coding'))
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

    const apiKeyInput = screen.getByTestId('api-key-input-z-ai-coding')
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
    await waitFor(() => screen.getByTestId('provider-row-z-ai'))
    const allText = document.body.textContent ?? ''
    expect(allText).not.toMatch(/Standard API/)
    expect(allText).not.toMatch(/Anthropic-compatible/)
    expect(allText).toMatch(/Pay-as-you-go API/)
    expect(allText).toMatch(/Coding Plan/)
    expect(screen.getByTestId('provider-row-title-z-ai').textContent).toBe('Pay-as-you-go API · International')
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

    const catalogEntry = PROVIDER_CATALOG.find((e) => e.id === 'openrouter')
    expect(catalogEntry).toBeDefined()
    // Subtitle from catalog should appear in the row
    expect(screen.getByText(catalogEntry!.subtitle)).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// FIX-5 — Endpoint-format toggle + "Anthropic endpoint" chip
// ---------------------------------------------------------------------------

describe('ProvidersSection — FIX-5 endpoint-format toggle', () => {
  it('the connect Sheet shows the toggle for a dual-wire entry (z-ai has anthropic_id)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-entry-z-ai'))
    fireEvent.click(screen.getByTestId('picker-entry-z-ai'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    expect(screen.getByTestId('endpoint-format-toggle-z-ai')).toBeInTheDocument()
    expect(screen.getByTestId('endpoint-format-openai-z-ai')).toBeInTheDocument()
    expect(screen.getByTestId('endpoint-format-anthropic-z-ai')).toBeInTheDocument()
    // OpenAI-compatible is the default selection.
    expect(screen.getByTestId('endpoint-format-openai-z-ai')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText(/same account and api key/i)).toBeInTheDocument()
  })

  it('the connect Sheet shows NO toggle for a single-wire entry (openai has no anthropic_id)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-entry-openai'))
    fireEvent.click(screen.getByTestId('picker-entry-openai'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))
    expect(screen.queryByTestId('endpoint-format-toggle-openai')).not.toBeInTheDocument()
  })

  it('selecting Anthropic-compatible and connecting submits configureProvider with the anthropic_id', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureProvider).mockResolvedValue({ ...ZHIPU_STD_PROVIDER, id: 'z-ai-anthropic' } as never)
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)

    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-entry-z-ai'))
    fireEvent.click(screen.getByTestId('picker-entry-z-ai'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.click(screen.getByTestId('endpoint-format-anthropic-z-ai'))
    expect(screen.getByTestId('endpoint-format-anthropic-z-ai')).toHaveAttribute('aria-checked', 'true')

    fireEvent.change(screen.getByTestId('api-key-input-z-ai'), { target: { value: 'sk-zai-secret' } })
    fireEvent.click(screen.getByTestId('save-provider-z-ai'))

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.configureProvider).toHaveBeenCalledWith(
        'z-ai-anthropic',
        'sk-zai-secret',
        undefined,
        undefined,
        'reauth_tok',
        undefined,
      )
    })
  })

  it('a provider configured under an anthropic_id shows the muted "Anthropic endpoint" chip on its row', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...ZHIPU_STD_PROVIDER, id: 'z-ai-anthropic', name: 'z-ai-anthropic' },
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-z-ai-anthropic'))
    expect(screen.getByTestId('anthropic-endpoint-chip-z-ai-anthropic')).toBeInTheDocument()
    expect(screen.getByText('Anthropic endpoint')).toBeInTheDocument()
  })

  it('a provider configured under the primary (openai-compatible) id shows NO chip', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ZHIPU_STD_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-z-ai'))
    expect(screen.queryByTestId('anthropic-endpoint-chip-z-ai')).not.toBeInTheDocument()
    expect(screen.queryByText('Anthropic endpoint')).not.toBeInTheDocument()
  })

  it('editing a provider already configured under anthropic_id defaults the toggle to Anthropic-compatible', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...ZHIPU_STD_PROVIDER, id: 'z-ai-anthropic', name: 'z-ai-anthropic' },
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-z-ai-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-z-ai-anthropic'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))
    expect(screen.getByTestId('endpoint-format-anthropic-z-ai')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('endpoint-format-openai-z-ai')).toHaveAttribute('aria-checked', 'false')
  })
})

// ---------------------------------------------------------------------------
// Migration dataset — resolveCatalogEntry (spec test #12 + FIX-5 anthropic_id)
// ---------------------------------------------------------------------------

describe('resolveCatalogEntry — migration dataset', () => {
  it('#1 z-ai → Zhipu / GLM (canonical)', () => {
    const result = resolveCatalogEntry('z-ai')
    expect(result.group).toBe('Zhipu / GLM')
    expect(result.entry?.id).toBe('z-ai')
    expect(result.viaAnthropicId).toBeUndefined()
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

  it('#5 ollama → Ollama (local) [first-class catalog provider, NOT Self-hosted/Custom]', () => {
    const result = resolveCatalogEntry('ollama')
    expect(result.group).toBe('Ollama (local)')
    expect(result.entry?.id).toBe('ollama')
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

  it('#10 z-ai-legacy-removed → Other (alias in no catalog entry, no throw)', () => {
    const result = resolveCatalogEntry('z-ai-legacy-removed')
    expect(result.group).toBe(GENERIC_GROUP)
    expect(result.entry).toBeUndefined()
  })

  // [FIX-5] anthropic_id resolution — a stored id of the merged Anthropic-wire
  // sibling protocol resolves to the PRIMARY catalog entry, flagged so the row
  // can show the "Anthropic endpoint" chip.
  it('#11 z-ai-anthropic → Zhipu / GLM entry via anthropic_id (viaAnthropicId: true)', () => {
    const result = resolveCatalogEntry('z-ai-anthropic')
    expect(result.group).toBe('Zhipu / GLM')
    expect(result.entry?.id).toBe('z-ai')
    expect(result.viaAnthropicId).toBe(true)
  })

  it('#12 moonshot-anthropic → Moonshot / Kimi entry via anthropic_id (viaAnthropicId: true)', () => {
    const result = resolveCatalogEntry('moonshot-anthropic')
    expect(result.group).toBe('Moonshot / Kimi')
    expect(result.entry?.id).toBe('moonshot')
    expect(result.viaAnthropicId).toBe(true)
  })

  it('#13 coding-plan-anthropic → Qwen / Alibaba entry via anthropic_id (viaAnthropicId: true)', () => {
    const result = resolveCatalogEntry('coding-plan-anthropic')
    expect(result.group).toBe('Qwen / Alibaba')
    expect(result.entry?.id).toBe('coding-plan')
    expect(result.viaAnthropicId).toBe(true)
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
