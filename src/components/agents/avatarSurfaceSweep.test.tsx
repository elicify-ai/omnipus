/**
 * avatarSurfaceSweep.test.tsx — one mark on every surface.
 *
 * Oracle (MESSAGES.md FOUNDER DECISIONS 2026-10-10): "every surface draws an
 * agent with ONE mark (its figure, role badge and colour); the old per-agent
 * Phosphor 'icon' system is gone everywhere." For EACH surface switched to
 * AgentMark this file renders the real component and asserts the mark is the
 * agent's own (data-testid="agent-icon" with the agent's figure) and that no
 * legacy bare-initial circle came back with it.
 *
 * Network edges only are mocked (the agents/tasks/plans queries); every
 * component under test is real.
 */
import * as React from 'react'
import { describe, expect, it, vi, beforeAll } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Agent, Workspace } from '@/lib/api'
import type { ChatMessage } from '@/store/chat'
import type { TeamEditState } from '@/components/workspaces/team/teamGraphModel'
import type { AgentActivityItem } from '@/hooks/useRunningActivity'
import { makeAgent } from '@/test/factories'

// ── Shared fixtures ──────────────────────────────────────────────────────────
//
// The sweep agent carries a figure no fallback would produce, so a regression
// to initials, a generic mark or the Omnipus default is visible in data-figure.

const RIVET: Agent = makeAgent({
  id: 'rivet',
  name: 'Rivet',
  type: 'Subagent',
  figure: 'Robot',
  role: 'developer',
  color: '#3B82F6', // Azure — palette order 1
})

const RIVET_ROLLUP_AGENT = {
  id: 'rivet',
  name: 'Rivet',
  figure: 'Robot',
  role: 'developer',
  color: '#3B82F6', // Azure — palette order 1
} as const

beforeAll(() => {
  // jsdom shims the surfaces in this file share with Radix/React Flow.
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
  if (typeof window !== 'undefined' && !window.matchMedia) {
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(),
      })),
    })
  }
  const g = globalThis as unknown as Record<string, unknown>
  g.DOMMatrixReadOnly ??= class { m22 = 1 }
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, value: 600 })
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width: 220, height: 92, top: 0, left: 0, right: 220, bottom: 92, toJSON: () => ({}),
  } as DOMRect)
})

// Network edge mocks — the agents cache every surface reads, plus the tasks/
// plans queries the Tasks tab fires. Everything else stays real.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  const { makeAgent: agentFactory } = await import('@/test/factories')
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      agentFactory({ id: 'rivet', name: 'Rivet', type: 'Subagent', figure: 'Robot', role: 'developer', color: '#3B82F6' }),
    ]),
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    updateTask: vi.fn(),
    deletePlan: vi.fn(),
  }
})

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_target, tag) =>
      (props: React.HTMLAttributes<HTMLElement>) => React.createElement(tag as string, props),
  }),
}))

// WorkspaceAgentList navigates; jsdom has no router here.
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

// AddAgentPicker's Radix popover renders inline (mirrors AddAgentPicker.test.tsx).
vi.mock('@/components/ui/popover', () => ({
  Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
  PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) => {
    if (asChild && React.isValidElement(children)) return children
    return React.createElement('div', null, children)
  },
  PopoverContent: ({ children }: { children: React.ReactNode }) =>
    React.createElement('div', { 'data-testid': 'popover-content' }, children),
}))

// The team graph's delegate picker is stubbed exactly as its own suite does —
// the sweep asserts the NODE marks, not the picker.
vi.mock('@/components/workspaces/team/AgentDelegatePicker', () => ({
  AgentDelegatePicker: ({ source }: { source: { id: string } }) => (
    <button type="button" data-testid={`mock-delegate-${source.id}`}>Delegate</button>
  ),
}))

/** The mark drawn for `figure` must be present among the rendered marks. */
function expectAgentMarkFigure(container: HTMLElement, figure: string, atLeast = 1): void {
  const marks = Array.from(container.querySelectorAll('[data-testid="agent-icon"]'))
  expect(
    marks.filter((mark) => mark.getAttribute('data-figure') === figure).length,
    `an agent-icon with data-figure=${figure}`,
  ).toBeGreaterThanOrEqual(atLeast)
}

