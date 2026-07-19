/**
 * calendarAgentFilter.test.ts (test 19)
 *
 * Traces to: docs/internal/specs/calendar-recurrence-redesign-spec.md
 *   User Story 4 (Filter the calendar by agent), FR-015
 *   BDD "Agent filter narrows all task event kinds instantly" (US-4/AS-1-3)
 *   BDD "Unassigned filter shows only agentless tasks" (US-4/AS-4)
 *
 * Pure unit tests over `eventMatchesAgentFilter` / `filterEventsByAgent` —
 * no React, no network. Covers the filter predicate incl. the
 * milestones-exempt rule (Ambiguity Warning #2).
 */

import { describe, it, expect } from 'vitest'
import type { EventInput } from '@fullcalendar/core'
import {
  eventMatchesAgentFilter,
  filterEventsByAgent,
  AGENT_FILTER_ALL,
  AGENT_FILTER_UNASSIGNED,
} from './calendarAgentFilter'
import type { Task } from '@/lib/api'

// ── Fixtures ─────────────────────────────────────────────────────────────────

// Mirrors the `makeTask` factory convention in eventMapping.test.ts: the
// generated `Task` type marks several fields (owner/created_by/timestamps)
// required even though they're irrelevant to this predicate, so the fixture
// is cast rather than exhaustively populated.
function makeTask(overrides: Partial<Task> & { id: string }): Task {
  return {
    title: overrides.id,
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    ...overrides,
  } as Task
}

function dueEvent(id: string, taskId: string): EventInput {
  return {
    id: `task:${taskId}:due`,
    start: new Date('2026-08-15'),
    title: id,
    extendedProps: { kind: 'task-due', taskId, status: 'inbox', icon: 'Circle' },
  }
}

function fireEvent(id: string, taskId: string): EventInput {
  return {
    id: `task:${taskId}:fire`,
    start: new Date('2026-08-15T09:00:00'),
    title: id,
    extendedProps: { kind: 'task-fire', taskId, status: 'inbox', icon: 'Clock' },
  }
}

function milestoneEvent(id: string, milestoneId: string): EventInput {
  return {
    id: `milestone:${milestoneId}`,
    start: new Date('2026-08-16'),
    title: id,
    extendedProps: { kind: 'milestone', milestoneId, icon: 'Flag' },
  }
}

// Simulates a Wave-2 recurring-occurrence chip. This `kind` does not exist in
// the current `CalendarEventExtProps` union yet — the predicate must still
// filter it correctly by keying off `taskId` generically (spec US-4 note:
// "occurrence chips carry their task_id → resolve agent via the task list").
function occurrenceEvent(id: string, taskId: string): EventInput {
  return {
    id: `task:${taskId}:occ:2026-08-17`,
    start: new Date('2026-08-17'),
    title: id,
    extendedProps: { kind: 'task-occurrence', taskId },
  }
}

// Simulates a Wave-2 aggregated ">3 occurrences" day-bucket chip — same
// genericity requirement as `occurrenceEvent` above.
function aggregatedEvent(id: string, taskId: string): EventInput {
  return {
    id: `task:${taskId}:agg:2026-08-18`,
    start: new Date('2026-08-18'),
    title: id,
    extendedProps: { kind: 'task-agg', taskId },
  }
}

const mia = makeTask({ id: 'task-mia-due', agent_id: 'mia' })
const miaRecurring = makeTask({ id: 'task-mia-recurring', agent_id: 'mia' })
const jim = makeTask({ id: 'task-jim-once', agent_id: 'jim' })
const unassigned = makeTask({ id: 'task-unassigned', agent_id: undefined })

const tasks: Task[] = [mia, miaRecurring, jim, unassigned]

// ── eventMatchesAgentFilter ──────────────────────────────────────────────────

