// planFilter.test.ts — US-10 AS-2 ("Selecting a plan filters the board to its
// dependency chain"), US-11 AS-8 ("board tag chip filters the board"), and the
// edge case "a task belonging to a plan AND carrying tags" (plan+tag compose, AND).

import { describe, it, expect } from 'vitest'
import { filterByPlan, filterByTag, filterByPlanAndTag, PLAN_FILTER_ALL, PLAN_FILTER_UNTAGGED } from './planFilter'
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

describe('filterByPlan', () => {
  const tasks = [
    makeTask({ id: 't1', plan_id: 'plan-a' }),
    makeTask({ id: 't2', plan_id: 'plan-b' }),
    makeTask({ id: 't3' }), // no plan
  ]

  it('PLAN_FILTER_ALL (null) returns every task unfiltered', () => {
    expect(filterByPlan(tasks, PLAN_FILTER_ALL)).toEqual(tasks)
  })

  it('a plan id filters to only that plan’s member tasks', () => {
    const result = filterByPlan(tasks, 'plan-a')
    expect(result.map((t) => t.id)).toEqual(['t1'])
  })

  it('a plan with zero member tasks yields an empty array (not a crash)', () => {
    expect(filterByPlan(tasks, 'plan-nonexistent')).toEqual([])
  })
})

describe('filterByTag', () => {
  const tasks = [
    makeTask({ id: 't1', tags: ['release'] }),
    makeTask({ id: 't2', tags: ['release', 'urgent'] }),
    makeTask({ id: 't3', tags: [] }),
    makeTask({ id: 't4' }), // tags absent entirely
  ]

  it('null returns every task unfiltered', () => {
    expect(filterByTag(tasks, null)).toEqual(tasks)
  })

  it('a tag string filters to tasks carrying that tag', () => {
    expect(filterByTag(tasks, 'release').map((t) => t.id)).toEqual(['t1', 't2'])
    expect(filterByTag(tasks, 'urgent').map((t) => t.id)).toEqual(['t2'])
  })

  it('PLAN_FILTER_UNTAGGED matches tasks with an empty or absent tags array', () => {
    expect(filterByTag(tasks, PLAN_FILTER_UNTAGGED).map((t) => t.id)).toEqual(['t3', 't4'])
  })
})

describe('filterByPlanAndTag — composition (AND)', () => {
  const tasks = [
    makeTask({ id: 't1', plan_id: 'plan-a', tags: ['release'] }),
    makeTask({ id: 't2', plan_id: 'plan-a', tags: ['urgent'] }),
    makeTask({ id: 't3', plan_id: 'plan-b', tags: ['release'] }),
    makeTask({ id: 't4', tags: ['release'] }), // no plan
  ]

  it('intersects plan filter and tag filter (edge case: task belongs to a plan AND carries tags)', () => {
    const result = filterByPlanAndTag(tasks, 'plan-a', 'release')
    expect(result.map((t) => t.id)).toEqual(['t1'])
  })

  it('plan filter alone (tag = null) returns the full plan chain', () => {
    const result = filterByPlanAndTag(tasks, 'plan-a', null)
    expect(result.map((t) => t.id).sort()).toEqual(['t1', 't2'])
  })

  it('tag filter alone (plan = All) returns every tagged task regardless of plan', () => {
    const result = filterByPlanAndTag(tasks, PLAN_FILTER_ALL, 'release')
    expect(result.map((t) => t.id).sort()).toEqual(['t1', 't3', 't4'])
  })

  it('both All returns everything', () => {
    expect(filterByPlanAndTag(tasks, PLAN_FILTER_ALL, null)).toEqual(tasks)
  })
})
