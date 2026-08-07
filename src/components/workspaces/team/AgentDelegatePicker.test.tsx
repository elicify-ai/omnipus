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
    isImplicit: false,
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

  it('after selecting a candidate (menu closes), focus returns to the trigger button (WCAG 2.1.1 Keyboard)', () => {
    // Radix's default onCloseAutoFocus restore-to-last-focused-element can
    // land on <body> when the trigger has unmounted or React Flow's own
    // focus tracking interferes — AgentDelegatePicker suppresses that and
    // explicitly refocuses its own trigger (see the onCloseAutoFocus handler
    // wired to DropdownMenuContent), so a keyboard user keeps their place on
    // the node they were delegating from instead of losing focus entirely.
    renderPicker()
    fireEvent.click(screen.getByTestId('team-node-delegate-target-mia-jim'))

    // The mocked DropdownMenuContent invokes onCloseAutoFocus from its own
    // onBlur (see the vi.mock at the top of this file) — simulating the
    // menu-close lifecycle event Radix would fire for real.
    const content = screen.getByTestId('delegate-menu-content')
    fireEvent.blur(content)

    const trigger = screen.getByTestId('team-node-delegate-mia')
    expect(trigger).toHaveFocus()
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

// SD-C17 defense-in-depth (ADR-049 D3): a System agent (the Judge) is never
// a valid delegation target, even in the hypothetical case where one somehow
// reached the canvas as a node (the supported flow already can't produce
// this — AddAgentPicker.tsx excludes `type: 'system'` from the team-add
// picker in the first place — see AddAgentPicker.test.tsx for that half of
// the defense). AgentDelegatePicker passes `n.type === 'system'` as
// `isSystemTarget` into `validateConnection`, which rejects with
// 'system-target' regardless of team-membership/duplicate-edge state.
describe('AgentDelegatePicker — System agent exclusion (SD-C17)', () => {
  it('excludes a type:system node (the Judge) from the candidate list even though it is otherwise a valid, unconnected team member', () => {
    const judgeNode = node('judge', { name: 'Judge', type: 'system', role: 'System agent' })
    renderPicker({
      nodes: [SOURCE, VALID_TARGET, judgeNode],
      editState: state({ members: ['mia', 'jim', 'judge'] }), // judge IS a team member here
    })
    // jim (a normal Main agent, team member, no edge yet) is a valid candidate.
    expect(screen.getByTestId('team-node-delegate-target-mia-jim')).toBeInTheDocument()
    // judge is a team member with no edge either — by team-membership alone
    // it would qualify, but its type:system must still exclude it.
    expect(screen.queryByTestId('team-node-delegate-target-mia-judge')).toBeNull()
    expect(screen.queryByText('Judge')).not.toBeInTheDocument()
  })

  it('with the Judge as the ONLY other team member, the menu falls back to the empty-state message', () => {
    const judgeNode = node('judge', { name: 'Judge', type: 'system', role: 'System agent' })
    renderPicker({
      nodes: [SOURCE, judgeNode],
      editState: state({ members: ['mia', 'judge'] }),
    })
    expect(screen.queryByTestId(/team-node-delegate-target-/)).toBeNull()
    expect(screen.getByText(/No eligible agents/i)).toBeInTheDocument()
  })
})