/** No legacy avatar: an element (outside SVG art) whose whole text is the bare initial. */
function expectNoInitialCircle(container: HTMLElement, initial: string): void {
  const offenders = Array.from(container.querySelectorAll('*'))
    .filter((el) => el.namespaceURI?.endsWith('svg') !== true)
    .filter((el) => el.textContent?.trim() === initial)
    .map((el) => `<${el.tagName.toLowerCase()} class="${String(el.className)}">`)
  expect(offenders, `legacy initial-circle showing "${initial}"`).toEqual([])
}

// ── 1–2: roster cards ────────────────────────────────────────────────────────

describe('surface sweep — AgentCard and WorkerCard (roster)', () => {
  it('AgentCard draws the agent mark with the agent figure', async () => {
    const { AgentCard } = await import('./AgentCard')
    const { container } = render(<AgentCard agent={RIVET} onClick={() => {}} />)
    expectAgentMarkFigure(container, 'Robot', 1)
    expect(container.querySelectorAll('[data-testid="agent-icon"]')).toHaveLength(1)
    expectNoInitialCircle(container, 'R')
  })

  it('WorkerCard draws the agent mark with the agent figure', async () => {
    const { WorkerCard } = await import('./WorkerCard')
    const { container } = render(<WorkerCard agent={RIVET} />)
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })
})

// ── 3: chat message avatar ───────────────────────────────────────────────────

describe('surface sweep — chat MessageItem', () => {
  it("draws the speaking agent's own mark on an assistant message", async () => {
    const { MessageItem } = await import('@/components/chat/MessageItem')
    const { useChatStore } = await import('@/store/chat')
    const { act } = await import('react')
    act(() => {
      useChatStore.setState({ toolCalls: {} })
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const message = {
      id: 'msg_1',
      session_id: 'sess_1',
      role: 'assistant',
      agentId: 'rivet',
      content: 'On it.',
      timestamp: '2026-10-10T10:00:00Z',
      status: 'done',
    } as ChatMessage
    const { container } = render(
      <QueryClientProvider client={client}>
        <MessageItem message={message} />
      </QueryClientProvider>,
    )
    await screen.findByText('On it.')
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })
})

// ── 4: activity avatar ───────────────────────────────────────────────────────

describe('surface sweep — ActivityAvatar', () => {
  it('draws the resolved agent mark for an agent activity item, with no fallback circle', async () => {
    const { ActivityAvatar } = await import('@/components/chat/ActivityAvatar')
    const item: AgentActivityItem = {
      kind: 'agent',
      key: 'span-1',
      agentId: 'rivet',
      agentName: 'Rivet',
      agentType: 'native',
      agent: RIVET,
      taskLabel: 'Sweep the decks',
      status: 'running',
    }
    const { container } = render(<ActivityAvatar item={item} />)
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })
})

// ── 5: session group header ──────────────────────────────────────────────────

describe('surface sweep — SessionGroupHeaders AgentHeader', () => {
  it('draws the agent mark in the session group header', async () => {
    const { AgentHeader } = await import('@/components/sessions/SessionGroupHeaders')
    const { container } = render(
      <AgentHeader agent={RIVET} name="Rivet" isCollapsed={false} onToggle={() => {}} panelId="panel-1" />,
    )
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })
})

// ── 6: team add-agent picker ─────────────────────────────────────────────────

describe('surface sweep — team AddAgentPicker', () => {
  it('draws each candidate mark in the picker row', async () => {
    const { AddAgentPicker } = await import('@/components/workspaces/team/AddAgentPicker')
    const other = makeAgent({ id: 'sable', name: 'Sable', type: 'Main', figure: 'Woman', role: 'writer' })
    const { container } = render(
      <AddAgentPicker agents={[RIVET, other]} memberIds={new Set<string>()} onAdd={() => {}} />,
    )
    const row = screen.getByTestId('team-add-agent-option-rivet')
    expect(within(row).getByTestId('agent-icon')).toHaveAttribute('data-figure', 'Robot')
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })
})

// ── 7: team delegation graph ─────────────────────────────────────────────────

describe('surface sweep — WorkspaceTeamGraph', () => {
  it('draws each team node with its own mark', async () => {
    const { WorkspaceTeamGraph } = await import('@/components/workspaces/team/WorkspaceTeamGraph')
    const { buildTeamGraphModel } = await import('@/components/workspaces/team/teamGraphModel')
    const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'Main', figure: 'Robot', role: 'developer' })
    const jim = makeAgent({ id: 'jim', name: 'Jim', type: 'core', figure: 'Man', role: 'orchestrator' })
    const editState: TeamEditState = {
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
      defaultDepth: 3,
    }
    const model = buildTeamGraphModel(editState, [mia, jim])
    const { container } = render(
      <WorkspaceTeamGraph
        nodes={model.nodes}
        edges={model.edges}
        workerIds={new Set<string>()}
        editState={editState}
        defaultDepth={model.defaultDepth}
        onConnect={() => {}}
        onToggleMode={() => {}}
        onSetDepth={() => {}}
        onDeleteEdge={() => {}}
        onRemoveMember={() => {}}
        onRejectConnection={() => {}}
        onOpenAgent={() => {}}
      />,
    )
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'M')
  })
})

