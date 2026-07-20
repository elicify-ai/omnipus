/**
 * derivePlanColumn.test.ts
 *
 * Exhaustive branch coverage for `derivePlanColumn` (BoardView.tsx) — the
 * pure function that derives a plan card's status COLUMN at the Board's top
 * level from its member top-level tasks (Hierarchical Drill-Down board
 * redesign):
 *
 *   1. 0 tasks → 'inbox'
 *   2. all tasks 'done' → 'done'
 *   3. all tasks terminal ('done'|'failed') AND ≥1 'failed' → 'failed'
 *   4. else, first match in precedence: in_progress > blocked > planning >
 *      next > inbox
 */

import { describe, it, expect } from 'vitest'
import { derivePlanColumn } from './BoardView'
import type { Task } from '@/lib/api'

function makeTask(status: Task['status']): Task {
  return {
    id: `t-${status}-${Math.random().toString(36).slice(2)}`,
    title: 'Member task',
    status,
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
  }
}

describe('derivePlanColumn', () => {
  it('rule 1 — zero member tasks → inbox', () => {
    expect(derivePlanColumn([])).toBe('inbox')
  })

  it('rule 2 — every member task done → done', () => {
    expect(derivePlanColumn([makeTask('done')])).toBe('done')
    expect(derivePlanColumn([makeTask('done'), makeTask('done')])).toBe('done')
  })

  it('rule 3 — every member task terminal and at least one failed → failed', () => {
    expect(derivePlanColumn([makeTask('failed')])).toBe('failed')
    expect(derivePlanColumn([makeTask('failed'), makeTask('failed')])).toBe('failed')
    expect(derivePlanColumn([makeTask('done'), makeTask('failed')])).toBe('failed')
  })

  it('rule 4 precedence — in_progress beats blocked/planning/next', () => {
    expect(
      derivePlanColumn([makeTask('next'), makeTask('planning'), makeTask('blocked'), makeTask('in_progress')]),
    ).toBe('in_progress')
  })

  it('rule 4 precedence — blocked beats planning/next when no in_progress', () => {
    expect(derivePlanColumn([makeTask('next'), makeTask('planning'), makeTask('blocked')])).toBe('blocked')
  })

  it('rule 4 precedence — planning beats next when no in_progress/blocked', () => {
    expect(derivePlanColumn([makeTask('next'), makeTask('planning')])).toBe('planning')
  })

  it('rule 4 precedence — next when only next (plus non-terminal inbox) present', () => {
    expect(derivePlanColumn([makeTask('inbox'), makeTask('next')])).toBe('next')
    expect(derivePlanColumn([makeTask('next')])).toBe('next')
  })

  it('rule 4 fallback — inbox when nothing else matches (all inbox, or a non-terminal done+inbox mix)', () => {
    expect(derivePlanColumn([makeTask('inbox')])).toBe('inbox')
    expect(derivePlanColumn([makeTask('done'), makeTask('inbox')])).toBe('inbox')
  })

  it('a done+failed+in_progress mix is NOT "all terminal" — falls through to precedence (in_progress), not failed', () => {
    expect(derivePlanColumn([makeTask('done'), makeTask('failed'), makeTask('in_progress')])).toBe('in_progress')
  })
})
