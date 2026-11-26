// Inputs for the Web & Search side panel tests (SkillsToolsPage.tsx#overlay:1): the
// search services the panel lists, and the app state that decides how a change is
// confirmed. Fix round 4, Part G.
//
// This file is a helper, not a test file: it holds no test and no expectation about the
// panel. Every oracle sits in the test file that uses it. The shapes follow the generated
// IntegrationProvidersResponse of the contract; the values are chosen here.
import type { AppState, IntegrationProvidersResponse } from '@/lib/api'

// A hosted or desktop install confirms a sensitive change with a plain dialog (button
// "Update integration") instead of a password prompt, so a test needs no password.
export const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

type Row = Record<string, unknown> & { id: string }

// One search service as the server lists it.
export function searchService(overrides: Row): Row {
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

// The four services every panel test starts from: Brave has no key, Tavily is the default,
// DuckDuckGo (no key needed) is the fallback, Exa is usable and not chosen.
export function searchServices(): Row[] {
  return [
    searchService({ id: 'brave', display_name: 'Brave Search', configured: false, usable: false }),
    searchService({ id: 'tavily', search_depth_cap: 'basic' }),
    searchService({ id: 'duckduckgo', display_name: 'DuckDuckGo', requires_key: false }),
    searchService({ id: 'exa', display_name: 'Exa' }),
  ]
}

export function searchProviders(search: Row[] = searchServices()): IntegrationProvidersResponse {
  return {
    search,
    voice: [],
    active_search: 'tavily',
    default_search: 'tavily',
    fallback_search: 'duckduckgo',
    native_search_in_effect: false,
  } as unknown as IntegrationProvidersResponse
}
