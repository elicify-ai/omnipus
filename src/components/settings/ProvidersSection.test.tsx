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
 *   - Exact catalog identity (catalogEntryById over the fetched catalog)
 *   - Settings-side catalog label consistency (US-7)
 *   - ADR-068 T068-25 (TDD row 27): the *Default model* card, *Set as default
 *     model…* from a row, the *Remove provider* flow and its no-Undo guarantee,
 *     and the shared <ProviderPicker> that replaced the local picker Sheet.
 *
 * Catalog source (ADR-068 FR-037 / T068-05): the registry-fed document from
 * GET /providers/catalog, mocked here via fetchProvidersCatalog with the
 * shared stub fixture — never a bundled catalog. GET /providers returns
 * configured rows only (FR-011a), so every fixture row carries the required
 * `auth_method` / `dependents` / `backs_default` fields.
 */

import * as React from 'react'
import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { act, render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// cmdk (the shared picker and the ModelSelector) needs ResizeObserver and
// scrollIntoView; jsdom has neither.
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
  // jsdom reports every element as 0x0, and @tanstack/react-virtual measures
  // the scroll element with offsetHeight — a zero-height viewport renders zero
  // rows, which would "pass" a not-rendered assertion for the wrong reason.
  // Give the picker's virtual viewport the height it has in a browser.
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute('data-testid') === 'picker-virtual-viewport' ? 480 : 0
    },
  })
})

// The ModelSelector's popover is stubbed to render in place, so its options are
// queryable without driving a portal. The selector itself stays real.
vi.mock('@/components/ui/popover', () => ({
  Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
  PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild && React.isValidElement(children) ? children : React.createElement('div', null, children),
  PopoverContent: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
  PopoverAnchor: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
}))

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
    deleteProvider: vi.fn(),
    getDefaultModel: vi.fn(),
    putDefaultModel: vi.fn(),
    checkEntitlement: vi.fn(),
    reAuth: vi.fn(),
    signOutProvider: vi.fn(),
    fetchSignInStatus: vi.fn(),
    isApiError: actual.isApiError,
  }
})

// SignInDialog itself is unit-tested in full in SignInDialog.test.tsx — here
// we only need to assert ProvidersSection opens it with the right target.
vi.mock('@/components/providers/SignInDialog', () => ({
  SignInDialog: ({ open, providerId, providerLabel }: { open: boolean; providerId: string; providerLabel: string }) =>
    open ? (
      <div data-testid="sign-in-dialog-stub">
        {providerLabel} ({providerId})
      </div>
    ) : null,
}))

// ManageSignInDialog and ReSignInDialog (T068-26) are unit-tested in full in
// their own files — here we only need to assert handleManage() dispatches to
// the right one with the right target, and that Sign out (surfaced by the
// stub, same as the real dialog's own button) reaches the shared mutation.
vi.mock('@/components/providers/ManageSignInDialog', () => ({
  ManageSignInDialog: ({
    open,
    providerLabel,
    accountLabel,
    onSignOut,
    signingOut,
  }: {
    open: boolean
    providerLabel: string
    accountLabel?: string
    onSignOut: () => void
    signingOut?: boolean
  }) =>
    open ? (
      <div data-testid="manage-sign-in-dialog-stub">
        {providerLabel} {accountLabel ? `(${accountLabel})` : '(no account label)'}
        <button
          type="button"
          onClick={onSignOut}
          disabled={signingOut}
          data-testid="manage-sign-out-btn-stub"
        >
          Sign out
        </button>
      </div>
    ) : null,
}))

vi.mock('@/components/providers/ReSignInDialog', () => ({
  ReSignInDialog: ({
    open,
    providerId,
    providerLabel,
    cliKind,
  }: {
    open: boolean
    providerId: string
    providerLabel: string
    cliKind: string
  }) =>
    open ? (
      <div data-testid="re-sign-in-dialog-stub">
        {providerLabel} ({providerId}) [{cliKind}]
      </div>
    ) : null,
}))

