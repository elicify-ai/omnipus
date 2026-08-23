// onboarding-settings-parity.test.tsx — [C2] cross-surface consistency gate
// (SC-002 / US-7 / FR-019), re-based on the registry-fed catalog (ADR-068
// FR-037 / T068-05).
//
// Goal: prove that both surfaces (ProvidersSection in Settings, and the
// onboarding picker) render the SAME display strings derived from the SAME
// fetched CatalogProvider — via src/lib/catalogDisplay.ts — not hardcoded
// strings and not a bundled catalog.
//
// Strategy:
// - Mock fetchProvidersCatalog with the shared stub document and render
//   ProvidersSection with a configured provider; assert the DOM shows the
//   EXACT subtitle catalogSubtitle() derives for that entry.
// - The onboarding surface DOM assertion is in -onboarding.test.tsx; both
//   reference the same stub document and the same derivation helpers, so a
//   drift in either breaks both tests — that is the invariant.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PROVIDERS_CATALOG_STUB, STUB_PROVIDERS } from '@/test/fixtures/providersCatalog.stub'
import { catalogLabel, catalogLogoSlug, catalogSubtitle } from '@/lib/catalogDisplay'

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
    fetchProvidersCatalog: vi.fn(),
    configureProvider: vi.fn(),
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
  vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG_STUB)
})

const configured = (id: string, display_name: string, models: string[]) =>
  ({ id, name: id, display_name, status: 'connected', auth_method: 'api_key', dependents: [], backs_default: false, models }) as never

// ---------------------------------------------------------------------------
// [C2] Settings surface DOM-render parity with the fetched catalog.
//
// Why subtitle (not label)? The label is typically shown in the group header
// or row title in a compound form. The subtitle is always shown verbatim as
// a sub-line, so it's the clearest single-field differentiation test.
// ---------------------------------------------------------------------------

describe('[C2] ProvidersSection renders the catalog-derived subtitle verbatim — settings DOM parity', () => {
  it('zai-coding-plan: subtitle in DOM matches catalogSubtitle(entry) (not hardcoded)', async () => {
    const catalogEntry = STUB_PROVIDERS.find((e) => e.id === 'zai-coding-plan')
    expect(catalogEntry, 'zai-coding-plan must be in the stub catalog').toBeDefined()

    vi.mocked(api.fetchProviders).mockResolvedValue([configured('zai-coding-plan', 'zai-coding-plan', ['glm-5.2'])])

    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-zai-coding-plan'))

    expect(screen.getByText(catalogSubtitle(catalogEntry!))).toBeInTheDocument()
  })

  it('openai: subtitle in DOM matches catalogSubtitle(entry) (not hardcoded)', async () => {
    const catalogEntry = STUB_PROVIDERS.find((e) => e.id === 'openai')
    expect(catalogEntry, 'openai must be in the stub catalog').toBeDefined()

    vi.mocked(api.fetchProviders).mockResolvedValue([configured('openai', 'OpenAI', ['gpt-5'])])

    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-openai'))

    expect(screen.getByText(catalogSubtitle(catalogEntry!))).toBeInTheDocument()
  })

  it('differentiation: openai and zai-coding-plan produce DIFFERENT subtitle text', () => {
    const openaiEntry = STUB_PROVIDERS.find((e) => e.id === 'openai')
    const zhipuEntry = STUB_PROVIDERS.find((e) => e.id === 'zai-coding-plan')
    expect(openaiEntry).toBeDefined()
    expect(zhipuEntry).toBeDefined()

    expect(catalogSubtitle(openaiEntry!), 'openai and zai-coding-plan must have distinct subtitles').not.toBe(
      catalogSubtitle(zhipuEntry!),
    )
  })

  it('zai: derived label/subtitle/logo match the values asserted in -onboarding.test.tsx', () => {
    // No component render — pins the derivation the onboarding test also
    // pins verbatim, so a helper change without a test update fails here.
    const entry = STUB_PROVIDERS.find((e) => e.id === 'zai')
    expect(entry).toBeDefined()
    expect(catalogLabel(entry!)).toBe('Z.AI (International)')
    expect(catalogSubtitle(entry!)).toBe('Pay-as-you-go, per token · api.z.ai/api/paas/v4')
    expect(catalogLogoSlug(entry!)).toBe('zhipu')
  })

  it('the catalog reaches the section through fetchProvidersCatalog, never a bundle', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([configured('openai', 'OpenAI', ['gpt-5'])])
    renderSection()
    await waitFor(() => screen.getByTestId('provider-row-openai'))
    expect(api.fetchProvidersCatalog).toHaveBeenCalled()
  })
})