describe('eventMatchesAgentFilter — AGENT_FILTER_ALL is a no-op', () => {
  it('shows every event kind when no filter is applied', () => {
    // Traces to: US-4/AS-1 ("All agents" default)
    expect(eventMatchesAgentFilter(dueEvent('d', mia.id), tasks, AGENT_FILTER_ALL)).toBe(true)
    expect(eventMatchesAgentFilter(fireEvent('f', jim.id), tasks, AGENT_FILTER_ALL)).toBe(true)
    expect(eventMatchesAgentFilter(milestoneEvent('m', 'ms-1'), tasks, AGENT_FILTER_ALL)).toBe(true)
    expect(eventMatchesAgentFilter(occurrenceEvent('o', miaRecurring.id), tasks, AGENT_FILTER_ALL)).toBe(true)
  })
})

describe('eventMatchesAgentFilter — narrows every task-derived chip kind (US-4/AS-2)', () => {
  it('due chip: matches only the selected agent', () => {
    expect(eventMatchesAgentFilter(dueEvent('d', mia.id), tasks, 'mia')).toBe(true)
    expect(eventMatchesAgentFilter(dueEvent('d', mia.id), tasks, 'jim')).toBe(false)
  })

  it('fire (once) chip: matches only the selected agent', () => {
    expect(eventMatchesAgentFilter(fireEvent('f', jim.id), tasks, 'jim')).toBe(true)
    expect(eventMatchesAgentFilter(fireEvent('f', jim.id), tasks, 'mia')).toBe(false)
  })

  it('recurring occurrence chip (Wave-2 kind, not in the current union): resolves via taskId generically', () => {
    // Proves the predicate keys off the underlying task's agent_id, not a
    // hardcoded switch over `kind` — this kind does not exist in
    // `CalendarEventExtProps` yet, only `taskId` duck-typing.
    expect(eventMatchesAgentFilter(occurrenceEvent('o', miaRecurring.id), tasks, 'mia')).toBe(true)
    expect(eventMatchesAgentFilter(occurrenceEvent('o', miaRecurring.id), tasks, 'jim')).toBe(false)
  })

  it('aggregated day-bucket chip (Wave-2 kind): resolves via taskId generically', () => {
    expect(eventMatchesAgentFilter(aggregatedEvent('a', miaRecurring.id), tasks, 'mia')).toBe(true)
    expect(eventMatchesAgentFilter(aggregatedEvent('a', miaRecurring.id), tasks, 'jim')).toBe(false)
  })

  it('differentiation: the same filter value produces different results for different agents’ chips', () => {
    // Anti-hardcode guard.
    const miaResult = eventMatchesAgentFilter(dueEvent('d', mia.id), tasks, 'mia')
    const jimResult = eventMatchesAgentFilter(fireEvent('f', jim.id), tasks, 'mia')
    expect(miaResult).not.toBe(jimResult)
  })
})

describe('eventMatchesAgentFilter — milestones are exempt (US-4/AS-3, Ambiguity #2)', () => {
  it('milestone chips remain visible under an active agent filter', () => {
    expect(eventMatchesAgentFilter(milestoneEvent('m', 'ms-1'), tasks, 'mia')).toBe(true)
    expect(eventMatchesAgentFilter(milestoneEvent('m', 'ms-1'), tasks, 'jim')).toBe(true)
  })

  it('milestone chips remain visible under the Unassigned filter too', () => {
    expect(eventMatchesAgentFilter(milestoneEvent('m', 'ms-1'), tasks, AGENT_FILTER_UNASSIGNED)).toBe(true)
  })
})

describe('eventMatchesAgentFilter — Unassigned filter (US-4/AS-4)', () => {
  it('shows only chips whose task has no agent_id', () => {
    expect(eventMatchesAgentFilter(dueEvent('d', unassigned.id), tasks, AGENT_FILTER_UNASSIGNED)).toBe(true)
  })

  it('hides chips whose task HAS an agent_id', () => {
    expect(eventMatchesAgentFilter(dueEvent('d', mia.id), tasks, AGENT_FILTER_UNASSIGNED)).toBe(false)
    expect(eventMatchesAgentFilter(fireEvent('f', jim.id), tasks, AGENT_FILTER_UNASSIGNED)).toBe(false)
  })
})

