/*
 * #1055 Settings RED pack, replacing ADR-096's retired radio assertions.
 * Oracle: issue #1055 Approved design / Acceptance criteria, ADR-096 FR-031
 * and R5 for the retained notices, and #1056 F-2 for SearXNG removal.
 * The real IntegrationsSection and catalogued controls render; only the REST
 * boundary and toast store are mocked. GREEN may change component composition.
 */

import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, IntegrationProvidersResponse } from '@/lib/api'

const addToast = vi.fn()
vi.mock('@/store/ui', () => ({ useUiStore: vi.fn(() => ({ addToast })) }))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchIntegrationProviders: vi.fn(),
    configureIntegrationProvider: vi.fn(),
    fetchAppState: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { IntegrationsSection } from './IntegrationsSection'

const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

const response = {
  search: [
    { id: 'brave', kind: 'search', display_name: 'Brave Search', requires_key: true, configured: false, usable: false },
    { id: 'tavily', kind: 'search', display_name: 'Tavily', requires_key: true, configured: true, usable: true, search_depth_cap: 'basic' },
    { id: 'perplexity', kind: 'search', display_name: 'Perplexity', requires_key: true, configured: false, usable: false },
    { id: 'duckduckgo', kind: 'search', display_name: 'DuckDuckGo', requires_key: false, configured: true, usable: true },
    { id: 'exa', kind: 'search', display_name: 'Exa', requires_key: true, configured: true, usable: true },
  ],
  voice: [
    { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs Scribe', requires_key: true, configured: true, active: true },
  ],
  default_search: 'tavily',
  fallback_search: null,
  native_search_in_effect: false,
} as IntegrationProvidersResponse

function renderSection(overrides: Partial<IntegrationProvidersResponse> = {}) {
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue({ ...response, ...overrides })
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
      <IntegrationsSection />
    </QueryClientProvider>,
  )
}

async function card(role: 'default' | 'fallback') {
  return screen.findByTestId(`${role}-search-card`)
}

async function openSelector(role: 'default' | 'fallback') {
  fireEvent.click(within(await card(role)).getByRole('button', { name: 'Change' }))
  const selector = screen.getByRole('combobox', { name: role === 'default' ? 'Default search' : 'Fallback' })
  fireEvent.click(selector)
  return selector
}

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

describe('Settings web search — #1055 card and Change selectors', () => {
  it('shows two named current-choice cards, plain-language help, and no role radios or regional-block warning', async () => {
    renderSection()
    const defaultCard = await card('default')
    const fallbackCard = await card('fallback')
    expect(defaultCard).toHaveTextContent('Tavily')
    expect(fallbackCard).toHaveTextContent(/None/)
    expect(within(defaultCard).getByRole('button', { name: 'Change' })).toBeEnabled()
    expect(within(fallbackCard).getByRole('button', { name: 'Change' })).toBeEnabled()
    expect(screen.getByText('Your agents search with the default service. If it fails, the fallback takes over.')).toBeInTheDocument()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)
    expect(screen.queryByText(/North Korea|Indonesia|mainland China/i)).not.toBeInTheDocument()
  })

  it('shows unusable choices disabled with a reason; clicking one cannot start a gated save', async () => {
    renderSection()
    await openSelector('default')
    const brave = screen.getByRole('option', { name: /Brave Search/i })
    expect(brave).toHaveAttribute('aria-disabled', 'true')
    expect(brave).toHaveTextContent(/Add a key first/i)
    fireEvent.click(brave)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('fallback offers None and every usable service except the current default', async () => {
    renderSection()
    await openSelector('fallback')
    expect(screen.getByRole('option', { name: /None/i })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /DuckDuckGo/i })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Exa/i })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /^Tavily/i })).not.toBeInTheDocument()
    const brave = screen.getByRole('option', { name: /Brave Search/i })
    expect(brave).toHaveAttribute('aria-disabled', 'true')
    expect(brave).toHaveTextContent(/Add a key first/i)
  })

  it('warns on an already-selected default whose key is missing and Fix opens that key editor', async () => {
    renderSection({
      search: response.search.map((p) => p.id === 'tavily' ? { ...p, configured: false, usable: false } : p),
    })
    const current = await card('default')
    expect(current).toHaveTextContent(/Tavily.*key missing.*searches will fail/i)
    fireEvent.click(within(current).getByRole('button', { name: 'Fix' }))
    expect(screen.getByTestId('key-input-tavily')).toBeInTheDocument()
  })

  it('warns on an unusable stored fallback while retaining its identity and Fix action', async () => {
    renderSection({ fallback_search: 'brave' })
    const current = await card('fallback')
    expect(current).toHaveTextContent(/Brave Search.*key missing/i)
    fireEvent.click(within(current).getByRole('button', { name: 'Fix' }))
    expect(screen.getByTestId('key-input-brave')).toBeInTheDocument()
  })

  it('suggests usable DuckDuckGo when fallback is None, but not if DuckDuckGo itself is unusable', async () => {
    const view = renderSection()
    expect(await card('fallback')).toHaveTextContent(/DuckDuckGo works without a key/i)
    view.unmount()
    renderSection({ search: response.search.map((p) => p.id === 'duckduckgo' ? { ...p, usable: false } : p) })
    expect(await card('fallback')).not.toHaveTextContent(/DuckDuckGo works without a key/i)
  })

  it('shows the configured Tavily depth cap on the default card without an editing control', async () => {
    renderSection()
    const current = await card('default')
    expect(current).toHaveTextContent(/Depth cap: basic/i)
    expect(within(current).queryByRole('combobox', { name: /depth/i })).not.toBeInTheDocument()
    expect(within(current).queryByRole('spinbutton', { name: /depth/i })).not.toBeInTheDocument()
  })

  it('orders ready search services before those needing setup, with quiet key actions', async () => {
    const { container } = renderSection()
    await screen.findByTestId('search-row-tavily')
    const ids = [...container.querySelectorAll('[data-testid^="search-row-"]')]
      .map((node) => node.getAttribute('data-testid'))
    expect(ids).toEqual([
      'search-row-tavily', 'search-row-duckduckgo', 'search-row-exa',
      'search-row-brave', 'search-row-perplexity',
    ])
    for (const id of ['brave', 'tavily', 'perplexity', 'exa']) {
      expect(screen.getByTestId(`addkey-${id}`).className).not.toContain('bg-[var(--color-accent)]')
    }
  })
})

