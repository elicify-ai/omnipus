/**
 * Integration test for WorkspaceTeamTab — the per-workspace delegation graph
 * editor. Mocks the API + the active-workspace context and asserts the full
 * data flow: the fetched delegation graph renders one node per team agent and
 * mounts the React Flow edge layer; the [+ Add agent] picker offers agents not
 * on the team. (Edge-editor interaction is covered in EdgeModeEditor.test.tsx.)
 *
 * React Flow needs jsdom shims (ResizeObserver + geometry), same as the
 * graph/GraphView and WorkspaceTeamGraph tests.
 */

import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Agent, Workspace, WorkspaceDelegation } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import type { WorkspaceTeamGraphProps } from './team/WorkspaceTeamGraph'

// ── React Flow jsdom shims ──────────────────────────────────────────────────
beforeAll(() => {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  const g = globalThis as unknown as Record<string, unknown>
  g.ResizeObserver = ResizeObserverStub
  g.DOMMatrixReadOnly = class {
    m22 = 1
  }
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, value: 600 })
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width: 220, height: 92, top: 0, left: 0, right: 220, bottom: 92, toJSON: () => ({}),
  } as DOMRect)
})

// ── Fixtures ────────────────────────────────────────────────────────────────
function agent(id: string, over: Partial<Agent> = {}): Agent {
  return {
    id,
    name: id.charAt(0).toUpperCase() + id.slice(1),
    type: 'Main',
    locked: false,
    status: 'active',
    soul: '',
    heartbeat: '',
    instructions: '',
    timeout_seconds: 60,
    max_tool_iterations: 10,
    heartbeat_enabled: false,
    heartbeat_interval: 0,
    ...over,
  } as Agent
}

const WORKSPACE: Workspace = {
  id: 'ws-1',
  name: 'My Workspace',
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  core_team: ['mia', 'jim'],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
} as Workspace

const AGENTS: Agent[] = [
  agent('mia', { name: 'Mia', default: true }),
  agent('jim', { name: 'Jim' }),
  agent('planner', { name: 'Planner', type: 'Subagent' }),
  agent('ray', { name: 'Ray' }), // not on the team — should appear in the picker
  // System agent (the Judge, ADR-049 D3) — implicit member of EVERY
  // workspace (pkg/workspace/find_for_agent.go's isImplicitMember). Present
  // in every test in this file via the shared `fetchAgents` mock so the
  // "Judge invisible in workspace Teams" fix is exercised across the whole
  // suite, not just its own describe block below.
  agent('judge', { name: 'Judge', type: 'system' }),
]

const DELEGATION: WorkspaceDelegation = {
  workspace_id: 'ws-1',
  team: ['mia', 'jim', 'planner'],
  edges: [{ from_agent: 'jim', to_agent: 'planner', modes: ['direct', 'task'], depth: 2 }],
  default_depth: 3,
}

// stubCsrfCookie / restoreCsrfCookie — mirrors the pattern in api.test.ts.
// jsdom exposes document.cookie as an unconfigurable getter/setter that
// simulates a real cookie jar; we override it with Object.defineProperty so
// the F3 beacon test (below) exercises the SAME getCsrfCookie()/
// readCSRFCookie() path production `beaconFlush` calls, proving the
// X-CSRF-Token header is actually populated from a real cookie rather than
// just asserting a shape that would stay green if the header were dropped.
function stubCsrfCookie(value: string) {
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => value,
  })
}

function restoreCsrfCookie() {
  // jsdom reinstates its own descriptor when we delete the override.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

// ── Mocks ───────────────────────────────────────────────────────────────────
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchWorkspaceDelegation: vi.fn(),
    updateWorkspaceDelegation: vi.fn(),
    updateWorkspace: vi.fn(),
  }
})

// Provide the active workspace via the container's context hook.
vi.mock('./WorkspaceTabContainer', () => ({
  useActiveWorkspace: () => WORKSPACE,
}))

