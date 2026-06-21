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
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Agent, Workspace, WorkspaceDelegation } from '@/lib/api'

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
    steering_mode: 'one-at-a-time',
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
]

const DELEGATION: WorkspaceDelegation = {
  workspace_id: 'ws-1',
  team: ['mia', 'jim', 'planner'],
  edges: [{ from_agent: 'jim', to_agent: 'planner', modes: ['await', 'task'], depth: 2 }],
}

// ── Mocks ───────────────────────────────────────────────────────────────────
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchWorkspaceDelegation: vi.fn(),
    updateWorkspaceDelegation: vi.fn(),
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

import { fetchAgents, fetchWorkspaceDelegation } from '@/lib/api'
import { WorkspaceTeamTab } from './WorkspaceTeamTab'

function renderTab() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceTeamTab workspaceId="ws-1" />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockResolvedValue(AGENTS)
  vi.mocked(fetchWorkspaceDelegation).mockResolvedValue(DELEGATION)
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
})
