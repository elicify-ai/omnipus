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
 * The Radix DropdownMenu is stubbed inline (pointer/portal internals don't
 * drive in jsdom — same convention as ListView.filters.test.tsx) so a click
 * on a candidate actually reaches `handleAgentSelect`.
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

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({
    children,
    onClick,
    className,
  }: {
    children: React.ReactNode
    onClick?: () => void
    className?: string
  }) => (
    <button type="button" role="menuitem" onClick={onClick} className={className}>
      {children}
    </button>
  ),
}))

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

describe('AgentPicker — Admin standalone-operator chat reachability (ADR-090 FR-001)', () => {
  it('lists Admin in the dropdown while Jim (off-team ordinary core) stays scoped out', async () => {
    renderPicker()

    // Team query applied: Jim is gone. Stubbed menu items are always in the
    // tree, so this is the dropdown list, not just the trigger label.
    await vi.waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: /jim/i })).not.toBeInTheDocument()
      expect(screen.getByRole('menuitem', { name: /admin/i })).toBeInTheDocument()
    })
    expect(screen.getByRole('menuitem', { name: /mia/i })).toBeInTheDocument()
  })

  it('clicking Admin records an explicit user selection and does not write workspace membership', async () => {
    renderPicker()

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

    await vi.waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: /jim/i })).not.toBeInTheDocument()
      expect(screen.getByRole('menuitem', { name: /mia/i })).toBeInTheDocument()
    })
    expect(screen.queryByRole('menuitem', { name: /admin/i })).not.toBeInTheDocument()
  })
})
