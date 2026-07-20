// taskFilters.test.ts — ADR-051 D2/D6 (Tasks Screen: Plans-as-Filter).

import { describe, it, expect } from 'vitest'
import { filterTasks } from './taskFilters'
import type { Task } from '@/lib/api'

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
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

describe('filterTasks', () => {
  const tasks = [
    makeTask({ id: 't1', plan_id: 'plan-a', agent_id: 'jim' }),
    makeTask({ id: 't2', plan_id: 'plan-a', agent_id: 'ava' }),
    makeTask({ id: 't3', plan_id: 'plan-b', agent_id: 'jim' }),
    makeTask({ id: 't4', agent_id: 'jim' }), // plan_id absent (plan-less task)
    makeTask({ id: 't5', plan_id: 'plan-b' }), // agent_id absent (unassigned task)
    makeTask({ id: 't6' }), // both absent
  ]

  it('both filters null returns every task, unfiltered, in a new array', () => {
    const result = filterTasks(tasks, { planId: null, ownerAgentId: null })
    expect(result).toEqual(tasks)
    expect(result).not.toBe(tasks)
  })

  it('empty input returns an empty array regardless of filter', () => {
    expect(filterTasks([], { planId: null, ownerAgentId: null })).toEqual([])
    expect(filterTasks([], { planId: 'plan-a', ownerAgentId: 'jim' })).toEqual([])
  })

  it('plan-only filter keeps tasks whose plan_id matches, drops the rest', () => {
    const result = filterTasks(tasks, { planId: 'plan-a', ownerAgentId: null })
    expect(result.map((t) => t.id)).toEqual(['t1', 't2'])
  })

  it('plan-only filter excludes tasks with an undefined plan_id', () => {
    const result = filterTasks(tasks, { planId: 'plan-b', ownerAgentId: null })
    expect(result.map((t) => t.id)).toEqual(['t3', 't5'])
    expect(result.map((t) => t.id)).not.toContain('t4')
  })

  it('owner-only filter keeps tasks whose agent_id matches, drops the rest', () => {
    const result = filterTasks(tasks, { planId: null, ownerAgentId: 'jim' })
    expect(result.map((t) => t.id)).toEqual(['t1', 't3', 't4'])
  })

  it('owner-only filter excludes tasks with an undefined agent_id', () => {
    const result = filterTasks(tasks, { planId: null, ownerAgentId: 'jim' })
    expect(result.map((t) => t.id)).not.toContain('t5')
    expect(result.map((t) => t.id)).not.toContain('t6')
  })

  it('both set ANDs the two filters (must satisfy both, not either)', () => {
    const result = filterTasks(tasks, { planId: 'plan-a', ownerAgentId: 'jim' })
    expect(result.map((t) => t.id)).toEqual(['t1'])
  })

  it('AND combination that matches nothing returns an empty array', () => {
    const result = filterTasks(tasks, { planId: 'plan-a', ownerAgentId: 'nobody' })
    expect(result).toEqual([])
  })

  it('plan filter with an id that matches no task returns an empty array', () => {
    expect(filterTasks(tasks, { planId: 'plan-zzz', ownerAgentId: null })).toEqual([])
  })

  it('owner filter with an id that matches no task returns an empty array', () => {
    expect(filterTasks(tasks, { planId: null, ownerAgentId: 'nobody' })).toEqual([])
  })

  it('does not mutate the input array or its task objects', () => {
    const original = [makeTask({ id: 't1', plan_id: 'plan-a' }), makeTask({ id: 't2', plan_id: 'plan-b' })]
    const snapshot = JSON.parse(JSON.stringify(original))
    filterTasks(original, { planId: 'plan-a', ownerAgentId: null })
    expect(original).toEqual(snapshot)
  })

  it('a task with both plan_id and agent_id undefined is excluded once any filter is active', () => {
    const loose = [makeTask({ id: 'loose' })]
    expect(filterTasks(loose, { planId: 'plan-a', ownerAgentId: null })).toEqual([])
    expect(filterTasks(loose, { planId: null, ownerAgentId: 'jim' })).toEqual([])
    expect(filterTasks(loose, { planId: null, ownerAgentId: null })).toEqual(loose)
  })
})
