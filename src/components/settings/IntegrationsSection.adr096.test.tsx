/**
 * IntegrationsSection.adr096.test.tsx — ADR-096 Settings screen, section level.
 *
 * Verifies, through the real IntegrationsSection, the screen the ADR-096 spec
 * draws (§ "Settings screen"): ONE row per provider (the provider name renders
 * exactly once — the old screen repeated every name across two radio stacks),
 * the default + fallback radios live inside the provider's row, the default
 * row's fallback radio is disabled with its reason visible, "No fallback" is a
 * visible distinct choice, search rows wear Default / Fallback badges instead
 * of the retired "Active" badge (FR-028 / settings table "Badge"), a
 * configured-but-unusable provider reads "key not reaching search" (FR-028),
 * storing a key on a search row is separable from assigning a role (ADR-096
 * D18 — the PUT carries api_key and no role field), and each radio selection
 * maps to exactly one contract-shaped PUT (IntegrationProviderUpdateRequest).
 * Step-up gate and data loading are the existing suites' subject; mocked at
 * the process edge only (REST client).
 *
 * Expected values derive from the spec's Settings-screen table and save-rule
 * table and the generated contract, not from observed output.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { IntegrationProvidersResponse } from '@/lib/api'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

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
    configured: true,
    requires_key: true,
    active: false,
    usable: true,
    fallback: false,
    fallback_automatic: false,
    ...overrides,
  }
}

const RESPONSE: IntegrationProvidersResponse = {
  search: [
    srow({ id: 'brave' }),
    srow({ id: 'tavily' }),
    // The helper capitalises only the first letter ("Duckduckgo", "Searxng").
    // The assertion looks up the catalogue's real display names, which this
    // fixture has to supply — the screen renders display_name, it does not
    // invent the spelling.
    srow({ id: 'duckduckgo', display_name: 'DuckDuckGo', requires_key: false }),
    srow({ id: 'searxng', display_name: 'SearXNG', requires_key: false, configured: false, usable: false }),
  ],
  voice: [
    srow({ id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs Scribe', requires_key: true, usable: undefined, fallback: undefined, fallback_automatic: undefined, active: true }),
  ],
  active_search: 'tavily',
  default_search: 'tavily',
  fallback_search: 'duckduckgo',
  native_search_in_effect: false,
} as unknown as IntegrationProvidersResponse

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <IntegrationsSection />
    </QueryClientProvider>,
  )
}

async function confirmGate() {
  fireEvent.click(await screen.findByRole('button', { name: 'Update integration' }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(RESPONSE as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

describe('IntegrationsSection — ADR-096 one row per provider', () => {
  it('renders each search provider name exactly once — the row is the only place the name appears', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('search-row-brave')).toBeInTheDocument()
    })
    for (const name of ['Brave', 'Tavily', 'DuckDuckGo', 'SearXNG']) {
      expect(screen.getAllByText(name)).toHaveLength(1)
    }
  })

  it('puts both role radios inside the provider row, and the "No fallback" choice after the rows', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('default-radio-brave')).toBeInTheDocument()
      expect(screen.getByTestId('fallback-radio-brave')).toBeInTheDocument()
    })
    const row = screen.getByTestId('search-row-brave')
    expect(row.contains(screen.getByTestId('default-radio-brave'))).toBe(true)
    expect(row.contains(screen.getByTestId('fallback-radio-brave'))).toBe(true)
    // The "No fallback" choice lives outside every provider row.
    expect(screen.getByTestId('search-row-brave').contains(screen.getByTestId('no-fallback-choice'))).toBe(false)
    expect(screen.getByTestId('no-fallback-choice')).toHaveTextContent('No fallback')
  })

  it('disables the fallback radio on the default row and shows its reason inside that row', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('fallback-radio-tavily')).toBeDisabled()
    })
    expect(screen.getByTestId('fallback-disabled-reason-tavily')).toHaveTextContent(
      'A provider cannot fall back to itself.',
    )
    expect(screen.getByTestId('search-row-tavily').contains(screen.getByTestId('fallback-disabled-reason-tavily'))).toBe(true)
    expect(screen.getByTestId('fallback-radio-brave')).toBeEnabled()
  })

  it('checks the radios the resolved roles name — and nothing else', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('default-radio-tavily')).toHaveAttribute('data-state', 'checked')
      expect(screen.getByTestId('fallback-radio-duckduckgo')).toHaveAttribute('data-state', 'checked')
    })
    expect(screen.getByTestId('default-radio-brave')).toHaveAttribute('data-state', 'unchecked')
    expect(screen.getByTestId('fallback-radio-brave')).toHaveAttribute('data-state', 'unchecked')
    // SearXNG is offered as no new choice (D10).
    expect(screen.queryByTestId('default-radio-searxng')).not.toBeInTheDocument()
    expect(screen.queryByTestId('fallback-radio-searxng')).not.toBeInTheDocument()
  })
})

describe('IntegrationsSection — ADR-096 badges and readiness', () => {
  it('shows Default and Fallback badges on search rows from the role fields, and no Active badge', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('badge-default-tavily')).toBeInTheDocument()
      expect(screen.getByTestId('badge-fallback-duckduckgo')).toBeInTheDocument()
    })
    // "Active" goes away for search rows — the retired badge, not the retired
    // testid only. The fixture's search rows carry active:false anyway; the
    // decisive assertion is that the default row is marked Default, not Active.
    expect(screen.queryByTestId('active-tavily')).not.toBeInTheDocument()
    expect(screen.queryByTestId('active-duckduckgo')).not.toBeInTheDocument()
  })

  it('labels the R3 automatic fallback on the row and on its badge', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => (p.id === 'duckduckgo' ? { ...p, fallback_automatic: true } : p)),
    } as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('automatic-fallback-duckduckgo')).toBeInTheDocument()
      expect(screen.getByTestId('badge-fallback-duckduckgo')).toHaveTextContent('Fallback (automatic)')
    })
  })

  it('keeps the Active badge and behaviour on voice rows untouched', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('active-elevenlabs')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('badge-default-elevenlabs')).not.toBeInTheDocument()
  })

  it('renders a configured but unusable provider as "key not reaching search", never as ready', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => (p.id === 'tavily' ? { ...p, usable: false } : p)),
    } as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('key-not-reaching-tavily')).toBeInTheDocument()
    })
    expect(screen.getByTestId('key-not-reaching-tavily')).toHaveTextContent(/key not reaching search/i)
    expect(screen.queryByTestId('ready-tavily')).not.toBeInTheDocument()
  })

  it('marks a usable search provider as ready', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('ready-brave')).toBeInTheDocument()
    })
  })

  it('still marks the default row Default when its key stops resolving — the role is stored state, the readiness badge is honest', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => (p.id === 'tavily' ? { ...p, usable: false } : p)),
    } as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('badge-default-tavily')).toBeInTheDocument()
      expect(screen.getByTestId('key-not-reaching-tavily')).toBeInTheDocument()
    })
  })
})

describe('IntegrationsSection — ADR-096 save rules', () => {
  it('stores a key on a search row without assigning any role (D18 separability)', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    await waitFor(() => screen.getByTestId('addkey-brave'))

    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))
    await confirmGate()

    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'brave',
        { kind: 'search', api_key: 'BSA-secret' },
        undefined,
      )
    })
  })

  it('selecting a default radio PUTs active:true for that id, through the gate', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    await waitFor(() => screen.getByTestId('default-radio-brave'))

    fireEvent.click(screen.getByTestId('default-radio-brave'))
    await confirmGate()

    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'brave',
        { kind: 'search', active: true },
        undefined,
      )
    })
  })

  it('selecting a fallback radio PUTs fallback:true for that id', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    await waitFor(() => screen.getByTestId('fallback-radio-brave'))

    fireEvent.click(screen.getByTestId('fallback-radio-brave'))
    await confirmGate()

    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'brave',
        { kind: 'search', fallback: true },
        undefined,
      )
    })
  })

  it('choosing "No fallback" PUTs fallback:false with no active field', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(RESPONSE as never)
    renderSection()
    await waitFor(() => screen.getByTestId('fallback-radio-none'))

    fireEvent.click(screen.getByTestId('fallback-radio-none'))
    await confirmGate()

    await waitFor(() => {
      expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1)
      // The contract sets the fallback to none "regardless of the addressed
      // id", so the body — not the path id — is what carries the meaning.
      expect(vi.mocked(api.configureIntegrationProvider).mock.calls[0][1]).toStrictEqual({
        kind: 'search',
        fallback: false,
      })
    })
  })

  it('clicking the already-selected default fires no gated save', async () => {
    renderSection()
    await waitFor(() => screen.getByTestId('default-radio-tavily'))

    fireEvent.click(screen.getByTestId('default-radio-tavily'))
    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    })
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('the not-yet-loaded state (query pending) renders no role radios — distinct from an explicit none', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockReturnValue(new Promise(() => {}) as never)
    renderSection()
    // Skeleton state: the section has no data yet, so no radio exists.
    await waitFor(() => {
      expect(screen.queryByTestId('default-radio-brave')).not.toBeInTheDocument()
      expect(screen.queryByTestId('fallback-radio-none')).not.toBeInTheDocument()
    })
  })
})

describe('IntegrationsSection — failed save that persisted state', () => {
  // The reload-failure PUT is not a clean failure: the backend stores the
  // credential and writes config.json BEFORE the reload (or the post-reload
  // usability judgment) fails — rest_integrations_roles.go::
  // handleIntegrationProviderUpdate returns 500 "saved but config reload
  // failed" AFTER the store+persist, and the post-reload 400 keeps "the
  // persisted write". The list must refetch so the row reflects what the
  // backend actually holds, not the pre-save cache.
  it('refetches the list after a save-then-fail PUT — the reload-failure response names a save that DID persist', async () => {
    // Initial load: brave NOT configured — the row honestly shows "Add key".
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      ...RESPONSE,
      search: RESPONSE.search.map((p) => (p.id === 'brave' ? { ...p, configured: false, usable: false } : p)),
    } as never)
    renderSection()
    await waitFor(() => screen.getByTestId('addkey-brave'))
    expect(screen.getByTestId('addkey-brave')).toHaveTextContent('Add key')

    // The PUT fails AFTER persisting: key stored, config.json written, then
    // the reload fails. The refetch the fix must trigger returns the
    // persisted truth: the key is in the vault, so brave reads configured.
    vi.mocked(api.configureIntegrationProvider).mockRejectedValueOnce(
      new Error('Brave Search integration saved but config reload failed: reload timed out'),
    )
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce(RESPONSE as never)

    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))
    await confirmGate()

    // Existing behaviour kept: the failure surfaces as an error toast.
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
    })
    // The fix under test: the stale cache is dropped and the refetched list
    // renders — brave now shows "Edit key" (the stored key resolved).
    await waitFor(() => {
      expect(screen.getByTestId('addkey-brave')).toHaveTextContent('Edit key')
    })
    expect(vi.mocked(api.fetchIntegrationProviders).mock.calls.length).toBeGreaterThanOrEqual(2)
  })
})