import * as api from '@/lib/api'
import { ProvidersSection } from './ProvidersSection'
import { catalogEntryById, catalogGroupName, UNGROUPED_PROVIDER_GROUP } from '@/lib/catalogDisplay'
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
  // The gateway sets this on EVERY row it serves (pkg/gateway/rest.go), and
  // anthropic is a catalog provider whose `models` the gateway fills, so the
  // fixture states it. It used to be omitted and the SPA guessed 'live' from a
  // hand-written id list; T067-13 deleted that guess (FR-011/FR-025), and an
  // omitted field now means 'manual' — an editable slug list, which is not what
  // this row is.
  has_models_endpoint: true,
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
  // A fresh install has no default model; describes that need one override this.
  vi.mocked(api.getDefaultModel).mockResolvedValue(null as never)
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
    // FR-021: the sheet's contents are the ONE shared picker, not a local list.
    expect(screen.getByTestId('settings-provider-picker')).toBeInTheDocument()
    expect(screen.getByTestId('picker-popular-openai')).toBeInTheDocument()
  })

  it('selecting a picker entry transitions to the connect form Sheet', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-popular-openai'))
    // First level: the company. Second level (FR-027/FR-028): plan x region x
    // auth method, confirmed with Continue — that is what settles the id.
    fireEvent.click(screen.getByTestId('picker-popular-openai'))
    await waitFor(() => screen.getByTestId('provider-detail-panel'))
    fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    })
    expect(screen.getByTestId('api-key-input-openai')).toBeInTheDocument()
    // The picker Sheet closed once a provider was confirmed.
    expect(screen.queryByTestId('provider-picker-sheet')).not.toBeInTheDocument()
  })

  it('carries the key typed in the second-level panel into the connect form', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-popular-openai'))
    fireEvent.click(screen.getByTestId('picker-popular-openai'))
    await waitFor(() => screen.getByTestId('provider-detail-panel'))
    fireEvent.change(screen.getByTestId('provider-detail-panel-api-key-input'), {
      target: { value: 'sk-typed-in-panel' },
    })
    fireEvent.click(screen.getByTestId('provider-detail-panel-continue'))

    await waitFor(() => screen.getByTestId('api-key-input-openai'))
    expect(screen.getByTestId('api-key-input-openai')).toHaveValue('sk-typed-in-panel')
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

  // REPLACES three tests that asserted the local picker Sheet EXCLUDED
  // already-configured catalog entries (and their aliases), and showed an
  // "all configured" dead end when nothing was left. The shared picker
  // (ADR-068 FR-021/FR-022) deliberately does the opposite: every catalog
  // provider stays offerable — a company can be configured more than once
  // across plans and regions — and the configured rows are surfaced as
  // *Recent* instead of being hidden. The still-valid coverage those tests
  // carried (a configured provider is visibly distinguished in the picker,
  // search narrows the list, and there is always a way forward) is preserved
  // below against the shared picker's own surface.
  it('surfaces an already-configured provider as Recent, and still offers every catalog entry', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...ANTHROPIC_PROVIDER, updated_at: '2026-08-20T10:00:00Z' },
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('settings-provider-picker'))

    expect(screen.getByTestId('picker-recent-anthropic')).toBeInTheDocument()
    // Not hidden from the catalog band either — a second variant may be wanted.
    expect(screen.getByTestId('picker-popular-anthropic')).toBeInTheDocument()
    expect(screen.getByTestId('picker-popular-openai')).toBeInTheDocument()
  })

  // REPLACES 'the picker excludes the canonical entry for an alias-stored
  // provider (z-ai → zai)', which asserted the opposite outcome from the same
  // setup because the deleted alias resolver mapped 'z-ai' onto 'zai'. Under
  // ADR-067 FR-030 `aliases[]` is search-only and identity is exact, so a row
  // stored under a non-catalog id is NOT the catalog row: it must surface as
  // ITS OWN Recent entry (never canonicalised to 'zai'), and the real 'zai'
  // catalog row must stay fully offered — an alias-stored row can never hide
  // it or masquerade as it. Adapted from exclusion-based assertions
  // (picker-entry-*, deleted with the local Sheet) onto the shared picker's
  // Recent/Popular surface; the underlying FR-030 invariant this pins is
  // additionally covered at the unit level below
  // ('catalogEntryById — exact provider identity').
  it('a row stored under an alias id never resolves to — or collides with — the real catalog entry (FR-030)', async () => {
    expect(CATALOG_PROVIDERS.find((e) => e.id === 'zai')?.aliases).toContain('z-ai')
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...CONFIGURED_BASE, id: 'z-ai', name: 'z-ai', display_name: 'z-ai', updated_at: '2026-08-20T10:00:00Z', has_models_endpoint: false, models: [] },
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('settings-provider-picker'))

    // The stored row surfaces as Recent under its OWN raw id — never
    // canonicalised to the entry it aliases.
    expect(screen.getByTestId('picker-recent-z-ai')).toBeInTheDocument()
    expect(screen.queryByTestId('picker-recent-zai')).not.toBeInTheDocument()
    // 'zai' itself is still fully offered, untouched by the alias-stored row —
    // exact identity means a row filed under a different id can never hide it.
    expect(screen.getByTestId('picker-popular-zai')).toBeInTheDocument()
  })

  it('a Recent row opens that provider\'s own config Sheet, not a connect form', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...ANTHROPIC_PROVIDER, updated_at: '2026-08-20T10:00:00Z' },
    ] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-recent-anthropic'))
    fireEvent.click(screen.getByTestId('picker-recent-anthropic'))

    await waitFor(() => expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument())
    expect(screen.getByText('Update the API key for this provider.')).toBeInTheDocument()
  })

  it('search narrows the shared picker to the matching company', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    await waitFor(() => screen.getByTestId('picker-search'))
    fireEvent.change(screen.getByTestId('picker-search'), { target: { value: 'zhipu' } })

    await waitFor(() => {
      expect(screen.getByTestId('picker-row-Zhipu AI')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('picker-row-OpenAI')).not.toBeInTheDocument()
  })

  it('never dead-ends: Custom endpoint is offered even when every catalog entry is configured', async () => {
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
    await waitFor(() => screen.getByTestId('settings-provider-picker'))

    expect(screen.getByTestId('picker-custom-endpoint')).toBeInTheDocument()
    expect(screen.queryByText(/all available providers are already configured/i)).not.toBeInTheDocument()
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
// Exact catalog identity — catalogEntryById over the fetched catalog
// (ADR-067 FR-011 / FR-030, US-11.AC2; T067-13)
//
// REPLACES the "migration dataset" block that exercised
// `resolveCatalogEntry` from the now-deleted src/lib/providerMigration.ts.
// The same dataset is kept — the SAME ids, the SAME catalog, and the same
// never-throws obligations — but the ORACLE moved: alias ids and the
// hand-written litellm/vllm "self-hosted" side list no longer resolve to a
// canonical entry, because ADR-067 makes provider identity exact and makes
// `aliases[]` search-only (FR-030). A stored id is a catalog id or it is not.
//
// That inversion is the point of the greenfield rule, so the alias cases are
// kept as NEGATIVE controls rather than dropped: if alias resolution ever
// comes back, these fail.
// ---------------------------------------------------------------------------

describe('catalogEntryById — exact provider identity (FR-011/FR-030)', () => {
  const resolve = (id: string) => {
    const entry = catalogEntryById(CATALOG_PROVIDERS, id)
    return { entry, group: catalogGroupName(entry) }
  }

  it('#1 zai → Zhipu AI (canonical catalog id)', () => {
    const result = resolve('zai')
    expect(result.entry?.id).toBe('zai')
    expect(result.group).toBe('Zhipu AI')
  })

  it('#2 z-ai does NOT resolve — an alias is not an identity (FR-030)', () => {
    // `zai.aliases` carries this string, and the gateway would answer
    // `unknown provider "z-ai"` for it (US-10.AC2). The SPA must agree.
    expect(CATALOG_PROVIDERS.find((e) => e.id === 'zai')?.aliases).toContain('z-ai')
    const result = resolve('z-ai')
    expect(result.entry).toBeUndefined()
    expect(result.group).toBe(UNGROUPED_PROVIDER_GROUP)
  })

  it('#3 zhipu does NOT resolve (alias)', () => {
    expect(resolve('zhipu').entry).toBeUndefined()
  })

  it('#4 glm-coding does NOT resolve (alias of zai-coding-plan)', () => {
    expect(CATALOG_PROVIDERS.find((e) => e.id === 'zai-coding-plan')?.aliases).toContain('glm-coding')
    const result = resolve('glm-coding')
    expect(result.entry).toBeUndefined()
    expect(result.group).toBe(UNGROUPED_PROVIDER_GROUP)
  })

  it('#5 ollama → Ollama (a first-class catalog provider, exact id)', () => {
    const result = resolve('ollama')
    expect(result.entry?.id).toBe('ollama')
    expect(result.group).toBe('Ollama')
  })

  it('#6 vllm → Other (no hand-written self-hosted table any more)', () => {
    const result = resolve('vllm')
    expect(result.entry).toBeUndefined()
    expect(result.group).toBe(UNGROUPED_PROVIDER_GROUP)
  })

  it('#7 litellm → Other', () => {
    const result = resolve('litellm')
    expect(result.entry).toBeUndefined()
    expect(result.group).toBe(UNGROUPED_PROVIDER_GROUP)
  })

  it('#8 empty string → Other (no crash)', () => {
    const result = resolve('')
    expect(result.entry).toBeUndefined()
    expect(result.group).toBe(UNGROUPED_PROVIDER_GROUP)
  })

  it('#9 zzz-unknown → Other (raw id)', () => {
    const result = resolve('zzz-unknown')
    expect(result.entry).toBeUndefined()
    expect(result.group).toBe(UNGROUPED_PROVIDER_GROUP)
  })

  it('#10 z-ai-legacy-removed → Other (no throw)', () => {
    const result = resolve('z-ai-legacy-removed')
    expect(result.entry).toBeUndefined()
    expect(result.group).toBe(UNGROUPED_PROVIDER_GROUP)
  })

  it('#11 an empty catalog (GET not yet resolved) never crashes and resolves to Other', () => {
    const entry = catalogEntryById([], 'zai')
    expect(entry).toBeUndefined()
    expect(catalogGroupName(entry)).toBe(UNGROUPED_PROVIDER_GROUP)
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
        // 7th arg (ADR-068 FR-037): the custom-endpoint pair, absent here.
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
        undefined,
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
        undefined,
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

// ---------------------------------------------------------------------------
// FR-033 / US-8 — a typed key is never lost on sheet close.
//
// Oracle: the spec's "Esc with a dirty key keeps the sheet open", "Discard
// clears the draft" and "Close behaviour by draft state" (5 rows) scenarios in
// docs/internal/specs/adr-068-providers-ux-spec.md. The pure matrix is asserted
// in src/hooks/use-draft-guard.test.ts (TDD row 9); these are the wiring tests
// that prove ProvidersSection's close handler obeys it, plus the accessibility
// row "the prompt does not move focus out of the sheet" (3.2.1).
// ---------------------------------------------------------------------------

describe('ProvidersSection — FR-033 draft-key preservation (US-8)', () => {
  const CONNECTED_PROVIDER = [
    {
      ...CONFIGURED_BASE,
      id: 'openrouter',
      name: 'openrouter',
      display_name: 'OpenRouter',
      has_models_endpoint: true,
      models: ['openrouter/auto'],
    },
  ]

  beforeEach(() => {
    vi.mocked(api.fetchProviders).mockResolvedValue(CONNECTED_PROVIDER as never)
  })

  async function openSheet() {
    await waitFor(() => screen.getByTestId('configure-btn-openrouter'))
    fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))
    return screen.getByTestId('api-key-input-openrouter') as HTMLInputElement
  }

  async function openSheetWithKey(value: string) {
    renderSection()
    const input = await openSheet()
    fireEvent.change(input, { target: { value } })
    return input
  }

  function pressEsc() {
    fireEvent.keyDown(screen.getByTestId('provider-config-sheet'), {
      key: 'Escape',
      code: 'Escape',
    })
  }

  it('Esc with a dirty key keeps the sheet open and shows the Discard key? prompt', async () => {
    await openSheetWithKey('sk-test-123')

    pressEsc()

    // Stays open — the sheet and the field are both still mounted.
    expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    const prompt = screen.getByTestId('discard-key-prompt')
    expect(within(prompt).getByText('Discard key?')).toBeInTheDocument()
    expect(within(prompt).getByRole('button', { name: 'Discard' })).toBeInTheDocument()
    expect(within(prompt).getByRole('button', { name: 'Keep editing' })).toBeInTheDocument()
  })

  it('Keep editing dismisses the prompt and leaves the key value untouched', async () => {
    const input = await openSheetWithKey('sk-test-123')

    pressEsc()
    fireEvent.click(screen.getByRole('button', { name: 'Keep editing' }))

    await waitFor(() => {
      expect(screen.queryByTestId('discard-key-prompt')).not.toBeInTheDocument()
    })
    expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
    expect(input).toHaveValue('sk-test-123')
  })

  it('the prompt does not move focus out of the sheet (WCAG 3.2.1)', async () => {
    await openSheetWithKey('sk-test-123')

    pressEsc()

    const sheet = screen.getByTestId('provider-config-sheet')
    expect(sheet.contains(document.activeElement)).toBe(true)
  })

  it('Discard closes the sheet and clears the draft — reopening shows an empty field', async () => {
    await openSheetWithKey('sk-test-123')

    pressEsc()
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }))

    await waitFor(() => {
      expect(screen.queryByTestId('provider-config-sheet')).not.toBeInTheDocument()
    })

    const reopened = await openSheet()
    expect(reopened).toHaveValue('')
    expect(screen.queryByTestId('discard-key-prompt')).not.toBeInTheDocument()
  })

  it('an overlay click with a dirty key keeps the sheet open and prompts', async () => {
    await openSheetWithKey('sk-test-123')

    // Radix defers a left-button pointer-down-outside to the following click,
    // so the gesture is both events on the overlay — the element rendered
    // immediately before the sheet inside the portal.
    const overlay = screen.getByTestId('provider-config-sheet').previousElementSibling
    expect(overlay).not.toBeNull()
    fireEvent.pointerDown(overlay as Element, { button: 0 })
    fireEvent.click(overlay as Element, { button: 0 })

    await waitFor(() => {
      expect(screen.getByTestId('discard-key-prompt')).toBeInTheDocument()
    })
    expect(screen.getByTestId('provider-config-sheet')).toBeInTheDocument()
  })

  it('an empty key closes on Esc with no prompt', async () => {
    renderSection()
    await openSheet()

    pressEsc()

    await waitFor(() => {
      expect(screen.queryByTestId('provider-config-sheet')).not.toBeInTheDocument()
    })
    expect(screen.queryByTestId('discard-key-prompt')).not.toBeInTheDocument()
  })

  it('a whitespace-only key counts as empty and closes on Esc with no prompt', async () => {
    await openSheetWithKey('   ')

    pressEsc()

    await waitFor(() => {
      expect(screen.queryByTestId('provider-config-sheet')).not.toBeInTheDocument()
    })
    expect(screen.queryByTestId('discard-key-prompt')).not.toBeInTheDocument()
  })

  it('explicit Cancel with a dirty key closes without a prompt and clears the draft', async () => {
    await openSheetWithKey('sk-test-123')

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => {
      expect(screen.queryByTestId('provider-config-sheet')).not.toBeInTheDocument()
    })
    expect(screen.queryByTestId('discard-key-prompt')).not.toBeInTheDocument()

    const reopened = await openSheet()
    expect(reopened).toHaveValue('')
  })

  it('a saved key is clean — Esc closes with no prompt after a successful save', async () => {
    // A non-blocking outcome keeps the sheet open after the save, which is the
    // only in-component way to reach the "saved" column of the outline.
    vi.mocked(api.configureProvider).mockResolvedValue({
      ...CONNECTED_PROVIDER[0],
      validation: {
        outcome: 'no_credit',
        message: 'Your OpenRouter key works, but the account has no credit.',
      },
    } as never)
    vi.mocked(api.reAuth).mockResolvedValue({
      verified: true,
      token: 'reauth_tok',
      expires_in: 300,
    } as never)

    await openSheetWithKey('sk-saved-key')
    fireEvent.click(screen.getByTestId('save-provider-openrouter'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(screen.getByTestId('save-validation-banner-openrouter')).toBeInTheDocument()
    })

    // The re-auth dialog was a second Radix dismissable layer on top of the
    // sheet; the sheet re-attaches its own Escape listener only once Radix's
    // layer bookkeeping has re-rendered after that unmount. Flush that before
    // pressing Esc, or the first key press is swallowed by the library, not by
    // the behaviour under test.
    await act(async () => { await Promise.resolve() })
    pressEsc()

    await waitFor(() => {
      expect(screen.queryByTestId('provider-config-sheet')).not.toBeInTheDocument()
    })
    expect(screen.queryByTestId('discard-key-prompt')).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// ADR-068 §8b sign-in row states (T068-33 initial signed_in/expired/Sign-out
// wiring; T068-26 replaces the row copy and action with Manage +
// ManageSignInDialog / ReSignInDialog). Gated primarily on the registry
// catalog entry's `auth_methods` (see ProviderRow.tsx's isSignInCapable).
// `openai-chatgpt` and `codex-cli` are two of the shared fixture's
// `auth_methods: ["sign_in"]` rows, so these fixtures exercise the catalog
// signal itself; the configured row's own `auth_method: "sign_in"` field is
// carried too, which is the fallback isSignInCapable takes for an id absent
// from the catalog. `codex-cli` also resolves `cli_kind: "codex"` from the
// shared catalog fixture (providers-catalog.json), which is what routes its
// `expired` Manage click to ReSignInDialog instead of SignInDialog.
// ---------------------------------------------------------------------------

const SIGNED_IN_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'openai-chatgpt',
  name: 'openai-chatgpt',
  display_name: 'openai-chatgpt',
  status: 'signed_in',
  auth_method: 'sign_in',
  account_label: 'user@example.com',
  models: [],
}

const SIGNED_IN_NO_LABEL_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'openai-chatgpt',
  name: 'openai-chatgpt',
  display_name: 'openai-chatgpt',
  status: 'signed_in',
  auth_method: 'sign_in',
  models: [],
}

// codex-cli — a `cli_login` row: the shared providers-catalog.json fixture
// resolves its entry to `protocol: "cli"`, `cli_kind: "codex"`, which is what
// routes its `expired` Manage click to ReSignInDialog.
const EXPIRED_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'codex-cli',
  name: 'codex-cli',
  display_name: 'codex-cli',
  status: 'expired',
  auth_method: 'sign_in',
  models: [],
}

// openai-chatgpt — a `device_code` row: its catalog entry carries no
// `cli_kind`, so its `expired` Manage click falls back to the ordinary
// SignInDialog (a fresh device-code approval, not a status re-check).
const EXPIRED_DEVICE_CODE_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'openai-chatgpt',
  name: 'openai-chatgpt',
  display_name: 'openai-chatgpt',
  status: 'expired',
  auth_method: 'sign_in',
  models: [],
}

describe('ProvidersSection — ADR-068 sign-in row states', () => {
  it('a signed_in row reads "Signed in · <label>" and Manage opens the account/sign-out dialog (FR-034)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([SIGNED_IN_PROVIDER] as never)
    renderSection()

    await waitFor(() => screen.getByTestId('provider-row-openai-chatgpt'))
    expect(screen.getByTestId('signed-in-badge-openai-chatgpt')).toHaveTextContent('Signed in · user@example.com')
    expect(screen.queryByTestId('configure-btn-openai-chatgpt')).not.toBeInTheDocument()
    expect(screen.queryByTestId('sign-in-btn-openai-chatgpt')).not.toBeInTheDocument()

    const manageBtn = screen.getByTestId('manage-btn-openai-chatgpt')
    expect(manageBtn).toHaveTextContent('Manage')

    expect(screen.queryByTestId('manage-sign-in-dialog-stub')).not.toBeInTheDocument()
    fireEvent.click(manageBtn)
    await waitFor(() => {
      expect(screen.getByTestId('manage-sign-in-dialog-stub')).toHaveTextContent('user@example.com')
    })
  })

  it('a signed_in row without an account label reads "Signed in" — no e-mail exists in auth.json (MAJ-006)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([SIGNED_IN_NO_LABEL_PROVIDER] as never)
    renderSection()

    const badge = await screen.findByTestId('signed-in-badge-openai-chatgpt')
    expect(badge).toHaveTextContent('Signed in')
    expect(badge.textContent).not.toContain('·')
  })

  it('an expired cli_login row (codex-cli) shows "Session expired" and Manage opens the re-sign-in dialog, not SignInDialog', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([EXPIRED_PROVIDER] as never)
    renderSection()

    await waitFor(() => screen.getByTestId('provider-row-codex-cli'))
    expect(screen.getByTestId('expired-badge-codex-cli')).toHaveTextContent('Session expired')
    const btn = screen.getByTestId('manage-btn-codex-cli')
    expect(btn).toHaveTextContent('Manage')

    expect(screen.queryByTestId('re-sign-in-dialog-stub')).not.toBeInTheDocument()
    fireEvent.click(btn)
    await waitFor(() => {
      const dialog = screen.getByTestId('re-sign-in-dialog-stub')
      expect(dialog).toHaveTextContent('codex-cli')
      expect(dialog).toHaveTextContent('[codex]')
    })
    // No fresh device-code / POST /sign-in flow for an expired cli_login row
    // (BDD "Expired session routes to re-sign-in", MAJ-006).
    expect(screen.queryByTestId('sign-in-dialog-stub')).not.toBeInTheDocument()
  })

  it('an expired device_code row (openai-chatgpt, no cli_kind) falls back to SignInDialog on Manage', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([EXPIRED_DEVICE_CODE_PROVIDER] as never)
    renderSection()

    await waitFor(() => screen.getByTestId('provider-row-openai-chatgpt'))
    const btn = screen.getByTestId('manage-btn-openai-chatgpt')
    fireEvent.click(btn)
    await waitFor(() => {
      expect(screen.getByTestId('sign-in-dialog-stub')).toHaveTextContent('openai-chatgpt')
    })
    expect(screen.queryByTestId('re-sign-in-dialog-stub')).not.toBeInTheDocument()
  })

  it('Sign out (via Manage) calls signOutProvider, refetches the provider list, and closes the dialog on success', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([SIGNED_IN_PROVIDER] as never)
    vi.mocked(api.signOutProvider).mockResolvedValue({ success: true })
    renderSection()

    await waitFor(() => screen.getByTestId('manage-btn-openai-chatgpt'))
    fireEvent.click(screen.getByTestId('manage-btn-openai-chatgpt'))
    await waitFor(() => screen.getByTestId('manage-sign-out-btn-stub'))
    fireEvent.click(screen.getByTestId('manage-sign-out-btn-stub'))

    await waitFor(() => {
      expect(api.signOutProvider).toHaveBeenCalledWith('openai-chatgpt')
    })
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Signed out', variant: 'success' }))
    })
    // fetchProviders is re-invoked on invalidation (initial render + refetch).
    await waitFor(() => {
      expect(vi.mocked(api.fetchProviders).mock.calls.length).toBeGreaterThan(1)
    })
    await waitFor(() => {
      expect(screen.queryByTestId('manage-sign-in-dialog-stub')).not.toBeInTheDocument()
    })
  })

  it('Sign out surfacing a server-reported failure shows the error and leaves the Manage dialog open', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([SIGNED_IN_PROVIDER] as never)
    vi.mocked(api.signOutProvider).mockResolvedValue({ success: false, error: 'could not delete credential' })
    renderSection()

    await waitFor(() => screen.getByTestId('manage-btn-openai-chatgpt'))
    fireEvent.click(screen.getByTestId('manage-btn-openai-chatgpt'))
    await waitFor(() => screen.getByTestId('manage-sign-out-btn-stub'))
    fireEvent.click(screen.getByTestId('manage-sign-out-btn-stub'))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: 'could not delete credential', variant: 'error' }),
      )
    })
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ message: 'Signed out' }))
    expect(screen.getByTestId('manage-sign-in-dialog-stub')).toBeInTheDocument()
  })

  it('a non-sign-in provider (regression) never shows Sign in/Manage controls', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
    renderSection()

    await waitFor(() => screen.getByTestId('provider-row-anthropic'))
    expect(screen.queryByTestId('sign-in-btn-anthropic')).not.toBeInTheDocument()
    expect(screen.queryByTestId('manage-btn-anthropic')).not.toBeInTheDocument()
    expect(screen.getByTestId('configure-btn-anthropic')).toBeInTheDocument()
  })

  it('confirming a sign_in auth method from the picker opens SignInDialog directly, not the API-key connect Sheet', async () => {
    // `openai-chatgpt` is one of the shared fixture's `auth_methods:
    // ["sign_in"]` rows — the same shape the served catalog carries, so this
    // asserts the real FR-005 branch rather than a bespoke entry.
    //
    // T068-25 replaced the local ProviderPickerSheet with the shared
    // two-level ProviderPicker, so the route to this branch is now
    // tile -> detail panel -> Continue (handleProviderConfirm) rather than a
    // single click on a flat picker row. The assertion is unchanged: a
    // sign_in row must never land in the API-key Sheet.
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    renderSection()

    await waitFor(() => screen.getByTestId('connect-provider-btn'))
    fireEvent.click(screen.getByTestId('connect-provider-btn'))
    // `openai-chatgpt` carries its own `company: "ChatGPT"` in the fixture —
    // it is NOT a variant under the OpenAI tile (that row is api_key-only), so
    // the route to it is the searchable all-providers list.
    await waitFor(() => screen.getByTestId('picker-search'))
    fireEvent.change(screen.getByTestId('picker-search'), { target: { value: 'chatgpt' } })
    await waitFor(() => screen.getByTestId('picker-row-ChatGPT'))
    fireEvent.click(screen.getByTestId('picker-row-ChatGPT'))

    await waitFor(() => screen.getByTestId('provider-detail-panel'))
    // Sign-in is this company's only method, so AuthMethodControl renders no
    // segmented control (a one-option segment would be a lie) and goes
    // straight to the Sign in button. That button is the affordance the row
    // offers, and it is wired to the section's SignInDialog — a click must
    // open the dialog rather than do nothing.
    expect(screen.queryByTestId('provider-detail-panel-auth-segment')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('provider-detail-panel-auth-signin-start'))

    await waitFor(() => {
      expect(screen.getByTestId('sign-in-dialog-stub')).toHaveTextContent('openai-chatgpt')
    })
    expect(screen.queryByTestId('provider-config-sheet')).not.toBeInTheDocument()
  })
})

