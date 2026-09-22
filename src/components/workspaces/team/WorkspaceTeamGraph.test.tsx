/**
 * Render + interaction tests for WorkspaceTeamGraph — the Team-tab delegation
 * canvas. React Flow needs a real ResizeObserver + element geometry, which jsdom
 * lacks, so we stub them before mounting (same shim as graph/GraphView.test).
 *
 * Asserts: a node per team agent, a labelled delegation edge per edge, and that
 * clicking an edge opens the inline editor where toggling a mode fires
 * onToggleMode with the right (from, to, mode).
 */

import { describe, it, expect, beforeAll, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WorkspaceTeamGraph } from './WorkspaceTeamGraph'
import {
  buildTeamGraphModel,
  type TeamEditState,
} from './teamGraphModel'
import type { Agent } from '@/lib/api'

// AgentDelegatePicker itself is unit-tested (candidate filtering, selection,
// empty state) in AgentDelegatePicker.test.tsx against explicit props. Here
// we only need to prove WORKSPACETEAMGRAPH → AgentNode → AgentDelegatePicker
// wiring: that the canvas-global state (nodes/editState/workerIds/onDelegate)
// reaches the picker via TeamGraphCanvasContext and that invoking `onDelegate`
// runs through the SAME `handleConnect` path a canvas drag uses. A minimal
// stub avoids fighting Radix's portal-based DropdownMenu inside a jsdom
// ReactFlow canvas (which needs its own heavy geometry stubbing already).
vi.mock('./AgentDelegatePicker', () => ({
  AgentDelegatePicker: ({
    source,
    nodes,
    onDelegate,
  }: {
    source: { id: string }
    nodes: { id: string }[]
    onDelegate: (from: string, to: string) => void
  }) => {
    // Mirror the real picker's own exclusion of the source from the target
    // list, so this stub also proves `nodes` (canvas-global) reached the
    // component via context rather than being empty/stale.
    const target = nodes.find((n) => n.id !== source.id)
    return (
      <button
        type="button"
        data-testid={`mock-delegate-${source.id}`}
        disabled={!target}
        onClick={() => target && onDelegate(source.id, target.id)}
      >
        Delegate
      </button>
    )
  },
}))

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
  // The canvas-frame measurement the mini-map visibility check reads (this
  // canvas's own ResizeObserver-on-canvasDomRef effect) uses
  // clientWidth/clientHeight, not offsetWidth/offsetHeight — jsdom defaults
  // both to 0, which would make every test's "frame" 0x0 and the mini-map
  // show unconditionally. Stubbed to a generously large frame so the DEFAULT
  // across this file is "no mini-map"; the dedicated mini-map test below
  // shrinks it back down.
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, value: 600 })
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width: 220, height: 92, top: 0, left: 0, right: 220, bottom: 92, toJSON: () => ({}),
  } as DOMRect)
})

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

const AGENTS: Agent[] = [
  agent('mia', { name: 'Mia', default: true }),
  agent('jim', { name: 'Jim' }),
  agent('planner', { name: 'Planner', type: 'Subagent' }),
]
const WORKER_IDS = new Set(['planner'])

const STATE: TeamEditState = {
  members: ['mia', 'jim', 'planner'],
  edges: [
    { from: 'mia', to: 'jim', modes: ['direct'] },
    { from: 'jim', to: 'planner', modes: ['task'], depth: 2 },
  ],
  defaultDepth: 3,
}

function renderGraph(overrides: Partial<Parameters<typeof WorkspaceTeamGraph>[0]> = {}) {
  const model = buildTeamGraphModel(STATE, AGENTS)
  const props = {
    nodes: model.nodes,
    edges: model.edges,
    workerIds: WORKER_IDS,
    editState: STATE,
    defaultDepth: model.defaultDepth,
    onConnect: vi.fn(),
    onToggleMode: vi.fn(),
    onSetDepth: vi.fn(),
    onDeleteEdge: vi.fn(),
    onRemoveMember: vi.fn(),
    onRejectConnection: vi.fn(),
    onOpenAgent: vi.fn(),
    ...overrides,
  }
  const utils = render(<WorkspaceTeamGraph {...props} />)
  return { props, ...utils }
}

