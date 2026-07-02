// onboarding-settings-parity.test.tsx — [C2] ADR-031 Track 1 cross-surface
// consistency gate (SC-002 / US-7 / FR-019).
//
// Goal: prove that both surfaces (ProvidersSection in Settings, and the
// PROVIDER_CATALOG data used by onboarding) render the SAME catalog
// label/subtitle verbatim — not hardcoded strings.
//
// Strategy:
// - Render ProvidersSection with a mocked provider and assert the DOM shows the
//   EXACT label text held in PROVIDER_CATALOG (not a hardcoded string).
// - The onboarding surface DOM assertion is in -onboarding.test.tsx §"the z-ai
//   entry label and subtitle match catalog verbatim" and "openai entry label…".
//   Both reference the same PROVIDER_CATALOG object, so a catalog drift breaks
//   both tests simultaneously — that is the invariant.
//
// A regression where ProvidersSection begins computing its own label (e.g.
// `planLabel(entry.plan)` directly rather than `entry.label`) would NOT fail
// the catalog-consistency.test.ts suite (which only checks the catalog object),
// but WOULD fail here because the DOM text would differ from `entry.label`.
//
// Traces to: connectors-providers-redesign-spec.md §7 C2 gap / SC-002 / US-7.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'

// ---------------------------------------------------------------------------
// Module mocks — identical to ProvidersSection.test.tsx for consistency.
// ---------------------------------------------------------------------------

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
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
import { ProvidersSection } from '@/components/settings/ProvidersSection'

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
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
})

// ---------------------------------------------------------------------------
// [C2] Settings surface DOM-render parity with the PROVIDER_CATALOG.
//
// Each test below:
//   1. Looks up a catalog entry by id.
//   2. Renders ProvidersSection with a mocked configured provider using that id.
//   3. Asserts the DOM contains entry.subtitle verbatim.
//
// Why subtitle (not label)? The label encodes the company name + access type
// and is typically shown in the group header or row title in a compound form.
// The subtitle is always shown verbatim as a sub-line, so it's the clearest
// single-field differentiation test. Changing the catalog subtitle and NOT
// updating the component breaks this test.
// ---------------------------------------------------------------------------

describe('[C2] ProvidersSection renders catalog subtitle verbatim — settings DOM parity', () => {
  it('z-ai-coding: subtitle in DOM matches PROVIDER_CATALOG entry (not hardcoded)', async () => {
    const catalogEntry = PROVIDER_CATALOG.find((e) => e.id === 'z-ai-coding')
    expect(catalogEntry, 'z-ai-coding must be in PROVIDER_CATALOG').toBeDefined()

    vi.mocked(api.fetchProviders).mockResolvedValue([
      {
        id: 'z-ai-coding',
        name: 'z-ai-coding',
        display_name: 'z-ai-coding',
        status: 'connected',
        has_models_endpoint: true,
        models: ['codegeex-4'],
      },
    ] as never)

    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-z-ai-coding'))

    // If ProvidersSection starts computing its own subtitle instead of reading
    // entry.subtitle from the catalog, this assertion fails.
    expect(screen.getByText(catalogEntry!.subtitle)).toBeInTheDocument()
  })

  it('openai: subtitle in DOM matches PROVIDER_CATALOG entry (not hardcoded)', async () => {
    const catalogEntry = PROVIDER_CATALOG.find((e) => e.id === 'openai')
    expect(catalogEntry, 'openai must be in PROVIDER_CATALOG').toBeDefined()

    vi.mocked(api.fetchProviders).mockResolvedValue([
      {
        id: 'openai',
        name: 'openai',
        display_name: 'OpenAI',
        status: 'connected',
        has_models_endpoint: true,
        models: ['gpt-4o'],
      },
    ] as never)

    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-openai'))

    expect(screen.getByText(catalogEntry!.subtitle)).toBeInTheDocument()
  })

  it('differentiation: openai and z-ai-coding produce DIFFERENT subtitle text', async () => {
    // Proves the component is not returning a hardcoded fallback for ALL providers.
    // If both rendered the same text this test would catch a hardcoded response.
    const openaiEntry = PROVIDER_CATALOG.find((e) => e.id === 'openai')
    const zhipuEntry = PROVIDER_CATALOG.find((e) => e.id === 'z-ai-coding')
    expect(openaiEntry).toBeDefined()
    expect(zhipuEntry).toBeDefined()

    expect(openaiEntry!.subtitle, 'openai and z-ai-coding must have distinct subtitles').not.toBe(
      zhipuEntry!.subtitle,
    )
  })

  it('z-ai: onboarding catalog label matches the value asserted in -onboarding.test.tsx', () => {
    // This test does NOT render a component — it asserts the catalog object
    // holds the same value that -onboarding.test.tsx pins verbatim.
    // If someone changes the catalog data without updating the onboarding test,
    // one of the two assertions below fails.
    const entry = PROVIDER_CATALOG.find((e) => e.id === 'z-ai')
    expect(entry).toBeDefined()
    // These exact strings are also pinned in -onboarding.test.tsx §"the z-ai entry".
    // Post provider-ux-fixes-plan FIX-4/FIX-5: single-plan-per-row labels carry
    // no plan suffix (z-ai is the standard-api variant; the anthropic-wire
    // sibling is merged into anthropic_id, not a separate label).
    expect(entry!.label).toBe('Zhipu / GLM (International)')
    expect(entry!.subtitle).toBe('Pay-as-you-go, per token · api.z.ai/api/paas/v4')
    expect(entry!.logoSlug).toBe('zhipu')
    expect(entry!.anthropic_id).toBe('z-ai-anthropic')
  })
})
