/*
 * Settings section: ADR-096's role/status/save guarantees, adapted to #1055's
 * card + Change design and #1056's removal of SearXNG. The retired one-name-
 * per-screen and radio assertions are replaced rather than kept alongside the
 * new cards: a selected name must now appear on its card AND service row.
 * Oracle: #1055 Approved design/Acceptance criteria, ADR-096 FR-028, D18,
 * FR-031, and the generated IntegrationProviderUpdateRequest contract.
 */

import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { IntegrationProvidersResponse } from '@/lib/api'

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
import type { AppState } from '@/lib/api'
import { IntegrationsSection } from './IntegrationsSection'

const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

function srow(overrides: Record<string, unknown> & { id: string }): Record<string, unknown> {
  return {
    kind: 'search',
    display_name: overrides.id.charAt(0).toUpperCase() + overrides.id.slice(1),
    configured: true, requires_key: true, active: false, usable: true,
    fallback: false, fallback_automatic: false,
    ...overrides,
  }
}

const RESPONSE: IntegrationProvidersResponse = {
  search: [
    srow({ id: 'brave', display_name: 'Brave Search', configured: false, usable: false }),
    srow({ id: 'tavily', search_depth_cap: 'basic' }),
    srow({ id: 'duckduckgo', display_name: 'DuckDuckGo', requires_key: false }),
    srow({ id: 'exa', display_name: 'Exa' }),
  ],
  voice: [
    srow({ id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs Scribe', requires_key: true, usable: undefined, fallback: undefined, fallback_automatic: undefined, active: true }),
  ],
  active_search: 'tavily', default_search: 'tavily', fallback_search: 'duckduckgo',
  native_search_in_effect: false,
} as unknown as IntegrationProvidersResponse

function renderSection() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
      <IntegrationsSection />
    </QueryClientProvider>,
  )
}

async function chooseSearchRole(role: 'default' | 'fallback', label: RegExp) {
  const card = await screen.findByTestId(`${role}-search-card`)
  fireEvent.click(within(card).getByRole('button', { name: 'Change' }))
  fireEvent.click(screen.getByRole('combobox', { name: role === 'default' ? 'Default search' : 'Fallback' }))
  fireEvent.click(screen.getByRole('option', { name: label }))
}

async function confirmGate() {
  fireEvent.click(await screen.findByRole('button', { name: 'Update integration' }))
}

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(RESPONSE as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

describe('IntegrationsSection — #1055 choice cards and ADR-096 provider status', () => {
  it('renders each retained provider exactly once in the service list and the selected choice separately on its card', async () => {
    renderSection()
    for (const [id, name] of [['brave', 'Brave Search'], ['tavily', 'Tavily'], ['duckduckgo', 'DuckDuckGo'], ['exa', 'Exa']]) {
      const row = await screen.findByTestId(`search-row-${id}`)
      expect(within(row).getAllByText(name)).toHaveLength(1)
    }
    expect(await screen.findByTestId('default-search-card')).toHaveTextContent('Tavily')
    expect(await screen.findByTestId('fallback-search-card')).toHaveTextContent('DuckDuckGo')
    expect(screen.queryAllByRole('radio')).toHaveLength(0)
  })

  it('shows automatic fallback on its card when the server resolved an automatic choice', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => p.id === 'duckduckgo' ? { ...p, fallback_automatic: true } : p),
    } as never)
    renderSection()
    expect(await screen.findByTestId('fallback-search-card')).toHaveTextContent(/DuckDuckGo.*automatic/i)
  })

  it('does not show the old Active role badge on search rows; voice keeps its Active badge', async () => {
    renderSection()
    await screen.findByTestId('search-row-tavily')
    expect(screen.queryByTestId('active-tavily')).not.toBeInTheDocument()
    expect(screen.queryByTestId('active-duckduckgo')).not.toBeInTheDocument()
    expect(await screen.findByTestId('active-elevenlabs')).toHaveTextContent('Active')
  })

  it('renders a configured but unusable search service as key not reaching search, never Ready', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => p.id === 'tavily' ? { ...p, usable: false } : p),
    } as never)
    renderSection()
    expect(await screen.findByTestId('key-not-reaching-tavily')).toHaveTextContent(/key not reaching search/i)
    expect(screen.queryByTestId('ready-tavily')).not.toBeInTheDocument()
    expect(await screen.findByTestId('default-search-card')).toHaveTextContent('Tavily')
  })

  it('marks an actually usable search provider Ready', async () => {
    renderSection()
    expect(await screen.findByTestId('ready-tavily')).toHaveTextContent('Ready')
  })
})

describe('IntegrationsSection — ADR-096 save rules through #1055 selectors', () => {
  it('stores a key without assigning either role (D18 separability)', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    fireEvent.click(await screen.findByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))
    await confirmGate()
    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'brave', { kind: 'search', api_key: 'BSA-secret' }, undefined,
      )
    })
  })

  it('choosing a usable new default confirms once then PUTs active:true for that id', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    await chooseSearchRole('default', /^Exa/i)
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
    await confirmGate()
    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'exa', { kind: 'search', active: true }, undefined,
      )
    })
  })

  it('choosing a different usable fallback confirms once then PUTs fallback:true', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    await chooseSearchRole('fallback', /^Exa/i)
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
    await confirmGate()
    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'exa', { kind: 'search', fallback: true }, undefined,
      )
    })
  })

  it('choosing None for the fallback PUTs fallback:false with no active field', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    await chooseSearchRole('fallback', /^None/i)
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
    await confirmGate()
    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.configureIntegrationProvider).mock.calls[0][1]).toStrictEqual({
        kind: 'search', fallback: false,
      })
    })
  })

  it('reselecting the already-stored default does not open the gate or PUT', async () => {
    renderSection()
    await chooseSearchRole('default', /^Tavily/i)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('pending GET renders no choice cards, rather than mistaking absent roles for None', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockReturnValue(new Promise(() => {}) as never)
    renderSection()
    expect(screen.queryByTestId('default-search-card')).not.toBeInTheDocument()
    expect(screen.queryByTestId('fallback-search-card')).not.toBeInTheDocument()
  })
})

describe('IntegrationsSection — failed save that persisted state', () => {
  it('refetches after a save-then-fail PUT so the newly saved key replaces stale readiness', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => p.id === 'brave' ? { ...p, configured: false, usable: false } : p),
    } as never)
    renderSection()
    expect(await screen.findByTestId('addkey-brave')).toHaveTextContent('Add key')

    // The REST boundary rejects AFTER persisting. The second GET reports the
    // stored key; a stale cache would still show "Add key".
    vi.mocked(api.configureIntegrationProvider).mockRejectedValueOnce(
      new Error('Brave Search integration saved but config reload failed: reload timed out'),
    )
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => p.id === 'brave' ? { ...p, configured: true, usable: true } : p),
    } as never)
    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))
    await confirmGate()
    await waitFor(() => expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' })))
    await waitFor(() => expect(screen.getByTestId('addkey-brave')).toHaveTextContent('Edit key'))
    expect(vi.mocked(api.fetchIntegrationProviders).mock.calls.length).toBeGreaterThanOrEqual(2)
  })
})