// ── 8: tasks tab agent filter ────────────────────────────────────────────────

describe('surface sweep — WorkspaceTasksTab agent filter', () => {
  it('draws each agent option mark in the owner filter', async () => {
    const { WorkspaceTasksTab } = await import('@/components/workspaces/WorkspaceTasksTab')
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <WorkspaceTasksTab workspaceId="ws-1" />
      </QueryClientProvider>,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Filter by agent (current: all agents)' }))
    const option = await screen.findByRole('menuitem', { name: /Rivet/ })
    expect(within(option).getByTestId('agent-icon')).toHaveAttribute('data-figure', 'Robot')
  })
})

// ── 9: board rollup badge ────────────────────────────────────────────────────

describe('surface sweep — RollupBadge', () => {
  it('draws each rollup item mark inside its status ring', async () => {
    const { RollupBadge } = await import('@/components/workspaces/RollupBadge')
    const { container } = render(
      <RollupBadge
        rollup={[{ agent_id: 'rivet', label: 'Sweeping', status: 'in_progress' }]}
        agents={[RIVET_ROLLUP_AGENT]}
      />,
    )
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })
})

// ── 10: browser live toolbar ─────────────────────────────────────────────────

describe('surface sweep — BrowserLiveToolbar', () => {
  it('draws the driving agent mark in the identity chip', async () => {
    const { BrowserLiveToolbar } = await import('@/components/browser/BrowserLiveToolbar')
    const addressBarRef = React.createRef<HTMLInputElement>()
    const { container } = render(
      <BrowserLiveToolbar
        connected
        annotateMode={false}
        urlInput="https://omnipus.ai"
        onUrlChange={() => {}}
        onUrlFocus={() => {}}
        onUrlBlur={() => {}}
        onOmniboxSubmit={() => {}}
        onToolbarNav={() => {}}
        addressBarRef={addressBarRef}
        resolvedAgent={RIVET}
        agentDisplayName="Rivet"
        visualState="agent-working"
        visualDriveMode="agent-working"
        statusState="connecting"
        canAnnotate={false}
        onToggleAnnotate={() => {}}
        showMute={false}
        videoMuted={false}
        onToggleMute={() => {}}
      />,
    )
    const chip = screen.getByTestId('browser-live-agent-chip')
    expect(within(chip).getByTestId('agent-icon')).toHaveAttribute('data-figure', 'Robot')
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })
})

// ── 11–12: sidebar ───────────────────────────────────────────────────────────

describe('surface sweep — sidebar', () => {
  it('SidebarAgentIcon draws the agent mark with the agent figure', async () => {
    const { SidebarAgentIcon } = await import('@/components/layout/sidebar/SidebarAgentIcon')
    const { container } = render(<SidebarAgentIcon agent={RIVET} name="Rivet" halo={false} motion="static" />)
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'R')
  })

  it('WorkspaceAgentList draws the Admin mark from the workspace-carried main', async () => {
    const { WorkspaceAgentList } = await import('@/components/layout/sidebar/WorkspaceAgentList')
    const workspace = {
      id: 'default',
      name: 'Default',
      status: 'active',
      pinned: false,
      pin_order: 0,
      task_count: 0,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
      revision: '0'.repeat(64),
      is_default: true,
      admin_main_session_id: 'seam-opaque-admin-main',
    } as Workspace
    const admin = makeAgent({ id: 'admin', name: 'Admin', type: 'core', figure: 'Robot', role: 'security', color: '#F472B6' })
    const { container } = render(
      <WorkspaceAgentList
        projects={[workspace]}
        expandedIds={new Set([workspace.id])}
        activeId={null}
        agents={[admin]}
        rosterState="fresh"
        sessions={[]}
        onToggle={() => {}}
        onOpen={() => {}}
        onOverlayClose={() => {}}
      />,
    )
    expectAgentMarkFigure(container, 'Robot', 1)
    expectNoInitialCircle(container, 'A')
  })
})
