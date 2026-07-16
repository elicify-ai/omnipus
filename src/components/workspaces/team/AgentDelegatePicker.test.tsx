/**
 * Unit tests for AgentDelegatePicker — the keyboard-operable "Delegate…" menu
 * (WCAG 2.1.1 equivalent of dragging the gold connection dot onto another
 * node). Mounts the component directly with explicit props (no React Flow /
 * canvas context needed — its prop API is unchanged by the
 * WorkspaceTeamGraph context refactor, see WorkspaceTeamGraph.tsx).
 *
 * The real `@/components/ui/dropdown-menu` wraps Radix's portal-based
 * DropdownMenu, which is unreliable to drive in jsdom (pointer-capture /
 * portal quirks — see Sidebar.test.tsx for the same trade-off). We stub it
 * with a plain, always-rendered DOM shape so these tests exercise
 * AgentDelegatePicker's own candidate-filtering + selection logic
 * deterministically, matching the existing test convention in this repo.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AgentDelegatePicker } from './AgentDelegatePicker'
import type { TeamEditState, TeamNodeModel } from './teamGraphModel'

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({
    children,
    onCloseAutoFocus,
  }: {
    children: React.ReactNode
    onCloseAutoFocus?: (e: Event) => void
  }) => (
    <div data-testid="delegate-menu-content" onBlur={() => onCloseAutoFocus?.(new Event('blur'))}>
      {children}
    </div>
  ),
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
  DropdownMenuItem: ({
    children,
    onSelect,
    ...rest
  }: {
    children: React.ReactNode
    onSelect?: () => void
    [key: string]: unknown
  }) => (
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions
    <div role="menuitem" onClick={() => onSelect?.()} {...rest}>
      {children}
    </div>
  ),
}))

function node(id: string, over: Partial<TeamNodeModel> = {}): TeamNodeModel {
  return {
    id,
    name: id.charAt(0).toUpperCase() + id.slice(1),
    type: 'Main',
    role: 'Main agent',
    isDefault: false,
    isWorker: false,
    isGhost: false,
    position: { x: 0, y: 0 },
    ...over,
  }
}

// 3-node model: source + one valid target (team member, no existing edge) +
// one invalid target (present on the canvas but NOT a team member, so
// `validateConnection` rejects it with 'not-member').
const SOURCE = node('mia')
const VALID_TARGET = node('jim')
const INVALID_TARGET = node('planner', { name: 'Planner', role: 'Subagent', isWorker: true })
const ALL_NODES = [SOURCE, VALID_TARGET, INVALID_TARGET]

function state(over: Partial<TeamEditState> = {}): TeamEditState {
  return { members: ['mia', 'jim'], edges: [], defaultDepth: 3, ...over }
}

function renderPicker(over: Partial<Parameters<typeof AgentDelegatePicker>[0]> = {}) {
  const onDelegate = vi.fn()
  const props = {
    source: SOURCE,
    nodes: ALL_NODES,
    editState: state(),
    workerIds: new Set<string>(['planner']),
    onDelegate,
    ...over,
  }
  const utils = render(<AgentDelegatePicker {...props} />)
  return { onDelegate, ...utils }
}

describe('AgentDelegatePicker — candidate list', () => {
  it('lists exactly the nodes validateConnection allows, excluding the source itself', () => {
    renderPicker()
    // Valid target (team member, no existing edge) appears.
    expect(screen.getByTestId('team-node-delegate-target-mia-jim')).toBeInTheDocument()
    // Invalid target (not a team member) is excluded.
    expect(screen.queryByTestId('team-node-delegate-target-mia-planner')).toBeNull()
    // The source itself never appears as its own candidate.
    expect(screen.queryByTestId('team-node-delegate-target-mia-mia')).toBeNull()
  })

  it('excludes a target that already has an edge from this source (duplicate)', () => {
    renderPicker({
      editState: state({
        members: ['mia', 'jim', 'planner'],
        edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
      }),
      nodes: ALL_NODES,
    })
    // jim now has an existing edge from mia -> duplicate -> excluded.
    expect(screen.queryByTestId('team-node-delegate-target-mia-jim')).toBeNull()
    // planner is a team member here with no edge from mia -> valid candidate.
    expect(screen.getByTestId('team-node-delegate-target-mia-planner')).toBeInTheDocument()
  })
})

describe('AgentDelegatePicker — selection', () => {
  it('selecting a candidate calls onDelegate(source, target)', () => {
    const { onDelegate } = renderPicker()
    fireEvent.click(screen.getByTestId('team-node-delegate-target-mia-jim'))
    expect(onDelegate).toHaveBeenCalledWith('mia', 'jim')
    expect(onDelegate).toHaveBeenCalledTimes(1)
  })
})

describe('AgentDelegatePicker — empty state', () => {
  it('renders the empty-state message when no valid candidates exist', () => {
    renderPicker({
      editState: state({ members: ['mia'] }), // jim/planner not on the team
      nodes: ALL_NODES,
    })
    expect(screen.queryByTestId(/team-node-delegate-target-/)).toBeNull()
    expect(
      screen.getByText(/No eligible agents/i),
    ).toBeInTheDocument()
  })
})
