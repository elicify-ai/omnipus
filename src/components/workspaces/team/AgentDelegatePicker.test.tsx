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
  it('lists exactly the nodes validateConnection allows, including the source as an ordinary self-edge target', () => {
    renderPicker()
    // Valid target (team member, no existing edge) appears.
    expect(screen.getByTestId('team-node-delegate-target-mia-jim')).toBeInTheDocument()
    // Invalid target (not a team member) is excluded.
    expect(screen.queryByTestId('team-node-delegate-target-mia-planner')).toBeNull()
    // The source's OWN self-edge is an ordinary edge the shared model accepts
    // (validateConnection has no identity gate — a self-edge is exempt from the
    // cycle check and bounded by membership/duplicate like any other), so the
    // source appears as a candidate for itself. The picker must NOT hard-code a
    // `n.id !== source.id` filter (F6, reverify 60e299a86).
    expect(screen.getByTestId('team-node-delegate-target-mia-mia')).toBeInTheDocument()
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
  it('renders the empty-state message only when even the source self-edge is rejected (it already exists) and no other target qualifies', () => {
    // Weaker every-agent-has-an-edge scenario: Mia already carries her own
    // self-edge (a duplicate the model rejects) and mia/jim/planner leaves no
    // other qualifying target — jim/planner are not members here. This is the
    // honest empty state; a one-agent team is NOT one — the source itself is
    // offered (see the self-edge restore suite below).
    renderPicker({
      editState: state({
        members: ['mia'],
        edges: [{ from: 'mia', to: 'mia', modes: ['direct'] }],
      }),
      nodes: ALL_NODES,
    })
    expect(screen.queryByTestId(/team-node-delegate-target-/)).toBeNull()
    expect(
      screen.getByText(/No eligible agents/i),
    ).toBeInTheDocument()
  })
})

// F6 (reverify 60e299a86): the shared model accepts a member's ordinary
// self-edge, but the keyboard "Delegate…" picker still filtered
// `n.id !== source.id`, so a keyboard user who deleted Mia→Mia could not pick
// Mia to restore it, and a one-agent team reported no eligible agents. The
// picker must offer the source as its own target EXACTLY when
// `validateConnection` accepts that self-edge — via the shared validator, with
// no duplicated identity rule — while every other protection (membership,
// system-target, duplicate) still holds.
describe('AgentDelegatePicker — self-edge restore (F6, keyboard path)', () => {
  it('offers the source agent as its own target so a deleted self-edge can be restored without a drag gesture', () => {
    renderPicker({
      source: SOURCE,
      nodes: [SOURCE],
      editState: state({ members: ['mia'], edges: [] }),
    })
    expect(screen.getByTestId('team-node-delegate-target-mia-mia')).toBeInTheDocument()
  })

  it('a one-agent team offers that single agent itself rather than reporting no eligible agents', () => {
    renderPicker({
      nodes: [SOURCE],
      editState: state({ members: ['mia'], edges: [] }),
    })
    expect(screen.getByTestId('team-node-delegate-target-mia-mia')).toBeInTheDocument()
    expect(screen.queryByText(/No eligible agents/i)).toBeNull()
  })

  it('selecting the self-target calls onDelegate(source, source) — the same validated mutation path as a drag', () => {
    const { onDelegate } = renderPicker({
      nodes: [SOURCE],
      editState: state({ members: ['mia'], edges: [] }),
    })
    fireEvent.click(screen.getByTestId('team-node-delegate-target-mia-mia'))
    expect(onDelegate).toHaveBeenCalledWith('mia', 'mia')
    expect(onDelegate).toHaveBeenCalledTimes(1)
  })

  it('negative control — does NOT offer the source self-edge when the model rejects it as a duplicate', () => {
    renderPicker({
      source: SOURCE,
      nodes: [SOURCE],
      editState: state({
        members: ['mia'],
        edges: [{ from: 'mia', to: 'mia', modes: ['direct'] }],
      }),
    })
    expect(screen.queryByTestId('team-node-delegate-target-mia-mia')).toBeNull()
  })

  it('negative control — does NOT offer the source self-edge when the source is not a team member', () => {
    renderPicker({
      source: SOURCE,
      nodes: [SOURCE],
      editState: state({ members: ['jim'], edges: [] }),
    })
    expect(screen.queryByTestId('team-node-delegate-target-mia-mia')).toBeNull()
  })

  it('negative control — does NOT offer a system-type source as its own self target (SD-C17 system-target)', () => {
    const judgeNode = node('judge', { name: 'Judge', type: 'system', role: 'System agent' })
    renderPicker({
      source: judgeNode,
      nodes: [judgeNode],
      editState: state({ members: ['judge'], edges: [] }),
    })
    expect(screen.queryByTestId('team-node-delegate-target-judge-judge')).toBeNull()
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

  it('with the Judge as the ONLY other team member, the menu offers only the source self-edge — the Judge is still excluded (F6: source is the sole candidate, not an empty state)', () => {
    const judgeNode = node('judge', { name: 'Judge', type: 'system', role: 'System agent' })
    renderPicker({
      nodes: [SOURCE, judgeNode],
      editState: state({ members: ['mia', 'judge'] }),
    })
    // The Judge is excluded as a target even though it is a team member.
    expect(screen.queryByTestId('team-node-delegate-target-mia-judge')).toBeNull()
    expect(screen.queryByText('Judge')).not.toBeInTheDocument()
    // Mia's own self-edge is an ordinary valid edge, so Mia is offered back to
    // herself — the menu is NOT empty (F6).
    expect(screen.getByTestId('team-node-delegate-target-mia-mia')).toBeInTheDocument()
    expect(screen.queryByText(/No eligible agents/i)).toBeNull()
  })
})