// ===========================================================================
// ADR-068 T068-25 (TDD row 27) — the Default model card, the row action, and
// the Remove-provider flow.
// ===========================================================================

const CONNECTED_ANTHROPIC = {
  ...CONFIGURED_BASE,
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  models: ['claude-sonnet-4-5'],
}

const CONNECTED_OPENROUTER = {
  ...CONFIGURED_BASE,
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  has_models_endpoint: true,
  models: ['anthropic/claude-sonnet-4.6'],
}

const DEFAULT_PAIR = {
  provider: 'openrouter',
  model: 'z-ai/glm-5.2',
  context_window: 1048576,
  window_source: 'catalog',
}

describe('ProvidersSection — Default model card (FR-019)', () => {
  it('renders the card FIRST, reading provider · model and window · source', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue(DEFAULT_PAIR as never)
    renderSection()

    const card = await screen.findByTestId('default-model-card')
    expect(within(card).getByText('Default model')).toBeInTheDocument()
    // The card mounts immediately (skeleton); the pair arrives with the GET.
    expect(await screen.findByTestId('default-model-provider')).toHaveTextContent('OpenRouter')
    expect(screen.getByTestId('default-model-model')).toHaveTextContent('z-ai/glm-5.2')
    expect(screen.getByTestId('default-model-window')).toHaveTextContent('1,048,576')
    expect(screen.getByTestId('default-model-source')).toHaveTextContent('catalog')

    // "First" is a DOM-order claim, not a vibe: the card precedes every row.
    const row = await screen.findByTestId('provider-row-openrouter')
    expect(card.compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('renders an em dash for a window and source the server did not send', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue({
      provider: 'openrouter',
      model: 'z-ai/glm-5.2',
    } as never)
    renderSection()

    expect(await screen.findByTestId('default-model-window')).toHaveTextContent('—')
    expect(screen.getByTestId('default-model-source')).toHaveTextContent('—')
  })

  it('renders an em dash for an exempt row reporting context_window 0', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue({
      provider: 'codex-cli',
      model: 'gpt-5-codex',
      context_window: 0,
    } as never)
    renderSection()

    expect(await screen.findByTestId('default-model-window')).toHaveTextContent('—')
  })

  it('replaces the number with No context length + the pre-filled overrides link (X-08)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue({
      provider: 'ollama',
      model: 'llama3.3:70b',
      window_unknown: true,
    } as never)
    renderSection()

    expect(await screen.findByTestId('default-model-window')).toHaveTextContent('No context length')
    // O10: hash-prefixed so following it under the app's hash router is an
    // in-page navigation, not a full reload landing on the wrong screen.
    expect(screen.getByTestId('default-model-window-unknown-link')).toHaveAttribute(
      'href',
      '/#/settings?tab=models&provider=ollama&model=llama3.3%3A70b',
    )
    expect(screen.queryByTestId('default-model-source')).not.toBeInTheDocument()
  })

  it('says so plainly when no default model is set yet', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue(null as never)
    renderSection()

    expect(await screen.findByTestId('default-model-unset')).toBeInTheDocument()
    expect(screen.queryByTestId('default-model-window')).not.toBeInTheDocument()
  })

  // O11: a failed GET must never render as the same "No default model yet"
  // copy as a genuine empty state — that's a false statement of fact an
  // operator could act on by setting a default and clobbering the one that
  // was actually there but merely unreadable at that moment. These three
  // cases assert on what the reader actually SEES in each state, because the
  // defect was exactly that two of the three rendered identically.
  describe('default-model GET failure is distinct from "no default set" (O11)', () => {
    it('loaded WITH a value: shows the pair, not an error and not "not set"', async () => {
      vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
      vi.mocked(api.getDefaultModel).mockResolvedValue(DEFAULT_PAIR as never)
      renderSection()

      expect(await screen.findByTestId('default-model-provider')).toHaveTextContent('OpenRouter')
      expect(screen.getByTestId('default-model-model')).toHaveTextContent('z-ai/glm-5.2')
      expect(screen.queryByTestId('default-model-unset')).not.toBeInTheDocument()
      expect(screen.queryByTestId('default-model-error')).not.toBeInTheDocument()
    })

    it('loaded EMPTY (404 → null): shows the genuine "not set" copy, not an error', async () => {
      vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
      vi.mocked(api.getDefaultModel).mockResolvedValue(null as never)
      renderSection()

      expect(await screen.findByTestId('default-model-unset')).toHaveTextContent(
        'No default model yet — pick one so new agents have somewhere to run.',
      )
      expect(screen.queryByTestId('default-model-error')).not.toBeInTheDocument()
      expect(screen.queryByTestId('default-model-provider')).not.toBeInTheDocument()
    })

    it('FAILED (non-404 error): shows a distinct "could not load" message, never the "not set" copy', async () => {
      vi.mocked(api.fetchProviders).mockResolvedValue([CONNECTED_OPENROUTER] as never)
      vi.mocked(api.getDefaultModel).mockRejectedValue(
        new api.ApiError(500, 'The server is unavailable. Please try again in a moment.'),
      )
      renderSection()

      // The bug: this used to render `default-model-unset` with the false
      // "No default model yet" claim on a fetch failure. It must not.
      expect(await screen.findByTestId('default-model-error')).toHaveTextContent(
        'Could not load the default model. Please try again.',
      )
      expect(screen.queryByTestId('default-model-unset')).not.toBeInTheDocument()
      expect(screen.queryByTestId('default-model-provider')).not.toBeInTheDocument()
    })
  })

  it('Change offers models of connected/signed-in providers only, and PUTs the pick', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      CONNECTED_ANTHROPIC,
      { ...CONNECTED_OPENROUTER, status: 'error' },
      { ...CONFIGURED_BASE, id: 'groq', name: 'groq', display_name: 'Groq', status: 'disconnected', models: [] },
    ] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue(DEFAULT_PAIR as never)
    vi.mocked(api.putDefaultModel).mockResolvedValue({
      provider: 'anthropic',
      model: 'claude-sonnet-4-5',
    } as never)
    renderSection()

    await screen.findByTestId('default-model-provider')
    // The selector is not mounted until Change is pressed.
    expect(screen.queryByTestId('default-model-select')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('default-model-change-btn'))

    await waitFor(() => expect(screen.getByTestId('default-model-select')).toBeInTheDocument())
    // Anthropic is connected; OpenRouter is in error and Groq disconnected.
    expect(screen.getByTestId('default-model-option-claude-sonnet-4-5')).toBeInTheDocument()
    expect(screen.queryByTestId('default-model-option-anthropic/claude-sonnet-4.6')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('default-model-option-claude-sonnet-4-5'))

    await waitFor(() =>
      expect(api.putDefaultModel).toHaveBeenCalledWith({
        provider: 'anthropic',
        model: 'claude-sonnet-4-5',
      }),
    )
  })

  it('marks the row that backs the default, and only that row', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      CONNECTED_ANTHROPIC,
      CONNECTED_OPENROUTER,
    ] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue(DEFAULT_PAIR as never)
    renderSection()

    await screen.findByTestId('provider-row-openrouter')
    expect(screen.getByTestId('default-badge-openrouter')).toBeInTheDocument()
    expect(screen.queryByTestId('default-badge-anthropic')).not.toBeInTheDocument()
  })
})

