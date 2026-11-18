// Independent CHECK F5: pin the RECEIVING query value with the real route,
// its real search schema, SettingsScreen tabs, and ThirdPartyAgentsSection.
// No mocked route/search parser or manually supplied initialTab. API calls and
// unrelated chrome/provider content are the only replaced boundaries.
import { act, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ThirdPartyAgentSettingsView } from '@/lib/api/generated/openapi-types'

vi.mock('@/components/layout/AppShell', () => ({ AppShell: () => <Outlet /> }))
vi.mock('@/components/settings/ProvidersSection', () => ({ ProvidersSection: () => null }))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchAboutInfo: vi.fn(async () => ({ version: 'test', go_version: 'test', os: 'linux', arch: 'amd64', uptime_seconds: 0 })),
    getThirdPartyAgentSettings: vi.fn(async (): Promise<ThirdPartyAgentSettingsView> => ({
      clis: {
        'claude-code': { path: '', preset_args: '', detected: { installed: false, path: null, link_path: null, source: null } },
        codex: { path: '/opt/t11b/codex', preset_args: '--model saved-model', detected: { installed: false, path: null, link_path: null, source: null } },
        opencode: { path: '', preset_args: '', detected: { installed: false, path: null, link_path: null, source: null } },
      },
    })),
  }
})

import { routeTree } from '@/routeTree.gen'
import { resetTokenValidationCache } from '@/routes/_app'

const clients: QueryClient[] = []
beforeEach(() => {
  // jsdom has no scroll viewport. Replace that browser edge only; this row
  // checks routing/tab activation, not scroll positioning or browser rendering.
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  localStorage.setItem('omnipus_auth_username', 't11b-person')
  resetTokenValidationCache()
})
afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  localStorage.removeItem('omnipus_auth_username')
  resetTokenValidationCache()
  vi.restoreAllMocks()
})

function openSettings(url: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [url] }) })
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  return router
}

async function expectThirdPartyTab() {
  await waitFor(() => expect(screen.getByTestId('settings-tab-third-party-agents')).toHaveAttribute('data-state', 'active'))
  expect(await screen.findByRole('textbox', { name: 'Codex program path' })).toHaveValue('/opt/t11b/codex')
  expect(screen.getByRole('textbox', { name: 'Codex parameters' })).toHaveValue('--model saved-model')
  expect(screen.getByTestId('third-party-agents-section')).toBeVisible()
}

describe('T11-B receiving Settings link (CHECK F5)', () => {
  it('cold /settings?tab=third-party-agents opens the actual section, not Providers', async () => {
    const router = openSettings('/settings?tab=third-party-agents')
    await expectThirdPartyTab()
    expect(router.state.location.pathname).toBe('/settings')
    expect(router.state.location.search).toEqual({ tab: 'third-party-agents' })
  })

  it('a search-only navigation switches the mounted Settings screen to Third-party agents', async () => {
    const router = openSettings('/settings?tab=providers')
    await waitFor(() => expect(screen.getByRole('tab', { name: /^providers$/i })).toHaveAttribute('data-state', 'active'))
    expect(screen.queryByTestId('third-party-agents-section')).not.toBeInTheDocument()
    await act(async () => { await router.navigate({ to: '/settings', search: { tab: 'third-party-agents' } }) })
    await expectThirdPartyTab()
  })

  it('a misspelled query value does not accidentally select the Third-party agents tab', async () => {
    const router = openSettings('/settings?tab=third-party-agent')
    await waitFor(() => expect(screen.getByRole('tab', { name: /^providers$/i })).toHaveAttribute('data-state', 'active'))
    expect(screen.queryByTestId('third-party-agents-section')).not.toBeInTheDocument()
    expect(router.state.location.search).not.toEqual({ tab: 'third-party-agents' })
  })
})
