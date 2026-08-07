// AddAgentPicker.test.tsx — the "[+ Add agent]" popover picker of global
// agents not yet on the workspace team (WorkspaceTeamTab.tsx). Companion to
// AgentDelegatePicker.test.tsx's own System-agent-exclusion coverage: this
// is the OTHER half of SD-C17's defense-in-depth (ADR-049 D3) — a
// `type: 'system'` agent (the Judge) must never even be addable to a team in
// the first place, via the `.filter(a => a.type !== 'system')` in
// AddAgentPicker.tsx:36.
//
// Radix Popover is stubbed to render its content inline (mirrors
// model-selector.test.tsx's convention) so candidates are reachable without
// a real portal/pointer-capture in jsdom.

import * as React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AddAgentPicker } from './AddAgentPicker'
import type { Agent } from '@/lib/api'
import { makeAgent } from '@/test/factories'

vi.mock('@/components/ui/popover', () => {
  return {
    Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
    PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) => {
      if (asChild && React.isValidElement(children)) return children
      return React.createElement('div', null, children)
    },
    PopoverContent: ({ children }: { children: React.ReactNode }) =>
      React.createElement('div', { 'data-testid': 'popover-content' }, children),
  }
})

const JIM = makeAgent({ id: 'jim', name: 'Jim', type: 'core', description: 'Orchestrator' })
const MARS = makeAgent({ id: 'mars', name: 'Mars', type: 'Main', description: 'Ops lead' })
const JUDGE = makeAgent({ id: 'judge', name: 'Judge', type: 'system', locked: true, description: 'Adjudicates DoD criteria' })

function renderPicker(overrides: Partial<Parameters<typeof AddAgentPicker>[0]> = {}) {
  const onAdd = vi.fn()
  const props = {
    agents: [JIM, MARS, JUDGE] as Agent[],
    memberIds: new Set<string>(),
    onAdd,
    ...overrides,
  }
  const utils = render(<AddAgentPicker {...props} />)
  return { onAdd, ...utils }
}

describe('AddAgentPicker — System agent exclusion (SD-C17, ADR-049 D3)', () => {
  it('never lists a type:system agent (the Judge) as a candidate, even though it is not a team member', () => {
    renderPicker()
    expect(screen.getByTestId('team-add-agent-option-jim')).toBeInTheDocument()
    expect(screen.getByTestId('team-add-agent-option-mars')).toBeInTheDocument()
    expect(screen.queryByTestId('team-add-agent-option-judge')).not.toBeInTheDocument()
    expect(screen.queryByText('Judge')).not.toBeInTheDocument()
  })

  it('a search query matching the Judge by name still surfaces nothing (type filter runs before the query filter)', async () => {
    const user = userEvent.setup()
    renderPicker()
    await user.type(screen.getByTestId('team-add-agent-search'), 'Judge')
    expect(screen.queryByTestId('team-add-agent-option-judge')).not.toBeInTheDocument()
    expect(screen.getByText('No matching agents.')).toBeInTheDocument()
  })

  it('when the Judge is the only non-member agent, the picker shows the empty-team-add state, not the Judge', () => {
    renderPicker({ agents: [JIM, JUDGE], memberIds: new Set(['jim']) })
    expect(screen.queryByTestId(/team-add-agent-option-/)).not.toBeInTheDocument()
    expect(screen.getByText('Every agent is already on this team.')).toBeInTheDocument()
  })
})

describe('AddAgentPicker — membership exclusion', () => {
  it('excludes an agent already on the team (memberIds)', () => {
    renderPicker({ memberIds: new Set(['jim']) })
    expect(screen.queryByTestId('team-add-agent-option-jim')).not.toBeInTheDocument()
    expect(screen.getByTestId('team-add-agent-option-mars')).toBeInTheDocument()
  })
})

describe('AddAgentPicker — search filtering', () => {
  it('filters candidates by name (case-insensitive)', async () => {
    const user = userEvent.setup()
    renderPicker()
    await user.type(screen.getByTestId('team-add-agent-search'), 'mar')
    expect(screen.getByTestId('team-add-agent-option-mars')).toBeInTheDocument()
    expect(screen.queryByTestId('team-add-agent-option-jim')).not.toBeInTheDocument()
  })

  it('filters candidates by description too', async () => {
    const user = userEvent.setup()
    renderPicker()
    await user.type(screen.getByTestId('team-add-agent-search'), 'orchestrator')
    expect(screen.getByTestId('team-add-agent-option-jim')).toBeInTheDocument()
    expect(screen.queryByTestId('team-add-agent-option-mars')).not.toBeInTheDocument()
  })

  it('sorts candidates alphabetically by name', () => {
    renderPicker()
    const options = screen.getAllByTestId(/team-add-agent-option-/)
    expect(options.map((o) => o.getAttribute('data-testid'))).toEqual([
      'team-add-agent-option-jim',
      'team-add-agent-option-mars',
    ])
  })
})

describe('AddAgentPicker — selection', () => {
  it('clicking a candidate calls onAdd with its id', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderPicker()
    await user.click(screen.getByTestId('team-add-agent-option-mars'))
    expect(onAdd).toHaveBeenCalledWith('mars')
    expect(onAdd).toHaveBeenCalledTimes(1)
  })

  it('the search query clears after a selection', async () => {
    const user = userEvent.setup()
    renderPicker()
    const search = screen.getByTestId('team-add-agent-search')
    await user.type(search, 'mar')
    await user.click(screen.getByTestId('team-add-agent-option-mars'))
    expect(search).toHaveValue('')
  })
})

describe('AddAgentPicker — empty states', () => {
  it('shows "Every agent is already on this team." when every agent is a member', () => {
    renderPicker({ memberIds: new Set(['jim', 'mars', 'judge']) })
    expect(screen.getByText('Every agent is already on this team.')).toBeInTheDocument()
  })

  it('shows "No matching agents." when a search matches nothing', async () => {
    const user = userEvent.setup()
    renderPicker()
    await user.type(screen.getByTestId('team-add-agent-search'), 'zzzznotanagent')
    expect(screen.getByText('No matching agents.')).toBeInTheDocument()
  })
})
