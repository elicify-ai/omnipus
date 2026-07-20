// planFilter.test.ts — US-11 AS-8 ("board tag chip filters the board").

import { describe, it, expect } from 'vitest'
import { filterByTag, PLAN_FILTER_UNTAGGED } from './planFilter'
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
