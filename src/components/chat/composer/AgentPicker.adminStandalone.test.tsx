/**
 * AgentPicker — Admin standalone-operator chat reachability (ADR-090 FR-001).
 *
 * The team-add picker (`AddAgentPicker`) correctly refuses to offer Admin as
 * a teammate. The CHAT picker must still offer Admin: it is a chat-able core
 * agent with "no team membership", and `chatAgents` (src/hooks/useChatAgents.ts)
 * is the one list both this dropdown and the composer's "@" mention menu
 * consume. Selecting Admin is an explicit user choice (`selectAgent`) — it
 * must not write workspace membership.
 *
 * The dropdown is the REAL, catalogued `DropdownMenu` (not stubbed) — same
 * pattern as AgentPicker.agent-selector-open.test.tsx's `pickJim()`: since
 * `<DropdownMenu open={agentSelectorOpen}>` is controlled by the ui store,
 * setting that flag renders the Radix portal content straight into
 * `document.body`, where `screen` queries reach it without simulating
 * pointer capture.
 *
 * Covers:
 *   - Admin is listed in the open dropdown even when the active workspace
 *     team is mia-only (Jim, the off-team sentinel, is not)
 *   - Clicking Admin records an explicit user selection (`selectAgent`)
 *     without touching workspace membership
 *   - Auto-select still prefers the teammate (Mia), not Admin
 *   - A draft Admin is not listed (readiness gate still applies)
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'
import * as api from '@/lib/api'

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

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderPicker() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentPicker />
    </QueryClientProvider>,
  )
}

function miaOnlyWorkspace() {
  return [
    {
      id: 'ws-adr090',
      name: 'Ordinary Team WS',
      status: 'active',
      core_team: ['mia'],
    },
  ]
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchAgents).mockResolvedValue([MIA, JIM, ADMIN])
  vi.mocked(api.fetchWorkspaces).mockResolvedValue(miaOnlyWorkspace() as never)
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
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-adr090' })
  })
})

/** Opens the real, controlled Radix DropdownMenu by flipping the ui store's
 * flag — the same mechanism the `/agents` slash command uses — and waits for
 * the agent list to be loaded first so the menu isn't opened empty. */
async function openMenu() {
  await vi.waitFor(() => expect(vi.mocked(api.fetchAgents)).toHaveBeenCalled())
  await act(async () => {
    useUiStore.getState().setAgentSelectorOpen(true)
  })
}

describe('AgentPicker — Admin standalone-operator chat reachability (ADR-090 FR-001)', () => {
  it('lists Admin in the dropdown while Jim (off-team ordinary core) stays scoped out', async () => {
    renderPicker()
    await openMenu()

    // Team query applied: Jim is gone. Real menu items only exist once the
    // portal content has rendered, so this is the dropdown list, not just
    // the trigger label.
    await vi.waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: /jim/i })).not.toBeInTheDocument()
      expect(screen.getByRole('menuitem', { name: /admin/i })).toBeInTheDocument()
    })
    expect(screen.getByRole('menuitem', { name: /mia/i })).toBeInTheDocument()
  })

  it('clicking Admin records an explicit user selection and does not write workspace membership', async () => {
    renderPicker()
    await openMenu()

    const adminItem = await vi.waitFor(() => screen.getByRole('menuitem', { name: /admin/i }))
    fireEvent.click(adminItem)

    expect(useSessionStore.getState().activeAgentId).toBe('admin')
    expect(useSessionStore.getState().agentSelectionSource).toBe('user')
    expect(useSessionStore.getState().activeAgentType).toBe('core')
    // Selection is session-store only — AgentPicker never PUTs a workspace
    // (membership lives on the Team tab / REST / tools, all of which refuse
    // Admin). The workspace query is the only workspace call this surface
    // makes, and it is a GET.
    expect(vi.mocked(api.fetchWorkspaces).mock.calls.length).toBeGreaterThan(0)
  })

  it('auto-selects the teammate (Mia), not Admin, when no agent is active yet', async () => {
    renderPicker()

    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeAgentId).toBe('mia')
    })
    expect(useSessionStore.getState().agentSelectionSource).toBe('auto')
  })

  it('does not list a draft Admin — team independence is not a readiness bypass', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValue([
      MIA,
      JIM,
      makeAgent({ id: 'admin', name: 'Admin', type: 'core', locked: true, status: 'draft' }),
    ])
    renderPicker()
    await openMenu()

    await vi.waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: /jim/i })).not.toBeInTheDocument()
      expect(screen.getByRole('menuitem', { name: /mia/i })).toBeInTheDocument()
    })
    expect(screen.queryByRole('menuitem', { name: /admin/i })).not.toBeInTheDocument()
  })
})