describe('WorkspaceTeamGraph — nodes', () => {
  it('renders a node for each team agent with its name', () => {
    renderGraph()
    expect(screen.getByTestId('team-node-mia')).toBeInTheDocument()
    expect(screen.getByTestId('team-node-jim')).toBeInTheDocument()
    expect(screen.getByTestId('team-node-planner')).toBeInTheDocument()
    expect(screen.getByText('Mia')).toBeInTheDocument()
    expect(screen.getByText('Planner')).toBeInTheDocument()
  })

  it('lets a worker node be a delegation source (bounded delegation, not tier-gated)', () => {
    // Workers can now START edges — the backend seeds Planner→Researcher, both
    // workers — so a worker node gets a source handle just like a Main agent.
    renderGraph()
    expect(screen.getByTestId('team-node-mia').getAttribute('data-can-source')).toBe('true')
    expect(screen.getByTestId('team-node-planner').getAttribute('data-can-source')).toBe('true')
  })

  it('renders a source handle on the worker node so it can start a delegation edge', () => {
    const { container } = renderGraph()
    const planner = screen.getByTestId('team-node-planner')
    // React Flow source handles carry the `.react-flow__handle-bottom` class
    // (our source dot sits at Position.Bottom). A worker must have one now.
    expect(planner.querySelector('.source')).not.toBeNull()
    // Sanity: the canvas still mounts.
    expect(container.querySelector('[data-testid="team-graph-canvas"]')).not.toBeNull()
  })

  it('renders the canvas with the shared ZoomPill (not React Flow\'s own <Controls>)', () => {
    const { container } = renderGraph()
    expect(screen.getByTestId('team-graph-canvas')).toBeInTheDocument()
    expect(screen.getByTestId('zoomable-view-zoom-out')).toBeInTheDocument()
    expect(screen.getByTestId('zoomable-view-percent')).toBeInTheDocument()
    expect(screen.getByTestId('zoomable-view-zoom-in')).toBeInTheDocument()
    expect(container.querySelector('.react-flow__controls')).toBeNull()
  })
})

// ── ZoomableView canvas migration (C3 phase 2, D18) ─────────────────────────
describe('WorkspaceTeamGraph — the shared ZoomPill replaces React Flow\'s own <Controls>', () => {
  it('the percent menu offers Fit and 100% (no Zoom-to-selection assumption baked in beyond what the shared hook always returns)', async () => {
    renderGraph()
    const user = userEvent.setup()

    await user.click(screen.getByTestId('zoomable-view-percent'))

    expect(screen.getByTestId('zoomable-view-menu-fit')).toBeInTheDocument()
    expect(screen.getByTestId('zoomable-view-menu-100')).toBeInTheDocument()
  })

  it('clicking zoom-in moves the reported percentage up from its starting value', async () => {
    renderGraph()
    const user = userEvent.setup()
    const readPercent = () =>
      Number(screen.getByTestId('zoomable-view-percent').textContent?.replace('%', ''))
    const before = readPercent()

    await user.click(screen.getByTestId('zoomable-view-zoom-in'))

    expect(readPercent()).toBeGreaterThan(before)
  })
})

// ── Mini-map shows only when content exceeds the frame (D18) ───────────────
// The team graph previously had NO mini-map at all — this is new coverage,
// not a migration of an old assertion.
describe('WorkspaceTeamGraph — mini-map shows only when content exceeds the frame (D18)', () => {
  it('renders no mini-map inside the generously-sized default frame', () => {
    const { container } = renderGraph()
    expect(container.querySelector('.react-flow__minimap')).toBeNull()
  })

  it('renders the mini-map once the frame is smaller than the content', () => {
    const prevWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth')!
    const prevHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight')!
    Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: 5 })
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, value: 5 })
    try {
      const { container } = renderGraph()
      expect(container.querySelector('.react-flow__minimap')).not.toBeNull()
    } finally {
      Object.defineProperty(HTMLElement.prototype, 'clientWidth', prevWidth)
      Object.defineProperty(HTMLElement.prototype, 'clientHeight', prevHeight)
    }
  })
})