describe('eventMatchesAgentFilter — edge cases', () => {
  it('fails closed: a task-kind chip whose task cannot be resolved is hidden under an active filter', () => {
    expect(eventMatchesAgentFilter(dueEvent('d', 'ghost-task-id'), tasks, 'mia')).toBe(false)
  })

  it('a chip with no recognizable extendedProps shape is left visible', () => {
    const weird: EventInput = { id: 'x', start: new Date('2026-08-15'), title: 'x' }
    expect(eventMatchesAgentFilter(weird, tasks, 'mia')).toBe(true)
  })
})

// ── filterEventsByAgent (the function CalendarScreen calls directly) ────────

describe('filterEventsByAgent — two agents in one week, selecting one shows only that agent’s events instantly', () => {
  // Traces to: Independent Test (US-4) — "With tasks assigned to two different
  // agents on the same week, select one agent in the toolbar dropdown and
  // verify only that agent's task events remain, instantly (no network
  // request for the filtering itself — this module makes none)."
  const weekEvents: EventInput[] = [
    dueEvent('Mia due', mia.id),
    occurrenceEvent('Mia occurrence', miaRecurring.id),
    aggregatedEvent('Mia aggregated', miaRecurring.id),
    fireEvent('Jim once', jim.id),
    milestoneEvent('Launch', 'ms-1'),
  ]

  it('AGENT_FILTER_ALL returns every event unchanged (identity, no filtering work)', () => {
    const result = filterEventsByAgent(weekEvents, tasks, AGENT_FILTER_ALL)
    expect(result).toBe(weekEvents) // same reference — true no-op, zero work
    expect(result).toHaveLength(5)
  })

  it('selecting "mia" narrows to Mia’s due/occurrence/aggregated chips + the exempt milestone — Jim’s chip is gone', () => {
    const result = filterEventsByAgent(weekEvents, tasks, 'mia')
    const titles = result.map((e) => e.title)
    expect(titles).toEqual(
      expect.arrayContaining(['Mia due', 'Mia occurrence', 'Mia aggregated', 'Launch']),
    )
    expect(titles).not.toContain('Jim once')
    expect(result).toHaveLength(4)
  })

  it('selecting "jim" narrows to Jim’s chip + the exempt milestone — none of Mia’s chips remain', () => {
    const result = filterEventsByAgent(weekEvents, tasks, 'jim')
    const titles = result.map((e) => e.title)
    expect(titles).toEqual(expect.arrayContaining(['Jim once', 'Launch']))
    expect(titles).not.toContain('Mia due')
    expect(titles).not.toContain('Mia occurrence')
    expect(titles).not.toContain('Mia aggregated')
    expect(result).toHaveLength(2)
  })

  it('differentiation: selecting "mia" vs "jim" produces different result sets from the same input', () => {
    // Anti-hardcode guard — the filter genuinely depends on the selected agent.
    const miaResult = filterEventsByAgent(weekEvents, tasks, 'mia').map((e) => e.id)
    const jimResult = filterEventsByAgent(weekEvents, tasks, 'jim').map((e) => e.id)
    expect(miaResult).not.toEqual(jimResult)
  })
})

describe('filterEventsByAgent — Unassigned selection (US-4/AS-4)', () => {
  it('shows only the unassigned task’s events plus exempt milestones', () => {
    const events: EventInput[] = [
      dueEvent('Mia due', mia.id),
      dueEvent('Unassigned due', unassigned.id),
      milestoneEvent('Launch', 'ms-1'),
    ]
    const result = filterEventsByAgent(events, tasks, AGENT_FILTER_UNASSIGNED)
    const titles = result.map((e) => e.title)
    expect(titles).toEqual(expect.arrayContaining(['Unassigned due', 'Launch']))
    expect(titles).not.toContain('Mia due')
    expect(result).toHaveLength(2)
  })
})
