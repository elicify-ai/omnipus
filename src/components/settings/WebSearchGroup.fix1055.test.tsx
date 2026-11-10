/*
 * RED pack for the #1055/#1056 fix-red lane (founder decision 2026-09-29,
 * "Key save switches on"). This is a SIBLING file to WebSearchGroup.test.tsx
 * on purpose: that file's existing test
 * ("says list order does not determine which provider is tried first",
 * describe block "retained ADR-096 notices and #1056 removal") asserts the
 * technical intro sentence this decision requires REMOVED IS PRESENT — the
 * opposite of T2 below. Per elicify-test-writing / the RED integrity rules,
 * an existing test that contradicts a new founder decision is never
 * weakened or deleted by the RED author; it is reported instead (see the
 * qa-lead report for this dispatch). T2 lives here, asserting the decision's
 * side of the contradiction, so both tests stay intact and visible until
 * team-lead/the founder resolves which one is stale.
 *
 * Oracle:
 *   T2 — issue #1055 "problem 7": the sentence "The order of this list does
 *        not choose who is tried first." is the retired technical framing;
 *        it must not render anywhere in the web-search section.
 *   T3 — #1055 heuristic H4 ("same component = same behavior", one status
 *        vocabulary): a provider whose key is saved (configured: true) but
 *        which the tool's usability test still reports unusable
 *        (usable: false) must show the SAME status wording in the Default/
 *        Fallback selector as its service-row badge. IntegrationsSection.tsx
 *        (renderSearchRow) gives that exact wording for
 *        configured && requires_key && usable===false:
 *        `<Badge data-testid="key-not-reaching-{id}" variant="warning">Key
 *        not reaching search</Badge>` — never "Needs configuration". The
 *        no-key case ("Add a key first") is unchanged by this decision.
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

// Same base fixture WebSearchGroup.test.tsx uses, so T2/T3 exercise the real
// component tree under the same shape the sibling suite already covers.
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

describe('#1055 fix-red — T2: retired technical intro sentence is gone', () => {
  it('never renders "The order of this list does not choose who is tried first."', async () => {
    renderSection()
    await screen.findByTestId('search-row-tavily')
    expect(
      screen.queryByText(/The order of this list does not choose who is tried first\.?/),
    ).not.toBeInTheDocument()
  })
})

describe('#1055 fix-red — T3: one status vocabulary between row badge and selector', () => {
  // Brave configured (a key was saved) but still unusable — the exact state
  // IntegrationsSection's badge logic reports as "Key not reaching search"
  // (configured && requires_key && usable === false), distinct from the
  // no-key case ("Add a key first").
  const keyNotReachingOverride = {
    search: response.search.map((p) =>
      p.id === 'brave' ? { ...p, configured: true, usable: false } : p,
    ),
  }

  it('shows "Key not reaching search" — not "Needs configuration" — in the Default selector for a configured-but-unusable provider', async () => {
    renderSection(keyNotReachingOverride)
    await openSelector('default')
    const brave = screen.getByRole('option', { name: /Brave Search/i })
    expect(brave).toHaveAttribute('aria-disabled', 'true')
    expect(brave).toHaveTextContent(/Key not reaching search/i)
    expect(brave).not.toHaveTextContent(/Needs configuration/i)
  })

  it('shows "Key not reaching search" — not "Needs configuration" — in the Fallback selector for a configured-but-unusable provider', async () => {
    renderSection(keyNotReachingOverride)
    await openSelector('fallback')
    const brave = screen.getByRole('option', { name: /Brave Search/i })
    expect(brave).toHaveAttribute('aria-disabled', 'true')
    expect(brave).toHaveTextContent(/Key not reaching search/i)
    expect(brave).not.toHaveTextContent(/Needs configuration/i)
  })

  it('keeps "Add a key first" for a provider with no key at all (unchanged by this decision)', async () => {
    // response's own perplexity row: requires_key true, configured false.
    renderSection()
    await openSelector('default')
    const perplexity = screen.getByRole('option', { name: /Perplexity/i })
    expect(perplexity).toHaveTextContent(/Add a key first/i)
    expect(perplexity).not.toHaveTextContent(/Key not reaching search/i)
  })
})