describe('ProvidersSection — Set as default from the provider row (FR-019)', () => {
  it('opens the selector pre-filtered to that provider and performs the same PUT', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      CONNECTED_ANTHROPIC,
      CONNECTED_OPENROUTER,
    ] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue(DEFAULT_PAIR as never)
    vi.mocked(api.putDefaultModel).mockResolvedValue({
      provider: 'anthropic',
      model: 'claude-sonnet-4-5',
    } as never)
    renderSection()

    await screen.findByTestId('provider-row-anthropic')
    fireEvent.click(screen.getByTestId('set-default-btn-anthropic'))

    await waitFor(() => expect(screen.getByTestId('default-model-select')).toBeInTheDocument())
    // Pre-filtered: OpenRouter's models are not on offer even though it is connected.
    expect(screen.getByTestId('default-model-option-claude-sonnet-4-5')).toBeInTheDocument()
    expect(screen.queryByTestId('default-model-option-anthropic/claude-sonnet-4.6')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('default-model-option-claude-sonnet-4-5'))
    await waitFor(() =>
      expect(api.putDefaultModel).toHaveBeenCalledWith({
        provider: 'anthropic',
        model: 'claude-sonnet-4-5',
      }),
    )
  })

  it('offers no row action for a provider that cannot serve a turn', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...CONNECTED_OPENROUTER, status: 'error' },
    ] as never)
    renderSection()

    await screen.findByTestId('provider-row-openrouter')
    expect(screen.queryByTestId('set-default-btn-openrouter')).not.toBeInTheDocument()
  })
})