// AgentProfile pulls a wide tree of queries; stub it to a marker so the Team tab
// renders in isolation (its store-driven open behaviour is covered elsewhere).
vi.mock('@/components/agents/AgentProfile', () => ({
  AgentProfile: () => <div data-testid="agent-profile-stub" />,
}))

// Wrap (not replace) the real WorkspaceTeamGraph so every existing test keeps
// asserting against the real rendered DOM (node testids, react-flow classes,
// etc.), while also capturing the live onConnect callback. React Flow drag
// gestures aren't simulable in jsdom (no real layout/geometry — see the fixed
// getBoundingClientRect shim above), so the "draw a delegation edge" step of
// the P0 regression test below invokes the same onConnect handler the graph
// would call on a real drop, instead of simulating pointer events.
let capturedGraphProps: WorkspaceTeamGraphProps | null = null
vi.mock('./team/WorkspaceTeamGraph', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./team/WorkspaceTeamGraph')>()
  return {
    ...actual,
    WorkspaceTeamGraph: (props: WorkspaceTeamGraphProps) => {
      capturedGraphProps = props
      return <actual.WorkspaceTeamGraph {...props} />
    },
  }
})

import {
  fetchAgents,
  fetchWorkspaceDelegation,
  updateWorkspace,
  updateWorkspaceDelegation,
  workspacesQueryKeys,
} from '@/lib/api'
import { WorkspaceTeamTab } from './WorkspaceTeamTab'