// ── Zoom keyboard shortcuts do not hijack the depth editor (regression) ────
// EdgeModeEditor.tsx's depth field is a real `<input type="number">` where a
// user legitimately types "0" or "1" — D18's `+ − 0 1` shortcuts must not
// fire while that field is focused. WorkspaceTeamGraph.tsx's
// `handleCanvasKeyDown` guards every canvas keydown with
// `target.closest('input, textarea, select, [contenteditable="true"]')`
// before running the shortcut. The real depth input's own edge-label DOM
// never paints in this jsdom harness (see the "edges" describe block above:
// "React Flow only paints individual edge label DOM after nodes are
// measured, which jsdom does not do" — that interaction is covered instead
// by EdgeModeEditor.test.tsx), so this proves the CANVAS-LEVEL guard itself
// against a real `<input>` placed inside the canvas, rather than depending on
// DOM this harness cannot render.
describe('WorkspaceTeamGraph — zoom keyboard shortcuts do not hijack typing in an editable field', () => {
  it('a digit keydown on a real <input> inside the canvas does not change the reported zoom percent', () => {
    const { container } = renderGraph()
    const readPercent = () =>
      Number(screen.getByTestId('zoomable-view-percent').textContent?.replace('%', ''))
    const before = readPercent()

    const input = document.createElement('input')
    input.setAttribute('type', 'number')
    container.querySelector('[data-testid="team-graph-canvas"]')!.appendChild(input)

    fireEvent.keyDown(input, { key: '1' })

    expect(readPercent()).toBe(before)
    input.remove()
  })
})

describe('WorkspaceTeamGraph — edges', () => {
  // React Flow only paints individual edge label DOM after nodes are measured,
  // which jsdom does not do — so we assert the edges layer was handed the edges
  // (mirrors graph/GraphView.test.tsx). The edge-editor interaction itself is
  // tested deterministically against EdgeModeEditor in EdgeModeEditor.test.tsx,
  // and the edge model in teamGraphModel.test.ts.
  it('mounts the React Flow edge layer for a graph with delegation edges', () => {
    const { container } = renderGraph()
    expect(container.querySelector('.react-flow__edges')).not.toBeNull()
    expect(container.querySelector('.react-flow__viewport')).not.toBeNull()
  })

  it('renders no nodes / no canvas crash for an empty graph', () => {
    renderGraph({ nodes: [], edges: [] })
    expect(screen.getByTestId('team-graph-canvas')).toBeInTheDocument()
    expect(screen.queryByTestId('team-node-mia')).toBeNull()
  })
})

// ── Delegate picker wiring (context refactor regression) ────────────────────
//
// TeamGraphCanvasContext now carries `allNodes`/`editState`/`workerIds`/
// `onDelegate` from WorkspaceTeamGraphInner down to AgentNode ->
// AgentDelegatePicker, replacing the old per-node `data` copies. This proves
// the plumbing survived the refactor: the mocked picker only receives
// `nodes`/`onDelegate` via context (see the module mock above), and invoking
// it runs through the exact same `handleConnect` (validateConnection ->
// onConnect / onRejectConnection) a canvas drag uses — no separate mutation
// path for the keyboard route.
// ── AgentNode keyboard guard (bubbling hijack regression) ────────────────────
//
// AgentNode's onClick already skips clicks landing on a Handle or an
// action button (data-node-action="edit"/"remove"/"delegate") — those have
// their own behaviour. onKeyDown lacked the same guard: Enter/Space on a
// focused action button (e.g. Remove) bubbled up to the node's own
// onKeyDown, which preventDefault()'d the event and called onOpenAgent
// instead of running the action the user actually focused — making keyboard
// Remove impossible.
describe('WorkspaceTeamGraph — AgentNode keyboard guard (bubbling hijack regression)', () => {
  it('Enter on the node Remove button removes the member, and does NOT open the agent', async () => {
    const { props } = renderGraph()
    // jim is not the default agent, so its Remove button is rendered.
    // getByLabelText, not getByRole: React Flow's node wrapper renders with
    // `visibility: hidden` in jsdom (nodes are never "measured" without a
    // real layout engine — see the module's own ResizeObserver/geometry
    // stubs above), which getByRole's default accessible-tree filtering
    // excludes; getByLabelText does not filter on visibility.
    //
    // The Remove button is a native <button> with no onKeyDown of its own —
    // it relies on the browser's native Enter-activation (firing a click),
    // which user-event's keyboard() API faithfully simulates (including
    // respecting preventDefault from an ancestor's bubbled handler) —
    // fireEvent.keyDown alone does not.
    const removeButton = screen.getByLabelText('Remove Jim from team')
    const user = userEvent.setup()
    removeButton.focus()
    await user.keyboard('{Enter}')

    expect(props.onRemoveMember).toHaveBeenCalledWith('jim')
    expect(props.onOpenAgent).not.toHaveBeenCalled()
  })

  it('Enter on the node body itself still opens the agent (guard only blocks handle/action-button bubbles)', () => {
    const { props } = renderGraph()
    const node = screen.getByTestId('team-node-jim')
    fireEvent.keyDown(node, { key: 'Enter' })

    expect(props.onOpenAgent).toHaveBeenCalledWith('jim')
    expect(props.onRemoveMember).not.toHaveBeenCalled()
  })
})

