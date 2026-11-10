/**
 * #1102: a failed integration save must expose the server's complete reason.
 * Oracle: https://github.com/elicify-ai/omnipus/issues/1102 and the RED brief.
 *
 * Only the fetch/network edge is replaced. The real request client rejects
 * the save via ApiError.fromResponse on a real 500/503 Response; the section,
 * confirmation gate, mutation, error formatter and toast store all stay real.
 * A plain Error fixture would bypass the known-5xx message override and miss
 * this regression. Happy-save/password-gate cases remain in the existing
 * section tests. GREEN and mutation verification are deferred to CHECK.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import type { AppState, IntegrationProvidersResponse } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { IntegrationsSection } from './IntegrationsSection'

const APP_STATE: AppState = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
}

const CATALOGUE: IntegrationProvidersResponse = {
  search: [
    {
      id: 'tavily', kind: 'search', display_name: 'Tavily',
      configured: false, requires_key: true, active: false, usable: false,
    },
    {
      id: 'duckduckgo', kind: 'search', display_name: 'DuckDuckGo',
      configured: true, requires_key: false, active: true, usable: true,
    },
  ],
  voice: [],
  active_search: 'duckduckgo',
  default_search: 'duckduckgo',
  fallback_search: null,
}

const clients: QueryClient[] = []

function clearToasts() {
  for (const toast of useUiStore.getState().toasts) {
    // Use the real removal action so the store's dismissal timers are cleared.
    useUiStore.getState().removeToast(toast.id)
  }
}

beforeEach(() => {
  clearToasts()
  // This is the browser's existing CSRF cookie, not an error-handling seam.
  document.cookie = 'csrf=integration-save-test; Path=/'
})

afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients.length = 0
  clearToasts()
  document.cookie = 'csrf=; Path=/; Max-Age=0'
  vi.unstubAllGlobals()
})

describe('IntegrationsSection — #1102 server reasons on failed saves', () => {
  it.each([
    {
      status: 500,
      reason: 'Tavily integration saved but config reload failed: reload timed out',
    },
    {
      status: 503,
      reason: 'credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets',
    },
  ])('shows the server reason from the real $status ApiError', async ({ status, reason }) => {
    const errorBody = JSON.stringify({ error: reason })
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (input, init) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
      const path = new URL(url, 'http://localhost').pathname
      const method = init?.method ?? 'GET'
      const headers = { 'Content-Type': 'application/json' }
      if (method === 'GET' && path === '/api/v1/state') {
        return new Response(JSON.stringify(APP_STATE), { status: 200, headers })
      }
      if (method === 'GET' && path === '/api/v1/integrations/providers') {
        return new Response(JSON.stringify(CATALOGUE), { status: 200, headers })
      }
      if (method === 'PUT' && path === '/api/v1/integrations/providers/tavily') {
        // request() calls the unmodified ApiError.fromResponse on this body.
        return new Response(errorBody, { status, headers })
      }
      throw new Error(`Unexpected network request: ${method} ${path}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false, gcTime: Infinity },
        mutations: { retry: false, gcTime: Infinity },
      },
    })
    clients.push(client)
    render(<QueryClientProvider client={client}><IntegrationsSection /></QueryClientProvider>)
    await waitFor(() => expect(client.getQueryData(['app-state'])).toEqual(APP_STATE))
    fireEvent.click(await screen.findByTestId('addkey-tavily'))
    fireEvent.change(screen.getByTestId('key-input-tavily'), { target: { value: 'test-tavily-key' } })
    fireEvent.click(screen.getByTestId('save-tavily'))
    fireEvent.click(await screen.findByRole('button', { name: 'Update integration' }))

    await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(1))
    const mutations = client.getMutationCache().getAll()
    expect(mutations).toHaveLength(1)
    expect(mutations[0].state.status).toBe('error')
    expect(mutations[0].state.error).toBeInstanceOf(ApiError)
    expect(mutations[0].state.error).toMatchObject({ status, body: errorBody })
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/integrations/providers/tavily'),
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ kind: 'search', api_key: 'test-tavily-key' }) }),
    )

    const [toast] = useUiStore.getState().toasts
    expect(toast.variant).toBe('error')
    expect(toast.message, `#1102: the ${status} save must retain the server's actionable reason`).toContain(reason)
    expect(toast.message).not.toContain('The server is unavailable. Please try again in a moment.')
  })
})
