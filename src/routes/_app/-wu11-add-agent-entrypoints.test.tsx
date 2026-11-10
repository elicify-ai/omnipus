/**
 * -wu11-add-agent-entrypoints.test.tsx — WU-11: EVERY "Add agent" entry point
 * opens the Omnipus chat with workflow W1 (KEEP-GREEN GUARDS — not RED).
 *
 * Oracles (omnipus-system-agent-spec.md): FR-045, FR-103, BDD-4.1, BDD-12.8
 * ("no manual create action exists; Add agent opens Omnipus").
 *
 * WorkspaceTeamTab.omnipus.test.tsx already pins the LIST-view Team menu
 * (BDD-4.1) and the empty-team W18 path. This file pins the two entry points
 * NO existing test covers:
 *   1. the sidebar's per-workspace "Add agent"
 *      (WorkspaceAgentList.tsx::AddAgentRow, data-testid sidebar-add-agent-*),
 *   2. the Graph view's own picker — its FR-103 "Create with Omnipus instead"
 *      choice (TeamGraphView → AddAgentPicker onCreateWithOmnipus).
 *
 * Both are expected GREEN today (WU-10 landed the wiring); they must still
 * pass after WU-11 GREEN — if one starts opening a create form instead, this
 * file catches it. Labelled keep-green in tests/l1-wu11-red/ORACLE.md.
 *
 * Real components/stores; only the transport (WsConnection.send) and the
 * router's navigate are doubled — the same boundary the WU-10 pack uses.
 */
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { MessageFrame } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import { DEFAULT, SALES, OMNIPUS_MAIN, agent as makeAgentFixture, workspace as makeWorkspaceFixture } from '../../../tests/omnipus-l1-ui-red/fixtures'

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof import('@tanstack/react-router')>(),
  useNavigate: () => navigate,
  useLocation: () => ({ pathname: '/workspaces/sales/chat', search: {} }),
  Link: ({ children }: { children?: React.ReactNode }) => children,
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn(),
  fetchWorkspaces: vi.fn(),
  fetchWorkspaceDelegation: vi.fn(),
}))

import { fetchAgents, fetchWorkspaces, fetchWorkspaceDelegation } from '@/lib/api'
import { WorkspaceAgentList } from '@/components/layout/sidebar/WorkspaceAgentList'
import { WorkspaceContextProvider } from '@/components/workspaces/WorkspaceTabContainer'
import { WorkspaceTeamTab } from '@/components/workspaces/WorkspaceTeamTab'
import { installGraphTestGeometry } from '../../../tests/omnipus-l1-ui-red/graphGeometry'

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  })
}

let socket: WsConnection
let send = vi.fn<WsConnection['send']>()

beforeEach(() => {
  vi.clearAllMocks()
  installGraphTestGeometry()
  vi.mocked(fetchAgents).mockResolvedValue([])
  vi.mocked(fetchWorkspaces).mockResolvedValue([DEFAULT, SALES])
  vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
    revision: DEFAULT.revision,
    workspace_id: SALES.id,
    team: SALES.core_team ?? [],
    edges: [],
    default_depth: 1,
  })
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  vi.spyOn(socket, 'send').mockImplementation(send)
  useConnectionStore.setState({ connection: socket, isConnected: true, connectionError: null })
  useWorkspacesStore.setState({ activeWorkspaceId: 'sales' })
  useSessionStore.setState({ ...useSessionStore.getInitialState(), sessionByWorkspace: {} })
  // WU-11 GREEN: the create-modal store keys are removed (FR-108) — the guard
  // asserts their absence below instead of seeding them.
  useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null, toasts: [] })
  // useOpenOmnipus reads the workspace list from the query cache (the sidebar
  // keeps it warm); with no provider here that is the app singleton.
  queryClient.setQueryData(workspacesQueryKeys.list({ status: 'active' }), [DEFAULT, SALES])
})

afterEach(() => {
  cleanup()
  socket.disconnect()
  queryClient.removeQueries({ queryKey: workspacesQueryKeys.list({ status: 'active' }) })
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.restoreAllMocks()
})

/** BDD-4.1's wire contract: attach Omnipus's main, send W1 for Sales, change nothing else. */
function expectW1ForSales() {
  const messages = send.mock.calls.map(([frame]) => frame).filter((frame): frame is MessageFrame => frame.type === 'message')
  expect(messages).toHaveLength(1)
  expect(messages[0]).toMatchObject({
    type: 'message',
    session_id: OMNIPUS_MAIN,
    workflow_start: { workflow: 'W1', target_workspace_id: 'sales' },
  })
  expect(messages[0].content.trim()).not.toBe('')
  expect(send.mock.calls.map(([frame]) => frame).filter((frame) => frame.type === 'attach_session')).toEqual([
    { type: 'attach_session', session_id: OMNIPUS_MAIN },
  ])
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
  // Same behaviour without the removed key (FR-108): no manual-create flow
  // exists in the store for the entry point to open.
  expect(Object.hasOwn(useUiStore.getState(), 'createAgentModalOpen')).toBe(false)
}

describe('WU-11 keep-green — every Add agent entry opens Omnipus with W1 (FR-045, FR-103, BDD-4.1, BDD-12.8)', () => {
  it('the sidebar "Add agent" row starts W1 in the Omnipus main and navigates to /omnipus', async () => {
    const user = userEvent.setup()
    const sales = makeWorkspaceFixture('sales', {
      core_team: ['mia'],
      member_configs: { mia: { main_session_id: 'opaque-sales-mia-main' } },
    })
    const mia = makeAgentFixture('mia', { type: 'core' })
    render(
      <WorkspaceAgentList
        projects={[sales]}
        expandedIds={new Set(['sales'])}
        activeId={null}
        agents={[mia]}
        rosterState="fresh"
        sessions={[]}
        onToggle={() => {}}
        onOpen={() => {}}
        onOverlayClose={() => {}}
      />,
    )
    await user.click(screen.getByTestId('sidebar-add-agent-sales'))
    expectW1ForSales()
    expect(navigate).toHaveBeenCalledWith({ to: '/omnipus' })
  })

  it('the Graph view picker offers "Create with Omnipus instead" and it starts W1', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    client.setQueryData(workspacesQueryKeys.list({ status: 'active' }), [DEFAULT, SALES])
    render(
      <QueryClientProvider client={client}>
        <WorkspaceContextProvider workspace={SALES}>
          <WorkspaceTeamTab workspaceId={SALES.id} />
        </WorkspaceContextProvider>
      </QueryClientProvider>,
    )
    await user.click(screen.getByTestId('team-view-graph'))
    // The graph toolbar (carrying the picker) renders once the delegation
    // query settles; a failed load shows the error branch instead.
    await user.click(await screen.findByTestId('team-add-agent', {}, { timeout: 10_000 }))
    await user.click(screen.getByRole('button', { name: /create with omnipus/i }))
    expectW1ForSales()
    expect(navigate).toHaveBeenCalledWith({ to: '/omnipus' })
  })
})
