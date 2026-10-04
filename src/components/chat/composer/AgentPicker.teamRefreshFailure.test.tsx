/**
 * AgentPicker — failed team refresh must never read as current (review finding).
 *
 * Opening the picker invalidates the workspaces query (useChatAgents' pickerOpen
 * effect) to re-read the active workspace's core_team. If THAT refresh fails,
 * React Query keeps the previous team data and renders it — which is right (the
 * last known team is still the best list we have) but, without a signal, stale
 * data looks current: the user picks from a team that may no longer be the team.
 *
 * The docs sentence in docs/workspaces.md ("How to set who may delegate to
 * whom", step 2) promises the picker refreshes the team on open; this test pins
 * the failure branch of that promise: the previous team stays visible AND a
 * notice says the list could not be refreshed.
 *
 * Fails on the old behaviour (no notice rendered); passes once AgentPicker
 * reads the workspaces query's error state via useChatAgents and shows the
 * line while the open dropdown holds a team that failed to refresh.
 *
 * The dropdown is the REAL, catalogued DropdownMenu (not stubbed) — same
 * pattern as AgentPicker.adminStandalone.test.tsx: the flag-controlled open
 * state renders the Radix portal content into document.body, where `screen`
 * queries reach it.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { act } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'
import * as api from '@/lib/api'

// useChatAgents invalidates the workspaces query through the MODULE-LEVEL
// queryClient singleton (src/lib/queryClient.ts), not through a context
// client — in the app main.tsx passes that same singleton to the provider, so
// invalidations and observers share one cache. Reproduce that topology here:
// one test-owned client wired to BOTH the mocked module and the provider.
// (The real singleton retries with exponential backoff up to 30s — a failed
// refresh under it would not reach isError inside any sane waitFor.)
const { testQueryClient } = await vi.hoisted(async () => {
  const { QueryClient } = await import('@tanstack/react-query')
  return {
    testQueryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }),
  }
})

vi.mock('@/lib/queryClient', () => ({
  queryClient: testQueryClient,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchWorkspaces: vi.fn(),
  }
})

vi.mock('@/components/shared/IconRenderer', () => ({
  IconRenderer: () => null,
}))

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { AgentPicker } from './AgentPicker'

const MIA = makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', description: 'Assistant' })
const JIM = makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle', description: 'Orchestrator' })
const ADMIN = makeAgent({
  id: 'admin',
  name: 'Admin',
  type: 'core',
  locked: true,
  status: 'idle',
  description: 'Standalone operator',
})

function renderPicker() {
  return render(
    <QueryClientProvider client={testQueryClient}>
      <AgentPicker />
    </QueryClientProvider>,
  )
}

function miaOnlyWorkspace() {
  return [
    {
      id: 'ws-picker-refresh',
      name: 'Team WS',
      status: 'active',
      core_team: ['mia'],
    },
  ]
}

beforeEach(() => {
  vi.clearAllMocks()
  testQueryClient.clear()
  // First call (mount-time load) succeeds — the picker starts from a real,
  // known team. Every LATER call (the refresh the open triggers) fails.
  vi.mocked(api.fetchWorkspaces)
    .mockResolvedValueOnce(miaOnlyWorkspace() as never)
    .mockRejectedValue(new Error('team refresh failed'))
  vi.mocked(api.fetchAgents).mockResolvedValue([MIA, JIM, ADMIN])
  act(() => {
    useSessionStore.setState({
      activeAgentId: null,
      activeSessionId: null,
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
      sessionByWorkspace: {},
    })
    useUiStore.setState({ agentSelectorOpen: false })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-picker-refresh' })
  })
})

describe('AgentPicker — failed team refresh shows the last known team with a notice', () => {
  it('keeps the previous team visible and says the list could not be refreshed', async () => {
    renderPicker()
    await vi.waitFor(() => expect(vi.mocked(api.fetchAgents)).toHaveBeenCalled())
    // Initial (successful) team load must have landed before we open, so the
    // "last known team" the notice talks about genuinely exists.
    await vi.waitFor(() => {
      expect(vi.mocked(api.fetchWorkspaces).mock.calls.length).toBe(1)
    })

    await act(async () => {
      useUiStore.getState().setAgentSelectorOpen(true)
    })

    // Opening triggered the refresh; it failed (second workspaces call).
    await vi.waitFor(() => {
      expect(vi.mocked(api.fetchWorkspaces).mock.calls.length).toBeGreaterThan(1)
    })

    // The notice is the point of this test — absent on the old behaviour.
    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-team-refresh-notice')).toHaveTextContent(
        'Team list could not be refreshed — showing the last known team.',
      )
    })

    // Previous team still visible and still team-scoped: Mia stays, the
    // off-team Jim does not sneak back in because the refresh failed.
    expect(screen.getByRole('menuitem', { name: /mia/i })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /jim/i })).not.toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /admin/i })).toBeInTheDocument()
  })
})