describe('Settings web search — retained ADR-096 notices and #1056 removal', () => {
  // #1055 problem 7 (team-lead ruling, 2026-09-29): the technical,
  // negatively-framed sentence "The order of this list does not choose who
  // is tried first." is retired — the founder-approved design replaces it
  // with the plain-language intro. This test's assertion flips from
  // requiring the retired sentence to requiring its replacement, so
  // coverage of this line of the spec does not shrink; the retired
  // sentence's absence is pinned separately, in the sibling RED file
  // WebSearchGroup.fix1055.test.tsx (T2).
  it('shows the approved plain-language intro that replaces the retired "list order" sentence (#1055 problem 7: retired sentence replaced by approved intro)', async () => {
    renderSection()
    await screen.findByTestId('search-row-tavily')
    expect(
      screen.getByText('Your agents search with the default service. If it fails, the fallback takes over.'),
    ).toBeInTheDocument()
  })

  it('distinguishes undecided roles from an explicit None fallback', async () => {
    renderSection({ default_search: undefined, fallback_search: undefined })
    expect(await screen.findByTestId('roles-undecided')).toBeInTheDocument()
  })

  it('states native search overrides role choice without naming any provider as its answerer', async () => {
    renderSection({ native_search_in_effect: true })
    const notice = await screen.findByTestId('native-search-notice')
    expect(notice).toHaveTextContent(/native model search/i)
    expect(notice).toHaveTextContent(/not currently deciding who answers/i)
    for (const name of ['Brave', 'Tavily', 'DuckDuckGo']) expect(notice).not.toHaveTextContent(name)
  })

  it('explains an ignored stored self-fallback but never offers it as a new choice', async () => {
    renderSection({ fallback_ignored_reason: 'same_as_default' })
    expect(await screen.findByTestId('fallback-ignored-notice')).toHaveTextContent(/ignored/i)
    await openSelector('fallback')
    expect(screen.queryByRole('option', { name: /^Tavily/i })).not.toBeInTheDocument()
  })

  it('explains why an already-stored self-fallback is ignored', async () => {
    renderSection({ fallback_ignored_reason: 'same_as_default' })
    expect(await screen.findByTestId('fallback-ignored-notice')).toHaveTextContent(/cannot fall back to itself/i)
  })

  it('does not call a loaded default undecided or show the native-search notice when inactive', async () => {
    renderSection({ native_search_in_effect: false })
    await screen.findByTestId('search-row-tavily')
    expect(screen.queryByTestId('roles-undecided')).not.toBeInTheDocument()
    expect(screen.queryByTestId('native-search-notice')).not.toBeInTheDocument()
  })

  it('does not claim an ignored fallback when the server reports no ignored reason', async () => {
    renderSection()
    await screen.findByTestId('search-row-tavily')
    expect(screen.queryByTestId('fallback-ignored-notice')).not.toBeInTheDocument()
  })
})
