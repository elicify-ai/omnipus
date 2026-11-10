/// <reference types="vite/client" />

// This fixture runs in Vite/jsdom and imports the real browser API. Load its
// actual ambient declarations in the otherwise Node-only tests project too.
// Network-edge fixture for #1104/#1105. Components, real API adapters, generated
// response validation, step-up dialogs and the toast store are NOT mocked.
// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md.

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { vi } from 'vitest'
import type { AppState, IntegrationProvidersResponse } from '@/lib/api'
import { IntegrationsSection } from '@/components/settings/IntegrationsSection'
import { useUiStore } from '@/store/ui'

export function savedSearchCatalogue(): IntegrationProvidersResponse {
  return {
    search: [
      { id: 'brave', kind: 'search', display_name: 'Brave Search', requires_key: true, configured: false, usable: false },
      { id: 'tavily', kind: 'search', display_name: 'Tavily', requires_key: true, configured: true, usable: true, active: true, search_depth_cap: 'basic' },
      { id: 'perplexity', kind: 'search', display_name: 'Perplexity', requires_key: true, configured: false, usable: false },
      { id: 'duckduckgo', kind: 'search', display_name: 'DuckDuckGo', requires_key: false, configured: true, usable: true, fallback: true },
      { id: 'glm', kind: 'search', display_name: 'GLM Search', requires_key: true, configured: false, usable: false },
      { id: 'baidu', kind: 'search', display_name: 'Baidu Search', requires_key: true, configured: false, usable: false },
      { id: 'exa', kind: 'search', display_name: 'Exa', requires_key: true, configured: true, usable: true },
    ],
    voice: [],
    default_search: 'tavily',
    fallback_search: 'duckduckgo',
    active_search: 'tavily',
    native_search_in_effect: false,
  }
}

export function keyRemovedCatalogue(previous = savedSearchCatalogue()): IntegrationProvidersResponse {
  return {
    ...previous,
    search: previous.search.map((row) => row.id === 'tavily' ? { ...row, configured: false, usable: false, active: false } : row),
  }
}

export function jsonReply(body: unknown, status = 200, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

export function heldReply() {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((accept) => { resolve = accept })
  return { promise, resolve }
}

export function searchSettingsHarness(mode: AppState['identity']['mode'] = 'platform', initial = savedSearchCatalogue()) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  let catalogue = structuredClone(initial)
  const state: AppState = {
    onboarding_complete: true,
    identity: { mode, edition: mode === 'local' ? 'core' : 'hosted', signed_in: true },
  }
  // Seed a complete generated AppState to avoid an unrelated startup fetch;
  // useStepUp still reads its real shared query and makes the mode decision.
  client.setQueryData(['app-state'], state)
  useUiStore.setState({ toasts: [] })
  document.cookie = 'csrf=search-settings-test-csrf; Path=/'

  const harness = {
    client,
    replaceCatalogue(next: IntegrationProvidersResponse) { catalogue = structuredClone(next) },
    removeReply: (): Response | Promise<Response> => {
      catalogue = keyRemovedCatalogue(catalogue)
      return jsonReply(catalogue)
    },
    checkReply: (id: string): Response | Promise<Response> => jsonReply({ provider_id: id, status: 'success', checked_at: '2026-09-30T12:00:00Z' }),
    saveReply: (): Response | Promise<Response> => jsonReply(catalogue),
    // Until the new schema exists these diagnostic responses are raw HTTP
    // JSON fixtures, not hand-written wire types or casts to absent types.
    fetch: vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const path = new URL(String(input), 'http://localhost').pathname
      const method = init?.method ?? 'GET'
      if (path === '/api/v1/integrations/providers' && method === 'GET') return jsonReply(catalogue)
      if (path === '/api/v1/state' && method === 'GET') return jsonReply(state)
      if (path === '/api/v1/auth/reauth' && method === 'POST') {
        return jsonReply({ verified: true, token: 'fresh-single-use-consent', expires_in: 300 })
      }
      const check = /^\/api\/v1\/integrations\/providers\/([^/]+)\/check$/.exec(path)
      if (check && method === 'POST') return harness.checkReply(decodeURIComponent(check[1]))
      if (/^\/api\/v1\/integrations\/providers\/[^/]+$/.test(path) && method === 'PUT') {
        const body: unknown = JSON.parse(String(init?.body))
        if (typeof body === 'object' && body !== null && 'clear_api_key' in body && body.clear_api_key === true) {
          return harness.removeReply()
        }
        return harness.saveReply()
      }
      throw new Error(`Unexpected network-edge request: ${method} ${path}`)
    }),
    calls(method: string, path: string) {
      return harness.fetch.mock.calls.filter(([input, init]) =>
        (init?.method ?? 'GET') === method && new URL(String(input), 'http://localhost').pathname === path,
      )
    },
    render() {
      return render(<QueryClientProvider client={client}><IntegrationsSection /></QueryClientProvider>)
    },
  }
  vi.stubGlobal('fetch', harness.fetch)
  return harness
}