describe('ProvidersSection — Remove provider (FR-010, FR-016, FR-017)', () => {
  beforeEach(() => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      CONNECTED_ANTHROPIC,
      CONNECTED_OPENROUTER,
    ] as never)
    vi.mocked(api.getDefaultModel).mockResolvedValue(DEFAULT_PAIR as never)
    vi.mocked(api.deleteProvider).mockResolvedValue({
      deleted: true,
      dependents: [],
      default_changed: false,
    } as never)
  })

  async function openRemoveDialog(id: string) {
    renderSection()
    await screen.findByTestId(`provider-row-${id}`)
    fireEvent.click(screen.getByTestId(`configure-btn-${id}`))
    await waitFor(() => screen.getByTestId(`remove-provider-btn-${id}`))
    fireEvent.click(screen.getByTestId(`remove-provider-btn-${id}`))
    await waitFor(() => screen.getByTestId('remove-provider-dialog'))
  }

  it('the config sheet footer opens the confirmation, titled for that provider', async () => {
    await openRemoveDialog('anthropic')
    expect(screen.getByText('Remove Anthropic? Its key will be deleted.')).toBeInTheDocument()
    // Opening the dialog sends nothing.
    expect(api.deleteProvider).not.toHaveBeenCalled()
  })

  it('confirming DELETEs that provider with no replacement default', async () => {
    await openRemoveDialog('anthropic')
    fireEvent.click(screen.getByTestId('remove-provider-confirm'))

    await waitFor(() => expect(api.deleteProvider).toHaveBeenCalledWith('anthropic', undefined))
  })

  it('a default-backing row sends the inline new default with the DELETE', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      CONNECTED_ANTHROPIC,
      { ...CONNECTED_OPENROUTER, backs_default: true },
    ] as never)
    await openRemoveDialog('openrouter')

    expect(screen.getByTestId('remove-provider-confirm')).toBeDisabled()
    fireEvent.click(screen.getByTestId('new-default-model-claude-sonnet-4-5'))
    await waitFor(() => expect(screen.getByTestId('remove-provider-confirm')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('remove-provider-confirm'))

    await waitFor(() =>
      expect(api.deleteProvider).toHaveBeenCalledWith('openrouter', {
        provider: 'anthropic',
        model: 'claude-sonnet-4-5',
      }),
    )
  })

  it('leaves no Undo behind: no toast action, no restore request, nothing to click', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      await openRemoveDialog('anthropic')
      fireEvent.click(screen.getByTestId('remove-provider-confirm'))
      await waitFor(() => expect(api.deleteProvider).toHaveBeenCalledTimes(1))
      await waitFor(() =>
        expect(addToast).toHaveBeenCalledWith({ message: 'Provider removed', variant: 'success' }),
      )

      // FR-017: the toast carries copy and nothing else — no action button.
      for (const [toast] of addToast.mock.calls) {
        expect(toast).not.toHaveProperty('action')
      }
      expect(screen.queryByText(/undo/i)).not.toBeInTheDocument()

      // …and nothing tries to put the provider back, ever.
      await act(async () => {
        vi.advanceTimersByTime(10_000)
      })
      expect(api.configureProvider).not.toHaveBeenCalled()
      expect(api.putDefaultModel).not.toHaveBeenCalled()
      expect(api.deleteProvider).toHaveBeenCalledTimes(1)
      expect(screen.queryByText(/undo/i)).not.toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('a failed DELETE reports the server error and keeps the dialog open', async () => {
    vi.mocked(api.deleteProvider).mockRejectedValue(
      new api.ApiError(409, 'provider backs the default model; supply new_default'),
    )
    await openRemoveDialog('anthropic')
    fireEvent.click(screen.getByTestId('remove-provider-confirm'))

    await waitFor(() =>
      expect(addToast).toHaveBeenCalledWith({
        message: 'provider backs the default model; supply new_default',
        variant: 'error',
      }),
    )
    expect(screen.getByTestId('remove-provider-dialog')).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// ADR-068 FR-031/T068-27 — "Check with my account" (TDD row 27, row-expand
// entitlement describes) + FR-032 row expand (limits + window source).
// ---------------------------------------------------------------------------

// A configured OpenAI row whose catalog entry's models are overridden to the
// exact A/B/C dataset the spec's "Check with my account greys unavailable
// models" scenario names, so the test asserts the literal scenario rather
// than an approximation built from the real fixture's gpt-5/gpt-5-mini/o4-mini.
const ENTITLEMENT_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'openai',
  name: 'openai',
  display_name: 'OpenAI',
  models: [],
}

function catalogWithModels(providerId: string, models: unknown[]) {
  return {
    ...PROVIDERS_CATALOG,
    providers: PROVIDERS_CATALOG.providers.map((p) =>
      p.id === providerId ? { ...p, models } : p,
    ),
  }
}

const MODEL_A = {
  id: 'A', name: 'Model A', context_window: 128000, max_output_tokens: 4096,
  input_modalities: ['text'], tool_call: true, status: 'active',
}
const MODEL_B = {
  id: 'B', name: 'Model B', context_window: 128000, max_output_tokens: 4096,
  input_modalities: ['text'], tool_call: true, status: 'active',
}
const MODEL_C = {
  id: 'C', name: 'Model C', context_window: 200000, max_output_tokens: 8192,
  input_modalities: ['text', 'image', 'pdf'], tool_call: true, status: 'active',
}

const ENTITLEMENT_RESPONSE = {
  models: [
    { id: 'A', entitled: true, limits: 'known' },
    { id: 'B', entitled: false, limits: 'known' },
    { id: 'C', entitled: true, limits: 'known' },
    { id: 'Z', entitled: true, limits: 'unknown' },
  ],
  checked_at: '2026-08-22T10:15:00Z',
  cached: false,
}

async function expandRow(id: string) {
  fireEvent.click(screen.getByTestId(`provider-row-expand-toggle-${id}`))
  await waitFor(() => screen.getByTestId(`model-limits-${id}`))
}

describe('ProvidersSection — Check with my account (FR-031)', () => {
  beforeEach(() => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ENTITLEMENT_PROVIDER] as never)
    vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(
      catalogWithModels('openai', [MODEL_A, MODEL_B, MODEL_C]) as never,
    )
  })

  it('greys models the key cannot reach and flags catalog-unknown models as limits unknown', async () => {
    vi.mocked(api.checkEntitlement).mockResolvedValue(ENTITLEMENT_RESPONSE as never)
    renderSection()

    await screen.findByTestId('provider-row-openai')
    fireEvent.click(screen.getByTestId('check-entitlement-btn-openai'))
    await waitFor(() => expect(api.checkEntitlement).toHaveBeenCalledWith('openai'))

    await expandRow('openai')

    // B: in the catalog, absent from the live listing → greyed, "not
    // available on this key".
    expect(screen.getByTestId('model-limit-not-entitled-openai-B')).toHaveTextContent(
      'not available on this key',
    )
    // A and C are entitled — no "not available" badge for either.
    expect(screen.queryByTestId('model-limit-not-entitled-openai-A')).not.toBeInTheDocument()
    expect(screen.queryByTestId('model-limit-not-entitled-openai-C')).not.toBeInTheDocument()
    // Z: the provider reported it, the catalog does not carry it → "limits unknown".
    expect(screen.getByTestId('model-limit-limits-unknown-openai-Z')).toHaveTextContent(
      'limits unknown',
    )
    expect(screen.getByTestId('model-limit-row-openai-Z')).toBeInTheDocument()

    // Exactly one upstream request; a second click is a client-side no-op —
    // the result already in hand is shown, nothing new is requested.
    expect(api.checkEntitlement).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByTestId('check-entitlement-btn-openai'))
    expect(api.checkEntitlement).toHaveBeenCalledTimes(1)
  })

  it('upstream failure leaves the list unchanged and shows an inline warning', async () => {
    vi.mocked(api.checkEntitlement).mockRejectedValue(
      new api.ApiError(502, 'could not fetch upstream model list: status 429'),
    )
    renderSection()

    await screen.findByTestId('provider-row-openai')
    fireEvent.click(screen.getByTestId('check-entitlement-btn-openai'))

    await waitFor(() => {
      expect(screen.getByTestId('entitlement-error-openai')).toHaveTextContent(
        'could not fetch upstream model list: status 429',
      )
    })

    await expandRow('openai')
    // Nothing is greyed — the list is unchanged.
    expect(screen.queryByTestId('model-limit-not-entitled-openai-A')).not.toBeInTheDocument()
    expect(screen.queryByTestId('model-limit-not-entitled-openai-B')).not.toBeInTheDocument()
    expect(screen.queryByTestId('model-limit-not-entitled-openai-C')).not.toBeInTheDocument()
  })

  it('is not offered for a cli-protocol row (409 by construction)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...ENTITLEMENT_PROVIDER, id: 'codex-cli', name: 'codex-cli', protocol: 'cli' },
    ] as never)
    renderSection()
    await screen.findByTestId('provider-row-codex-cli')
    expect(screen.queryByTestId('check-entitlement-btn-codex-cli')).not.toBeInTheDocument()
  })

  it('is not offered for a custom row (no catalog entry to intersect against)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([
      { ...ENTITLEMENT_PROVIDER, id: 'my-custom', name: 'my-custom', custom: true },
    ] as never)
    renderSection()
    await screen.findByTestId('provider-row-my-custom')
    expect(screen.queryByTestId('check-entitlement-btn-my-custom')).not.toBeInTheDocument()
  })

  it('discards an in-flight result if the row is deleted before it resolves', async () => {
    let resolveCheck: (value: unknown) => void = () => {}
    vi.mocked(api.checkEntitlement).mockReturnValue(
      new Promise((resolve) => { resolveCheck = resolve }) as never,
    )
    vi.mocked(api.deleteProvider).mockResolvedValue({
      deleted: true,
      dependents: [],
      default_changed: false,
    } as never)
    renderSection()

    await screen.findByTestId('provider-row-openai')
    fireEvent.click(screen.getByTestId('check-entitlement-btn-openai'))
    await waitFor(() => expect(api.checkEntitlement).toHaveBeenCalledWith('openai'))

    // Provider removed while the check is still in flight — GET /providers
    // now returns nothing, exactly the "row no longer exists" edge case.
    vi.mocked(api.fetchProviders).mockResolvedValue([] as never)
    fireEvent.click(screen.getByTestId('configure-btn-openai'))
    await waitFor(() => screen.getByTestId('remove-provider-btn-openai'))
    fireEvent.click(screen.getByTestId('remove-provider-btn-openai'))
    await waitFor(() => screen.getByTestId('remove-provider-dialog'))
    fireEvent.click(screen.getByTestId('remove-provider-confirm'))
    await waitFor(() => expect(api.deleteProvider).toHaveBeenCalledWith('openai', undefined))
    await waitFor(() => expect(screen.getByTestId('providers-empty-state')).toBeInTheDocument())

    // The in-flight entitlement result lands AFTER the row is gone — it must
    // not resurrect the row or throw.
    await act(async () => {
      resolveCheck(ENTITLEMENT_RESPONSE)
      await Promise.resolve()
    })
    expect(screen.queryByTestId('provider-row-openai')).not.toBeInTheDocument()
  })
})

