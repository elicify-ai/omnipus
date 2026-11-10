// WU-10 RED. Oracles: omnipus-system-agent-spec §6.3/6.8, FR-102/103/109/112,
// BDD-4.1, 5.2, 5.9, 12.3/10/13/14; ADR OSA-10 U6 and OSA-11.
// Real Team/context/query/controls/stores. Only API and socket transport are doubled.
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Workspace } from '@/lib/api/generated/openapi-types'
import type { MessageFrame } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { ApiError } from '@/lib/api-error'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { WorkspaceContextProvider } from './WorkspaceTabContainer'
import { WorkspaceTeamTab } from './WorkspaceTeamTab'
import { DEFAULT, EXTERNAL_WORKER, JUDGE, MIA, OMNIPUS, OMNIPUS_MAIN, RESEARCH, REVISION, SALES, WORKER, agent, delegation } from '../../../tests/omnipus-l1-ui-red/fixtures'
import { installGraphTestGeometry } from '../../../tests/omnipus-l1-ui-red/graphGeometry'

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof import('@tanstack/react-router')>(),
  useNavigate: () => navigate,
  useLocation: () => ({ pathname: '/workspaces/sales/team', search: {} }),
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn(), fetchWorkspaces: vi.fn(), fetchWorkspaceDelegation: vi.fn(),
  updateWorkspace: vi.fn(), updateWorkspaceDelegation: vi.fn(),
}))
import { fetchAgents, fetchWorkspaces, fetchWorkspaceDelegation, updateWorkspace, updateWorkspaceDelegation, workspacesQueryKeys } from '@/lib/api'

// React Flow geometry is an environment boundary, not a substitute graph.
const clients: QueryClient[] = []
let socket: WsConnection
let send = vi.fn<WsConnection['send']>()

beforeEach(() => {
  vi.clearAllMocks()
  installGraphTestGeometry()
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  vi.mocked(fetchAgents).mockResolvedValue([MIA, WORKER, EXTERNAL_WORKER, OMNIPUS, JUDGE])
  vi.mocked(fetchWorkspaces).mockResolvedValue([DEFAULT, SALES, RESEARCH])
  vi.mocked(fetchWorkspaceDelegation).mockResolvedValue(delegation(SALES))
  vi.mocked(updateWorkspace).mockResolvedValue(SALES)
  vi.mocked(updateWorkspaceDelegation).mockResolvedValue(delegation(SALES))
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  vi.spyOn(socket, 'send').mockImplementation(send)
  useConnectionStore.setState({ connection: socket, isConnected: true, connectionError: null })
  useWorkspacesStore.setState({ activeWorkspaceId: 'sales' })
  useSessionStore.setState({ ...useSessionStore.getInitialState(), sessionByWorkspace: {} })
  // WU-11 GREEN: the create-modal store keys are removed (FR-108); the
  // tests below assert their absence instead of seeding them.
  useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null, toasts: [] })
})
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  socket.disconnect()
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderTeam(ws: Workspace = SALES) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  client.setQueryData(workspacesQueryKeys.list({ status: 'active' }), [DEFAULT, ws, RESEARCH])
  render(<QueryClientProvider client={client}>
    <WorkspaceContextProvider workspace={ws}><WorkspaceTeamTab workspaceId={ws.id} /></WorkspaceContextProvider>
  </QueryClientProvider>)
  return client
}
async function settled(client: QueryClient) {
  await waitFor(() => expect(client.isFetching()).toBe(0))
}
async function listReady(client: QueryClient) {
  await settled(client)
  expect(screen.getByRole('radio', { name: /^list$/i })).toBeChecked()
}
async function openActions(user: ReturnType<typeof userEvent.setup>, name: string) {
  const direct = screen.queryByRole('button', { name: `Edit ${name}` })
  if (!direct) {
    await user.click(screen.getByRole('button', { name: new RegExp(`actions.*${name}|${name}.*actions|more.*${name}|${name}.*more`, 'i') }))
  }
}