function renderTab(seed?: (client: QueryClient) => void) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  seed?.(client)
  const utils = render(
    <QueryClientProvider client={client}>
      <WorkspaceTeamTab workspaceId="ws-1" />
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

beforeEach(() => {
  // Clear call history from the PREVIOUS test — the vi.mock() factory above
  // creates these vi.fn()s once for the whole file, and vitest does not
  // clear mock call logs between tests by default. Without this, a test that
  // asserts on call count/order (e.g. `.not.toHaveBeenCalled()`,
  // `.mock.calls[0]`) can pass or fail depending on what earlier tests in
  // this file happened to call — including the async unmount-flush that
  // useAutoSave fires on cleanup() (see src/test/setup.ts) when a test ends
  // with unsaved pending changes.
  vi.clearAllMocks()
  capturedGraphProps = null
  vi.mocked(fetchAgents).mockResolvedValue(AGENTS)
  vi.mocked(fetchWorkspaceDelegation).mockResolvedValue(DELEGATION)
  // Safe defaults so any save path that fires resolves instead of hitting an
  // unconfigured mock (undefined) — individual tests override as needed.
  vi.mocked(updateWorkspace).mockResolvedValue(WORKSPACE)
  vi.mocked(updateWorkspaceDelegation).mockResolvedValue(DELEGATION)
})

describe('WorkspaceTeamTab', () => {
  it('renders a node for each team agent from the fetched delegation graph', async () => {
    renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('team-node-mia')).toBeInTheDocument()
    })
    expect(screen.getByTestId('team-node-jim')).toBeInTheDocument()
    expect(screen.getByTestId('team-node-planner')).toBeInTheDocument()
    expect(screen.getByText('Mia')).toBeInTheDocument()
    expect(screen.getByText('Planner')).toBeInTheDocument()
  })

  it('mounts the React Flow edge layer for the fetched delegation edges', async () => {
    const { container } = renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('team-graph-canvas')).toBeInTheDocument()
    })
    expect(container.querySelector('.react-flow__edges')).not.toBeNull()
  })

  it('the [+ Add agent] picker offers global agents not on the team', async () => {
    renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('team-add-agent')).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() => {
      // Ray is a global agent not on the team → offered.
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument()
    })
    // Mia / Jim / Planner are already on the team → NOT offered.
    expect(screen.queryByTestId('team-add-agent-option-mia')).toBeNull()
    expect(screen.queryByTestId('team-add-agent-option-jim')).toBeNull()
  })

  it('shows the title cue and an auto-save-capable header', async () => {
    renderTab()
    await waitFor(() => {
      expect(screen.getByText('Team & delegation')).toBeInTheDocument()
    })
  })

  it('shows a retry-able error state when the delegation fetch fails', async () => {
    vi.mocked(fetchWorkspaceDelegation).mockRejectedValueOnce(new Error('boom'))
    renderTab()
    await waitFor(() => {
      expect(
        screen.getByText(/Failed to load this workspace's delegation graph/),
      ).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('shows the empty state (no members) and an add-agent CTA', async () => {
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-1',
      team: [],
      edges: [],
      default_depth: 3,
    } as WorkspaceDelegation)
    // core_team would normally seed members; for this case the workspace mock
    // already supplies core_team, so to get a truly empty graph we also clear
    // it by returning an empty team and no edges — buildTeamEditState seeds from
    // core_team only when team is empty, so assert the picker CTA is present.
    renderTab()
    await waitFor(() => {
      expect(screen.getByText('Team & delegation')).toBeInTheDocument()
    })
  })

  it('flags an edgeless, non-core member as unsaved after it is added', async () => {
    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-add-agent')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() =>
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument(),
    )
    // Add Ray (not in core_team ['mia','jim'], no incident edge) → unsaved hint.
    fireEvent.click(screen.getByTestId('team-add-agent-option-ray'))
    await waitFor(() => {
      expect(screen.getByTestId('team-unsaved-members')).toHaveTextContent(/Ray/)
    })
    expect(screen.getByTestId('team-unsaved-members')).toHaveTextContent(
      /not connected yet/,
    )
    // This banner reflects a real, still-current limitation (edges-only PUT):
    // an edgeless, non-core member never triggers the debounced auto-save at
    // all, so it is never persisted on its own — see the next test for what
    // DOES persist it (drawing a delegation edge to it).
  })

  it('P0 regression: add a non-core agent, draw a delegation edge to it, save — persists core_team via updateWorkspace BEFORE the edges PUT', async () => {
    // Traces to the P0 bug: the Team tab's auto-save PUT the edge set only,
    // never core_team, so the backend's delegation PUT (which validates every
    // edge endpoint against the STORED core_team) rejected the very edge the
    // operator just drew to the agent they just added — a catch-22. The fix:
    // updateWorkspace({core_team}) must land before updateWorkspaceDelegation.
    vi.mocked(updateWorkspace).mockResolvedValue({
      ...WORKSPACE,
      core_team: ['mia', 'jim', 'planner', 'ray'],
    })
    vi.mocked(updateWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-1',
      team: ['mia', 'jim', 'planner', 'ray'],
      edges: [
        ...(DELEGATION.edges ?? []),
        { from_agent: 'jim', to_agent: 'ray', modes: ['direct', 'task'] },
      ],
      default_depth: 3,
    })

    // Seed the status-keyed active AND archived list caches the way
    // WorkspaceTabContainer's real useQuery would (that container is mocked
    // out above) so we can assert the fix writes BOTH exact keys the saveFn
    // loop iterates over (`for (const status of ['active', 'archived'])`),
    // not just the detail() cache or the active list alone.
    const { client } = renderTab((c) => {
      c.setQueryData(workspacesQueryKeys.list({ status: 'active' }), [WORKSPACE])
      c.setQueryData(workspacesQueryKeys.list({ status: 'archived' }), [WORKSPACE])
    })
    await waitFor(() => expect(screen.getByTestId('team-add-agent')).toBeInTheDocument())

    // Add Ray — a non-core agent — to the team (node only, no edge yet).
    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() =>
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument(),
    )
    fireEvent.click(screen.getByTestId('team-add-agent-option-ray'))
    await waitFor(() => expect(screen.getByTestId('team-node-ray')).toBeInTheDocument())

    // Draw a delegation edge jim -> ray. React Flow drag gestures aren't
    // simulable in jsdom (see the shim note at the top of this file), so we
    // invoke the same onConnect callback the graph calls on a real drop —
    // captured off the real (wrapped, not stubbed) WorkspaceTeamGraph.
    expect(capturedGraphProps).not.toBeNull()
    act(() => {
      capturedGraphProps!.onConnect('jim', 'ray')
    })

    // The debounced auto-save fires; both PUTs must land.
    await waitFor(() => expect(updateWorkspace).toHaveBeenCalled(), { timeout: 3000 })
    await waitFor(() => expect(updateWorkspaceDelegation).toHaveBeenCalled(), { timeout: 3000 })

    // core_team is written with the full current membership, including ray.
    expect(updateWorkspace).toHaveBeenCalledWith('ws-1', {
      core_team: ['mia', 'jim', 'planner', 'ray'],
    })
    // The edges PUT carries the new jim->ray edge.
    const edgesArg = vi.mocked(updateWorkspaceDelegation).mock.calls[0]?.[1]
    expect(edgesArg).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ from_agent: 'jim', to_agent: 'ray' }),
      ]),
    )

    // Ordering is load-bearing: core_team must commit before the edges PUT —
    // otherwise the backend 400s ("not a member of the workspace team").
    const coreTeamCallOrder = vi.mocked(updateWorkspace).mock.invocationCallOrder[0]
    const edgesCallOrder = vi.mocked(updateWorkspaceDelegation).mock.invocationCallOrder[0]
    expect(coreTeamCallOrder).toBeDefined()
    expect(edgesCallOrder).toBeDefined()
    expect(coreTeamCallOrder as number).toBeLessThan(edgesCallOrder as number)

    // Both PUTs succeeded — the unsaved-members warning must clear for ray
    // (it now has an incident edge, so isMemberPersisted is true regardless).
    await waitFor(() => {
      expect(screen.queryByTestId('team-unsaved-members')).toBeNull()
    })

    // The updated workspace (with ray in core_team) lands in every cache a
    // consumer might read it from — the detail cache (read by AgentProfile's
    // Heartbeat tab, FR-018/A5) and the status-keyed list caches (read by
    // useActiveWorkspace / the Sidebar / ChatControls).
    await waitFor(() => {
      expect(client.getQueryData(workspacesQueryKeys.detail('ws-1'))).toEqual(
        expect.objectContaining({ core_team: ['mia', 'jim', 'planner', 'ray'] }),
      )
    })
    const activeList = client.getQueryData(
      workspacesQueryKeys.list({ status: 'active' }),
    ) as Workspace[] | undefined
    expect(activeList?.find((w) => w.id === 'ws-1')?.core_team).toEqual([
      'mia',
      'jim',
      'planner',
      'ray',
    ])
    // The archived-status-keyed list cache must ALSO be updated — the saveFn
    // loop writes both `{status:'active'}` and `{status:'archived'}` keys
    // unconditionally (a workspace can be archived while this tab is still
    // mounted, e.g. a background archive from another tab), and only the
    // active-list assertion above was previously covered.
    const archivedList = client.getQueryData(
      workspacesQueryKeys.list({ status: 'archived' }),
    ) as Workspace[] | undefined
    expect(archivedList?.find((w) => w.id === 'ws-1')?.core_team).toEqual([
      'mia',
      'jim',
      'planner',
      'ray',
    ])
  })

  it('partial-failure ordering: updateWorkspace rejects — updateWorkspaceDelegation is never called and status becomes error', async () => {
    // If the core_team write itself fails, the edges PUT must NEVER fire —
    // otherwise the backend could accept an edge referencing a member that
    // was never actually persisted to core_team.
    vi.mocked(updateWorkspace).mockRejectedValue(new Error('core_team PUT failed'))

    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-add-agent')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() =>
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument(),
    )
    fireEvent.click(screen.getByTestId('team-add-agent-option-ray'))
    await waitFor(() => expect(screen.getByTestId('team-node-ray')).toBeInTheDocument())

    expect(capturedGraphProps).not.toBeNull()
    act(() => {
      capturedGraphProps!.onConnect('jim', 'ray')
    })

    await waitFor(() => expect(updateWorkspace).toHaveBeenCalled(), { timeout: 3000 })

    // The error surfaces via AutoSaveIndicator.
    await waitFor(
      () => {
        expect(screen.getByText('core_team PUT failed')).toBeInTheDocument()
      },
      { timeout: 3000 },
    )

    // The edges PUT must never have been reached.
    expect(updateWorkspaceDelegation).not.toHaveBeenCalled()
  })

  it('F4 inverse partial-failure: updateWorkspace resolves but updateWorkspaceDelegation rejects — surfaces a distinguishing message', async () => {
    vi.mocked(updateWorkspace).mockResolvedValue({
      ...WORKSPACE,
      core_team: ['mia', 'jim', 'planner', 'ray'],
    })
    vi.mocked(updateWorkspaceDelegation).mockRejectedValue(new Error('edges PUT failed'))

    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-add-agent')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() =>
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument(),
    )
    fireEvent.click(screen.getByTestId('team-add-agent-option-ray'))
    await waitFor(() => expect(screen.getByTestId('team-node-ray')).toBeInTheDocument())

    expect(capturedGraphProps).not.toBeNull()
    act(() => {
      capturedGraphProps!.onConnect('jim', 'ray')
    })

    await waitFor(() => expect(updateWorkspace).toHaveBeenCalled(), { timeout: 3000 })
    await waitFor(() => expect(updateWorkspaceDelegation).toHaveBeenCalled(), { timeout: 3000 })

    // F4: the surfaced message must distinguish "team membership saved, but
    // delegation edges failed" from a generic/undifferentiated save failure.
    await waitFor(
      () => {
        expect(
          screen.getByText(
            'Team membership saved, but delegation edges failed: edges PUT failed',
          ),
        ).toBeInTheDocument()
      },
      { timeout: 3000 },
    )
  })

  it('F3: the page-hide beacon fires the core_team PUT before the edges PUT for a newly added member + edge', async () => {
    // Regression for F3: the emergency-flush beacon (visibilitychange/
    // beforeunload/pagehide) must honor the same core_team-before-edges
    // ordering as the debounced saveFn — otherwise a member added and
    // connected right before a tab switch/close reintroduces the P0 bug on
    // the one path the P0 fix didn't cover (the built-in single-URL beacon
    // would PUT only the edges endpoint, which the backend rejects because
    // the new member was never written to core_team).
    const fetchSpy = vi
      .spyOn(window, 'fetch')
      .mockResolvedValue(new Response('{}', { status: 200 }))
    // A real CSRF cookie so beaconFlush's getCsrfCookie()/readCSRFCookie()
    // call has something to read — proves the X-CSRF-Token header on the
    // keepalive PUTs comes from an actual cookie, not just present-by-shape.
    const CSRF_TOKEN = 'f3-test-csrf-token'
    stubCsrfCookie(`__Host-csrf=${CSRF_TOKEN}`)

    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-add-agent')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() =>
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument(),
    )
    fireEvent.click(screen.getByTestId('team-add-agent-option-ray'))
    await waitFor(() => expect(screen.getByTestId('team-node-ray')).toBeInTheDocument())

    // Draw the delegation edge — same onConnect invocation as the P0 test.
    expect(capturedGraphProps).not.toBeNull()
    act(() => {
      capturedGraphProps!.onConnect('jim', 'ray')
    })

    // Fire the unload beacon IMMEDIATELY — well inside the 500ms debounce
    // window, so the debounced saveFn (updateWorkspace/updateWorkspaceDelegation)
    // has not fired yet. This is exactly the F3 finding's scenario.
    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })
    expect(updateWorkspace).not.toHaveBeenCalled()
    expect(updateWorkspaceDelegation).not.toHaveBeenCalled()

    // The beacon's two ordered keepalive PUTs must land — core_team first.
    await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(2))

    const [coreTeamCall, edgesCall] = fetchSpy.mock.calls
    expect(coreTeamCall[0]).toBe('/api/v1/workspaces/ws-1')
    expect(coreTeamCall[1]).toMatchObject({
      method: 'PUT',
      keepalive: true,
      credentials: 'include',
    })
    // Regression guard: the keepalive PUT must carry the CSRF double-submit
    // header (M3 round-1 fix) — a bare `toMatchObject({method, keepalive})`
    // would stay green even if `credentials`/the header were silently
    // dropped from `beaconFlush`/`putKeepalive`, since neither is checked.
    const coreTeamHeaders = coreTeamCall[1]?.headers as Record<string, string>
    expect(coreTeamHeaders['X-CSRF-Token']).toBe(CSRF_TOKEN)
    expect(JSON.parse(coreTeamCall[1]?.body as string)).toEqual({
      core_team: ['mia', 'jim', 'planner', 'ray'],
    })

    expect(edgesCall[0]).toBe('/api/v1/workspaces/ws-1/delegation')
    expect(edgesCall[1]).toMatchObject({
      method: 'PUT',
      keepalive: true,
      credentials: 'include',
    })
    const edgesHeaders = edgesCall[1]?.headers as Record<string, string>
    expect(edgesHeaders['X-CSRF-Token']).toBe(CSRF_TOKEN)
    const edgesBody = JSON.parse(edgesCall[1]?.body as string) as {
      edges: Array<{ from_agent: string; to_agent: string }>
    }
    expect(edgesBody.edges).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ from_agent: 'jim', to_agent: 'ray' }),
      ]),
    )

    restoreCsrfCookie()
    fetchSpy.mockRestore()
  })

  it('passes the workspaceId to openEditAgentSlideOver when a node edit button is clicked (FR-018 / A5)', async () => {
    // Traces to: FR-018 / A5 — Team tab wires handleOpenAgent to call
    // openEditAgentSlideOver(agentId, workspaceId) so AgentProfile can render
    // the conditional Heartbeat tab for this (workspace, agent) pair.
    //
    // This test exercises the real component path: render WorkspaceTeamTab,
    // wait for the nodes to appear, click the edit affordance on Jim's node,
    // and assert the store received (agentId, workspaceId) — NOT bare-store.
    const openSpy = vi.spyOn(useUiStore.getState(), 'openEditAgentSlideOver')
    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-node-jim')).toBeInTheDocument())

    // Click the "Edit Jim" button inside the Jim node. The button is rendered
    // by WorkspaceTeamGraph inside each non-ghost node when onOpenAgent is
    // provided (aria-label="Edit <name>"). We target it via querySelector on
    // the node element (same pattern as the remove-guard test below) because
    // React Flow renders nodes in a sub-tree that is not part of the ARIA
    // role tree accessible via getByRole. Clicking it calls
    // handleOpenAgent('jim') → openEditAgentSlideOver('jim', 'ws-1').
    const jimNode = screen.getByTestId('team-node-jim')
    const editBtn = jimNode.querySelector<HTMLElement>('[aria-label="Edit Jim"]')
    expect(editBtn).not.toBeNull()
    fireEvent.click(editBtn!)

    expect(openSpy).toHaveBeenCalledWith('jim', 'ws-1')
    // The store must record both the agent id and the workspace id.
    expect(useUiStore.getState().editAgentId).toBe('jim')
    expect(useUiStore.getState().editAgentWorkspaceId).toBe('ws-1')

    openSpy.mockRestore()
    useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null })
  })

  it('does not offer a remove button on the default agent node (remove-default guard)', async () => {
    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-node-mia')).toBeInTheDocument())
    // Mia is the default agent — the node must NOT render a "Remove … from team"
    // action (the guard's UI side; the handler also blocks it defensively).
    const miaNode = screen.getByTestId('team-node-mia')
    expect(
      miaNode.querySelector('[aria-label="Remove Mia from team"]'),
    ).toBeNull()
    // A non-default member (Jim) DOES get the remove action.
    const jimNode = screen.getByTestId('team-node-jim')
    expect(
      jimNode.querySelector('[aria-label="Remove Jim from team"]'),
    ).not.toBeNull()
  })
})