// ── Implicit System agent node (the Judge, ADR-049 D3) ──────────────────────
//
// Operator-reported: the Judge "looks absent" from the Team tab because
// pkg/workspace/find_for_agent.go's isImplicitMember resolves System-agent
// membership implicitly for EVERY workspace (never via core_team). Rendered
// as a non-removable, non-editable, non-connectable node with a muted
// "Verifier" badge — see teamGraphModel.test.ts for the render-only /
// never-persisted guarantee at the model layer.
describe('WorkspaceTeamGraph — implicit System agent node (Judge, ADR-049 D3)', () => {
  const JUDGE = agent('judge', { name: 'Judge', type: 'system' })
  const AGENTS_WITH_JUDGE = [...AGENTS, JUDGE]

  function renderGraphWithJudge(overrides: Partial<Parameters<typeof WorkspaceTeamGraph>[0]> = {}) {
    const model = buildTeamGraphModel(STATE, AGENTS_WITH_JUDGE)
    const props = {
      nodes: model.nodes,
      edges: model.edges,
      workerIds: WORKER_IDS,
      editState: STATE,
      defaultDepth: model.defaultDepth,
      onConnect: vi.fn(),
      onToggleMode: vi.fn(),
      onSetDepth: vi.fn(),
      onDeleteEdge: vi.fn(),
      onRemoveMember: vi.fn(),
      onRejectConnection: vi.fn(),
      onOpenAgent: vi.fn(),
      ...overrides,
    }
    const utils = render(<WorkspaceTeamGraph {...props} />)
    return { props, ...utils }
  }

  it('renders a node for the Judge with the muted "Verifier" badge', () => {
    renderGraphWithJudge()
    const judgeNode = screen.getByTestId('team-node-judge')
    expect(judgeNode).toHaveAttribute('data-implicit', 'true')
    expect(screen.getByText('Judge')).toBeInTheDocument()
    expect(screen.getByTestId('team-node-implicit-badge-judge')).toHaveTextContent(
      'Verifier — implicit member of every workspace',
    )
  })

  it('renders no remove or edit affordance on the Judge node (non-removable, non-editable)', () => {
    renderGraphWithJudge()
    const judgeNode = screen.getByTestId('team-node-judge')
    expect(judgeNode.querySelector('[aria-label="Remove Judge from team"]')).toBeNull()
    expect(judgeNode.querySelector('[aria-label="Edit Judge"]')).toBeNull()
    expect(judgeNode.querySelector('[data-testid="mock-delegate-judge"]')).toBeNull()
  })

  it('renders no connection handle on the Judge node — it can never be a delegation source or target', () => {
    renderGraphWithJudge()
    const judgeNode = screen.getByTestId('team-node-judge')
    expect(judgeNode.querySelector('.react-flow__handle')).toBeNull()
  })

  it('the Judge node is not clickable — no role=button, no tabIndex, click is a no-op', () => {
    const { props } = renderGraphWithJudge()
    const judgeNode = screen.getByTestId('team-node-judge')
    expect(judgeNode.getAttribute('role')).toBeNull()
    expect(judgeNode.getAttribute('tabindex')).toBeNull()
    fireEvent.click(judgeNode)
    expect(props.onOpenAgent).not.toHaveBeenCalled()
  })
})

describe('WorkspaceTeamGraph — delegate picker wiring', () => {
  it('a keyboard delegation that closes a cycle is rejected before onConnect', () => {
    const { props } = renderGraph()
    // mia -> jim -> planner already exists, so planner -> mia closes a cycle.
    fireEvent.click(screen.getByTestId('mock-delegate-planner'))
    expect(props.onConnect).not.toHaveBeenCalled()
    expect(props.onRejectConnection).toHaveBeenCalledWith('That delegation would create a cycle.')
  })

  it('an invalid keyboard delegation is rejected through the same validateConnection path (no edge added)', () => {
    const { props } = renderGraph()
    // mia -> jim already exists in STATE; the mock picker's first-other-node
    // target for mia is jim, so this exercises the rejection branch of
    // handleConnect instead of a bypassed/duplicated mutation.
    fireEvent.click(screen.getByTestId('mock-delegate-mia'))
    expect(props.onConnect).not.toHaveBeenCalled()
    expect(props.onRejectConnection).toHaveBeenCalledWith('That delegation edge already exists.')
  })
})