describe('ProvidersSection — Row expand shows limits and window source (FR-032)', () => {
  it('shows context_window · max_output_tokens · image · PDF and renders — for an unresolved window source', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ENTITLEMENT_PROVIDER] as never)
    vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(
      catalogWithModels('openai', [MODEL_A, MODEL_C]) as never,
    )
    renderSection()

    await screen.findByTestId('provider-row-openai')
    await expandRow('openai')

    expect(screen.getByTestId('model-limit-window-openai-A')).toHaveTextContent('128,000')
    expect(screen.getByTestId('model-limit-output-openai-A')).toHaveTextContent('4,096')
    // Model A carries only "text" — no image/PDF support.
    expect(screen.getByTestId('model-limit-image-openai-A')).toHaveTextContent('—')
    expect(screen.getByTestId('model-limit-pdf-openai-A')).toHaveTextContent('—')
    // Model C carries image + pdf.
    expect(screen.getByTestId('model-limit-image-openai-C')).toHaveTextContent('Image')
    expect(screen.getByTestId('model-limit-pdf-openai-C')).toHaveTextContent('PDF')
    // Before ADR-066 D9 lands, the fixture carries no window_source — the
    // cell renders the em dash, never a guess.
    expect(screen.getByTestId('model-limit-source-openai-A')).toHaveTextContent('—')
  })

  it('renders the resolved value once a model carries window_source', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ENTITLEMENT_PROVIDER] as never)
    vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(
      catalogWithModels('openai', [{ ...MODEL_A, window_source: 'catalog' }]) as never,
    )
    renderSection()

    await screen.findByTestId('provider-row-openai')
    await expandRow('openai')

    expect(screen.getByTestId('model-limit-source-openai-A')).toHaveTextContent('catalog')
  })

  it('a local model with window_unknown renders "No context length" with a pre-filled Settings → Models link (X-08)', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([ENTITLEMENT_PROVIDER] as never)
    vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(
      catalogWithModels('openai', [{ ...MODEL_A, window_unknown: true }]) as never,
    )
    renderSection()

    await screen.findByTestId('provider-row-openai')
    await expandRow('openai')

    expect(screen.getByTestId('model-limit-window-openai-A')).toHaveTextContent('No context length')
    const link = screen.getByTestId('model-limit-window-unknown-link-openai-A')
    // O10: hash-prefixed so following it under the app's hash router is an
    // in-page navigation, not a full reload landing on the wrong screen.
    expect(link).toHaveAttribute('href', '/#/settings?tab=models&provider=openai&model=A')
  })
})
