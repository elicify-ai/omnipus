import { describe, it, expect } from 'vitest'
import { planDependencyReconnect } from './dependencyReconnect'
import type { Task } from '@/lib/api'

type T = Pick<Task, 'id' | 'plan_id' | 'blocked_by'>

const tasks: T[] = [
  { id: 'A', plan_id: 'p1', blocked_by: [] },
  { id: 'B', plan_id: 'p1', blocked_by: ['A'] }, // A → B exists
  { id: 'C', plan_id: 'p1', blocked_by: [] },
  { id: 'X', plan_id: 'p2', blocked_by: [] }, // different plan
]

describe('planDependencyReconnect', () => {
  it('moves the BLOCKED endpoint to another same-plan task (two PUTs: drop old, add new)', () => {
    // Drag A→B target end from B onto C ⇒ A→C
    const plan = planDependencyReconnect(tasks, { blocker: 'A', blocked: 'B' }, { blocker: 'A', blocked: 'C' })
    expect(plan.error).toBeUndefined()
    expect(plan.puts).toEqual([
      { taskId: 'B', blockedBy: [] }, // A removed from B
      { taskId: 'C', blockedBy: ['A'] }, // A added to C
    ])
  })

  it('moves the BLOCKER endpoint on the same blocked task (one atomic PUT)', () => {
    // Drag A→B source end from A onto C ⇒ C→B (B.blocked_by: A → C)
    const plan = planDependencyReconnect(tasks, { blocker: 'A', blocked: 'B' }, { blocker: 'C', blocked: 'B' })
    expect(plan.error).toBeUndefined()
    expect(plan.puts).toEqual([{ taskId: 'B', blockedBy: ['C'] }])
  })

  it('rejects a cross-plan reconnection with same-plan (no PUTs)', () => {
    const plan = planDependencyReconnect(tasks, { blocker: 'A', blocked: 'B' }, { blocker: 'A', blocked: 'X' })
    expect(plan.error).toBe('same-plan')
    expect(plan.puts).toEqual([])
  })

  it('rejects a self-link as invalid', () => {
    const plan = planDependencyReconnect(tasks, { blocker: 'A', blocked: 'B' }, { blocker: 'C', blocked: 'C' })
    expect(plan.error).toBe('invalid')
    expect(plan.puts).toEqual([])
  })

  it('rejects an unknown task as invalid', () => {
    const plan = planDependencyReconnect(tasks, { blocker: 'A', blocked: 'B' }, { blocker: 'A', blocked: 'ZZZ' })
    expect(plan.error).toBe('invalid')
    expect(plan.puts).toEqual([])
  })

  it('treats an already-existing target link as a no-op (invalid, no PUTs)', () => {
    // Reconnect A→B's blocker end onto A again (B already blocked_by A)
    const plan = planDependencyReconnect(tasks, { blocker: 'A', blocked: 'B' }, { blocker: 'A', blocked: 'B' })
    expect(plan.puts).toEqual([])
    expect(plan.error).toBe('invalid')
  })

  it('normalizes plan-less tasks (null/undefined) as the same "Loose" group', () => {
    const loose: T[] = [
      { id: 'L1', plan_id: undefined, blocked_by: [] },
      { id: 'L2', blocked_by: ['L1'] } as T,
      { id: 'L3', plan_id: '', blocked_by: [] },
    ]
    const plan = planDependencyReconnect(loose, { blocker: 'L1', blocked: 'L2' }, { blocker: 'L1', blocked: 'L3' })
    expect(plan.error).toBeUndefined()
    expect(plan.puts).toEqual([
      { taskId: 'L2', blockedBy: [] },
      { taskId: 'L3', blockedBy: ['L1'] },
    ])
  })
})
