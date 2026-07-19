/**
 * ListView.filters.test.tsx
 *
 * ADR-049 — the Milestone `FilterSelect` + Milestone column are replaced by a
 * Plan filter + a Tag filter, and a Tags column (replaces the Milestone
 * column). Covers:
 *   1. No milestone UI anywhere (SC-040).
 *   2. Plan filter renders only when plans exist, and filters the table.
 *   3. Tag filter is derived from the tasks present (no fixed catalog), incl.
 *      the "Untagged" sentinel, and filters the table.
 *   4. The Tags column renders tag chips (capped with "+N" overflow), never
 *      a Milestone column.
 */

import { describe, it, expect } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import { ListView } from './ListView'
import type { Task, Plan } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:05:00Z',
    ...overrides,
  }
}

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Launch',
    state: 'running',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

// Selecting a Radix Select option: click the trigger, then click the option
// by its `role=option` (NOT plain text — a tag/plan name can collide with a
// same-named chip already rendered in the table, e.g. a "release" tag chip
// vs. the "release" dropdown option).
function selectOption(triggerLabel: RegExp, optionLabel: RegExp) {
  const trigger = screen.getByRole('combobox', { name: triggerLabel })
  fireEvent.click(trigger)
  const option = screen.getByRole('option', { name: optionLabel })
  fireEvent.click(option)
}

describe('ListView — no milestone UI anywhere (SC-040)', () => {
  it('renders no "Milestone" filter, column, or text', () => {
    render(<ListView tasks={[makeTask()]} plans={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.queryByText(/milestone/i)).toBeNull()
  })
})

describe('ListView — Plan filter', () => {
  it('does not render the Plan filter when there are no plans', () => {
    render(<ListView tasks={[makeTask()]} plans={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.queryByRole('combobox', { name: /filter by plan/i })).toBeNull()
  })

  it('renders the Plan filter when plans exist, and filters the table to that plan', () => {
    const tasks = [
      makeTask({ id: 't1', title: 'Launch task', plan_id: 'plan-1' }),
      makeTask({ id: 't2', title: 'Other task', plan_id: 'plan-2' }),
      makeTask({ id: 't3', title: 'No-plan task' }),
    ]
    const plans = [makePlan({ id: 'plan-1', title: 'Launch' }), makePlan({ id: 'plan-2', title: 'Hardening' })]
    render(<ListView tasks={tasks} plans={plans} agents={[]} onTaskClick={() => {}} />)

    expect(screen.getByText('Launch task')).toBeInTheDocument()
    expect(screen.getByText('Other task')).toBeInTheDocument()
    expect(screen.getByText('No-plan task')).toBeInTheDocument()

    selectOption(/filter by plan/i, /^Launch$/)

    expect(screen.getByText('Launch task')).toBeInTheDocument()
    expect(screen.queryByText('Other task')).toBeNull()
    expect(screen.queryByText('No-plan task')).toBeNull()
  })
})

describe('ListView — Tag filter (derived from visible tasks, no fixed catalog)', () => {
  it('does not render the Tag filter when no task carries a tag', () => {
    render(<ListView tasks={[makeTask()]} plans={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.queryByRole('combobox', { name: /filter by tag/i })).toBeNull()
  })

  it('renders the Tag filter with distinct tags across the visible tasks and filters by tag', () => {
    const tasks = [
      makeTask({ id: 't1', title: 'Release task', tags: ['release'] }),
      makeTask({ id: 't2', title: 'Urgent task', tags: ['urgent'] }),
      makeTask({ id: 't3', title: 'Bare task' }),
    ]
    render(<ListView tasks={tasks} plans={[]} agents={[]} onTaskClick={() => {}} />)

    selectOption(/filter by tag/i, /^release$/)

    expect(screen.getByText('Release task')).toBeInTheDocument()
    expect(screen.queryByText('Urgent task')).toBeNull()
    expect(screen.queryByText('Bare task')).toBeNull()
  })

  it('the "Untagged" sentinel filters to tasks with zero tags', () => {
    const tasks = [
      makeTask({ id: 't1', title: 'Release task', tags: ['release'] }),
      makeTask({ id: 't2', title: 'Bare task' }),
    ]
    render(<ListView tasks={tasks} plans={[]} agents={[]} onTaskClick={() => {}} />)

    selectOption(/filter by tag/i, /^Untagged$/)

    expect(screen.getByText('Bare task')).toBeInTheDocument()
    expect(screen.queryByText('Release task')).toBeNull()
  })
})

describe('ListView — Tags column (replaces the removed Milestone column)', () => {
  it('renders a Tags column header, not Milestone', () => {
    render(<ListView tasks={[makeTask()]} plans={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByRole('columnheader', { name: 'Tags' })).toBeInTheDocument()
    expect(screen.queryByRole('columnheader', { name: /milestone/i })).toBeNull()
  })

  it('renders tag chips in a row, capped with a "+N" overflow indicator', () => {
    const task = makeTask({ tags: ['release', 'urgent', 'q3', 'extra'] })
    render(<ListView tasks={[task]} plans={[]} agents={[]} onTaskClick={() => {}} />)

    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('release')).toBeInTheDocument()
    expect(within(row).getByText('urgent')).toBeInTheDocument()
    expect(within(row).getByText('+2')).toBeInTheDocument()
  })

  it('renders "—" in the Tags cell when a task has no tags', () => {
    render(<ListView tasks={[makeTask()]} plans={[]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    // Both the Tags cell and the Agent cell render "—" for a bare task — scope
    // to the 4th <td> (Pri, Title, Status, Tags, Agent, Updated) specifically.
    const cells = within(row).getAllByRole('cell')
    expect(within(cells[3]).getByText('—')).toBeInTheDocument()
  })
})