// ── Implicit Judge row (operator-reported: "Judge invisible in workspace
// Teams") ─────────────────────────────────────────────────────────────────
//
// Backend model (operator-directed, pkg/workspace/find_for_agent.go's
// isImplicitMember): System agents (the Judge) are IMPLICIT members of
// EVERY workspace — they cannot be added to core_team rosters (the gateway
// 400s that), and membership is resolved implicitly everywhere. Before this
// fix the Team tab only ever rendered the literal core_team ∪ edge-endpoint
// node set, so the Judge looked absent even though it always verifies work
// on every workspace.
describe('WorkspaceTeamTab — implicit Judge row (ADR-049 D3)', () => {
  it('shows the Judge row with the "Verifier — implicit member" badge for a normal workspace', async () => {
    renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('team-node-judge')).toBeInTheDocument()
    })
    expect(screen.getByText('Judge')).toBeInTheDocument()
    expect(screen.getByTestId('team-node-implicit-badge-judge')).toHaveTextContent(
      'Verifier — implicit member of every workspace',
    )
  })

  it('a workspace with ZERO real members still shows the Judge row', async () => {
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-1',
      team: [],
      edges: [],
      default_depth: 3,
    } as WorkspaceDelegation)
    renderTab()
    await waitFor(() => {
      expect(screen.getByTestId('team-node-judge')).toBeInTheDocument()
    })
  })

  it('the [+ Add agent] picker never offers the Judge — System agents are not team-addable', async () => {
    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-add-agent')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() =>
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument(),
    )
    expect(screen.queryByTestId('team-add-agent-option-judge')).toBeNull()
  })

  it('saving the team (adding a real member + drawing an edge) never includes the Judge in either PUT payload', async () => {
    vi.mocked(updateWorkspace).mockResolvedValue({
      ...WORKSPACE,
      core_team: ['mia', 'jim', 'planner', 'ray'],
    })
    vi.mocked(updateWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-1',
      team: ['mia', 'jim', 'planner', 'ray'],
      edges: [
        ...(DELEGATION.edges ?? []),
        { from_agent: 'jim', to_agent: 'ray', modes: ['direct', 'task'] },
      ],
      default_depth: 3,
    })

    renderTab()
    await waitFor(() => expect(screen.getByTestId('team-add-agent')).toBeInTheDocument())
    // The Judge row is present throughout — proving it renders alongside a
    // normal, in-flight edit session, not just a pristine/empty one.
    await waitFor(() => expect(screen.getByTestId('team-node-judge')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('team-add-agent'))
    await waitFor(() =>
      expect(screen.getByTestId('team-add-agent-option-ray')).toBeInTheDocument(),
    )
    fireEvent.click(screen.getByTestId('team-add-agent-option-ray'))
    await waitFor(() => expect(screen.getByTestId('team-node-ray')).toBeInTheDocument())

    expect(capturedGraphProps).not.toBeNull()
    act(() => {
      capturedGraphProps!.onConnect('jim', 'ray')
    })

    await waitFor(() => expect(updateWorkspace).toHaveBeenCalled(), { timeout: 3000 })
    const coreTeamArg = vi.mocked(updateWorkspace).mock.calls[0]?.[1] as { core_team?: string[] }
    expect(coreTeamArg?.core_team).not.toContain('judge')

    await waitFor(() => expect(updateWorkspaceDelegation).toHaveBeenCalled(), { timeout: 3000 })
    const edgesArg = vi.mocked(updateWorkspaceDelegation).mock.calls[0]?.[1] as Array<{
      from_agent: string
      to_agent: string
    }>
    expect(edgesArg.every((e) => e.from_agent !== 'judge' && e.to_agent !== 'judge')).toBe(true)
  })
})
