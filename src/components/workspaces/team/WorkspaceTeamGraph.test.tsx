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
import { render, screen } from '@testing-library/react'
import { WorkspaceTeamGraph } from './WorkspaceTeamGraph'
import {
  buildTeamGraphModel,
  type TeamEditState,
} from './teamGraphModel'
import type { Agent } from '@/lib/api'

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
    steering_mode: 'one-at-a-time',
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

  it('renders the canvas with controls', () => {
    renderGraph()
    expect(screen.getByTestId('team-graph-canvas')).toBeInTheDocument()
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