function expectWorkflow(workflow: 'W1' | 'W18') {
  const messages = send.mock.calls.map(([frame]) => frame).filter((frame): frame is MessageFrame => frame.type === 'message')
  expect(messages).toHaveLength(1)
  expect(messages[0]).toMatchObject({ type: 'message', session_id: OMNIPUS_MAIN,
    workflow_start: { workflow, target_workspace_id: 'sales' } })
  // OSA-11 requires a normal user request; its prose is deliberately not fixed.
  expect(messages[0].content.trim()).not.toBe('')
  expect(messages[0].metadata?.workspace_id).toBeUndefined()
  expect(send.mock.calls.map(([frame]) => frame).filter((frame) => frame.type === 'attach_session')).toEqual([
    { type: 'attach_session', session_id: OMNIPUS_MAIN },
  ])
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
  expect(useSessionStore.getState().sessionByWorkspace.sales).toBeUndefined()
  expect(Object.hasOwn(useUiStore.getState(), 'createAgentModalOpen')).toBe(false)
}

describe('WU-10 Team list, not the old graph-only surface', () => {
  it('BDD-12.3 selects List initially and offers Graph as the second view', async () => {
    const client = renderTeam()
    await listReady(client)
    expect(screen.getByRole('radio', { name: /^graph$/i })).not.toBeChecked()
    expect(screen.queryByTestId('team-graph-canvas')).not.toBeInTheDocument()
  })
  it('BDD-12.3 shows names, avatar, conversation kind, model and other workspaces', async () => {
    const client = renderTeam()
    await listReady(client)
    expect(screen.getByText('Mia', { exact: true })).toBeVisible()
    expect(screen.getByText(/conversation agent/i)).toBeVisible()
    expect(screen.getByText('sales-model', { exact: true })).toBeVisible()
    expect(screen.getByText(/\bResearch\b/)).toBeVisible()
    expect(screen.getAllByTestId('agent-icon')).toHaveLength(3)
  })
  it('BDD-12.3 keeps both native and external workers in a separate workers section', async () => {
    const client = renderTeam()
    await listReady(client)
    const heading = screen.getByRole('heading', { name: /workers/i })
    const mia = screen.getByText('Mia', { exact: true })
    expect(heading.compareDocumentPosition(mia) & Node.DOCUMENT_POSITION_PRECEDING).toBe(Node.DOCUMENT_POSITION_PRECEDING)
    for (const name of ['Researcher', 'External worker']) {
      expect(heading.compareDocumentPosition(screen.getByText(name, { exact: true })) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    }
  })
  it('US-1 AS-4 hides Omnipus from the default-workspace list, not just its avatar', async () => {
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue(delegation(DEFAULT))
    const client = renderTeam(DEFAULT)
    await listReady(client)
    expect(screen.getByText('Mia', { exact: true })).toBeVisible()
    expect(screen.queryByText('Omnipus', { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /edit omnipus/i })).not.toBeInTheDocument()
  })
  it('FR-111 / BDD-12.12 keeps Omnipus and its editable self-row visible in the Default graph', async () => {
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({ ...delegation(DEFAULT), edges: [
      { from_agent: 'omnipus', to_agent: 'omnipus', modes: ['direct', 'task'], depth: 1 },
    ] })
    const client = renderTeam(DEFAULT)
    await listReady(client)
    await userEvent.setup().click(screen.getByRole('radio', { name: /^graph$/i }))
    expect(await screen.findByTestId('team-node-omnipus')).toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Edit delegation omnipus to omnipus' }))
    expect(screen.getByRole('spinbutton', { name: /delegation depth/i })).toBeEnabled()
    expect(screen.getByTestId('team-edge-delete-omnipus-omnipus')).toBeEnabled()
    // Persistence after reload and next-helper refusal are real-browser/backend follow-ups.
  })
  it('BDD-12.14 switches List to Graph and back using only arrows and Space', async () => {
    const user = userEvent.setup()
    const client = renderTeam()
    await listReady(client)
    screen.getByRole('radio', { name: /^list$/i }).focus()
    await user.keyboard('{ArrowRight}{Space}')
    expect(screen.getByRole('radio', { name: /^graph$/i })).toBeChecked()
    expect(await screen.findByTestId('team-graph-canvas')).toBeInTheDocument()
    await user.keyboard('{ArrowLeft}{Space}')
    expect(screen.getByRole('radio', { name: /^list$/i })).toBeChecked()
    expect(screen.getByText('sales-model', { exact: true })).toBeVisible()
  })
  it('BDD-12.10 exposes Edit, Remove from this workspace and Delete everywhere without hover', async () => {
    const user = userEvent.setup()
    const client = renderTeam()
    await listReady(client)
    await openActions(user, 'Mia')
    expect(screen.getByRole('button', { name: /edit mia/i })).toBeEnabled()
    expect(screen.getByRole('button', { name: /remove.*this workspace/i })).toBeEnabled()
    expect(screen.getByRole('button', { name: /delete everywhere/i })).toBeEnabled()
  })
  it('BDD-12.10 keyboard Edit opens the shared editor for Mia in Sales', async () => {
    const user = userEvent.setup()
    const client = renderTeam()
    await listReady(client)
    await openActions(user, 'Mia')
    screen.getByRole('button', { name: /edit mia/i }).focus()
    await user.keyboard('{Enter}')
    expect(useUiStore.getState().editAgentId).toBe('mia')
    expect(useUiStore.getState().editAgentWorkspaceId).toBe('sales')
  })
  it('FR-103 offers the two Add choices before any manual creation or membership write', async () => {
    const user = userEvent.setup()
    const client = renderTeam()
    await listReady(client)
    await user.click(screen.getByRole('button', { name: /^add agent$/i }))
    expect(screen.getByRole('menuitem', { name: /^create with omnipus$/i })).toBeVisible()
    expect(screen.getByRole('menuitem', { name: /^add an existing agent$/i })).toBeVisible()
    expect(Object.hasOwn(useUiStore.getState(), 'createAgentModalOpen')).toBe(false)
    expect(updateWorkspace).not.toHaveBeenCalled()
  })
  it('BDD-4.1 starts W1 in the existing Omnipus main and keeps Sales active', async () => {
    const user = userEvent.setup()
    const client = renderTeam()
    await listReady(client)
    await user.click(screen.getByRole('button', { name: /^add agent$/i }))
    await user.click(screen.getByRole('menuitem', { name: /^create with omnipus$/i }))
    expectWorkflow('W1')
    expect(navigate).toHaveBeenCalledWith({ to: '/omnipus' })
  })
  it('BDD-12.14 opens Add choices and Create with Omnipus entirely by keyboard', async () => {
    const user = userEvent.setup()
    const client = renderTeam()
    await listReady(client)
    screen.getByRole('button', { name: /^add agent$/i }).focus()
    await user.keyboard('{Enter}')
    const create = screen.getByRole('menuitem', { name: /^create with omnipus$/i })
    create.focus()
    await user.keyboard('{Enter}')
    expectWorkflow('W1')
  })
})

describe('WU-10 Team state contract (FR-112, spec §6.3)', () => {
  it('BDD-5.2 shows a genuine empty team with setup and add, even if Judge exists globally', async () => {
    const empty = { ...SALES, core_team: [], member_configs: {} }
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue(delegation(empty))
    const client = renderTeam(empty)
    await settled(client)
    expect(screen.getByText(/^no agents yet$/i)).toBeVisible()
    expect(screen.getByRole('button', { name: /^set up a team$/i })).toBeEnabled()
    expect(screen.getAllByRole('button', { name: /^add agent$/i }).length).toBeGreaterThanOrEqual(1)
  })
  it('BDD-5.2 starts W18 for the empty target instead of creating a target-workspace Omnipus chat', async () => {
    const empty = { ...SALES, core_team: [], member_configs: {} }
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue(delegation(empty))
    const client = renderTeam(empty)
    await settled(client)
    await userEvent.setup().click(screen.getByRole('button', { name: /^set up a team$/i }))
    expectWorkflow('W18')
  })
  it('BDD-5.9 failed agent loading is error plus Retry, never a setup offer', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('Agent roster offline'))
    const client = renderTeam()
    await settled(client)
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(screen.getByText(/failed.*agents|agents.*failed|could not load.*team/i)).toBeVisible()
    expect(screen.queryByRole('button', { name: /^set up a team$/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/^no agents yet$/i)).not.toBeInTheDocument()
  })
  it('BDD-12.13 retry recovers failed agent loading to the actual list', async () => {
    vi.mocked(fetchAgents).mockRejectedValueOnce(new Error('Agent roster offline')).mockResolvedValue([MIA, WORKER, EXTERNAL_WORKER])
    const client = renderTeam()
    await settled(client)
    await userEvent.setup().click(screen.getByRole('button', { name: /^retry$/i }))
    await listReady(client)
    expect(screen.getByText('Mia', { exact: true })).toBeVisible()
    expect(screen.queryByText(/Agent roster offline/)).not.toBeInTheDocument()
  })
  it('BDD-12.13 a refresh failure retains cached cards and says may be out of date', async () => {
    const client = renderTeam()
    await settled(client)
    vi.mocked(fetchAgents).mockRejectedValue(new Error('Refresh offline'))
    await act(async () => { await client.invalidateQueries({ queryKey: ['agents'] }) })
    // Preserve every original postcondition; invalidate completion precedes DOM delivery.
    await waitFor(() => {
      expect(screen.getByText(/may be out of date/i)).toBeVisible()
      expect(screen.getByText('Mia', { exact: true })).toBeVisible()
      expect(screen.getByText('sales-model', { exact: true })).toBeVisible()
      expect(screen.queryByRole('button', { name: /^set up a team$/i })).not.toBeInTheDocument()
    })
  })
  it('section 6.3 a failed Graph refresh retains its cached graph with a stale hint', async () => {
    const client = renderTeam()
    await listReady(client)
    await userEvent.setup().click(screen.getByRole('radio', { name: /^graph$/i }))
    await settled(client)
    expect(screen.getByTestId('team-graph-canvas')).toBeInTheDocument()
    vi.mocked(fetchWorkspaceDelegation).mockRejectedValue(new Error('Delegation refresh offline'))
    await act(async () => { await client.invalidateQueries({ queryKey: workspacesQueryKeys.delegation('sales') }) })
    // Same cue and retained-graph assertions, after the real observer render.
    await waitFor(() => {
      expect(screen.getByText(/may be out of date|stale|last known/i)).toBeVisible()
      expect(screen.getByTestId('team-graph-canvas')).toBeInTheDocument()
    })
  })
  it('BDD-12.13 an add response lost after submit is not confirmed, with Retry and no false Added', async () => {
    const orphan = agent('orphan', { name: 'Orphan' })
    vi.mocked(fetchAgents).mockResolvedValue([MIA, WORKER, EXTERNAL_WORKER, orphan])
    vi.mocked(updateWorkspace).mockRejectedValue(new ApiError(0, 'Response lost after submit'))
    const client = renderTeam()
    await listReady(client)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /^add agent$/i }))
    await user.click(screen.getByRole('menuitem', { name: /^add an existing agent$/i }))
    await user.click(await screen.findByRole('button', { name: /Orphan/ }))
    await waitFor(() => expect(updateWorkspace).toHaveBeenCalledWith('sales', expect.objectContaining({
      revision: REVISION, core_team: ['mia', 'researcher', 'external', 'orphan'],
    })))
    expect(await screen.findByText(/not confirmed/i)).toBeVisible()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(screen.queryByText(/^added(?:\s*·\s*undo)?$/i)).not.toBeInTheDocument()
  })
  it('FR-112 loading is not mislabeled as a healthy empty team', () => {
    vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {}))
    renderTeam()
    expect(screen.queryByText(/^no agents yet$/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^set up a team$/i })).not.toBeInTheDocument()
  })
})
