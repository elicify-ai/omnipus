import type { Task } from '@/lib/api'

/** Order-insensitive equality for two dependency-id lists. */
function sameSet(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((x) => b.includes(x))
}

/** One end of a dependency edge: `blocker` must finish before `blocked`. */
export interface DepEndpoint {
  blocker: string
  blocked: string
}

/** A single atomic `PUT /tasks/{taskId}/dependencies` replacing its blocked set. */
export interface DepPut {
  taskId: string
  blockedBy: string[]
}

export interface DepReconnectPlan {
  /** The PUT(s) to apply. Empty when the move is a no-op or rejected. */
  puts: DepPut[]
  /**
   * Why no PUTs, when applicable:
   * - `same-plan` — the new endpoint pair crosses plans (show a toast).
   * - `invalid`   — self-link, missing task, or a no-op (silently revert).
   */
  error?: 'same-plan' | 'invalid'
}

/**
 * Pure planner for a graph edge RECONNECTION (drag one endpoint of an existing
 * dependency onto another task). Returns the exact `PUT`s to persist, or an
 * error the caller surfaces. Kept pure (no React, no network) so the tricky
 * branch logic is unit-tested directly.
 *
 * Cases:
 *  - Only the BLOCKER endpoint moved (same blocked task) → ONE PUT on that task
 *    (swap old blocker for new in its `blocked_by`).
 *  - The BLOCKED endpoint moved to a different task → TWO PUTs: drop the edge
 *    from the old blocked task, add it to the new one.
 *  - New pair is cross-plan → `same-plan` (a dependency must stay in one plan).
 *  - Self-link / unknown task / already-linked → `invalid` (no-op; caller
 *    re-syncs the canvas to server truth).
 */
export function planDependencyReconnect(
  tasks: Pick<Task, 'id' | 'plan_id' | 'blocked_by'>[],
  oldDep: DepEndpoint,
  newDep: DepEndpoint,
): DepReconnectPlan {
  const { blocker: ob, blocked: obk } = oldDep
  const { blocker: nb, blocked: nbk } = newDep

  if (!nb || !nbk || nb === nbk) return { puts: [], error: 'invalid' }

  const byId = new Map(tasks.map((t) => [t.id, t]))
  const newBlocker = byId.get(nb)
  const newBlocked = byId.get(nbk)
  if (!newBlocker || !newBlocked) return { puts: [], error: 'invalid' }

  if ((newBlocker.plan_id || null) !== (newBlocked.plan_id || null)) {
    return { puts: [], error: 'same-plan' }
  }

  // Only the blocker endpoint moved → one atomic PUT on the shared blocked task:
  // swap the old blocker out, add the new one. A drop that leaves the set
  // unchanged (e.g. dropped back on the same handle) is a no-op.
  if (obk === nbk) {
    const original = byId.get(obk)?.blocked_by ?? []
    const next = original.filter((x) => x !== ob)
    if (!next.includes(nb)) next.push(nb)
    if (sameSet(next, original)) return { puts: [], error: 'invalid' }
    return { puts: [{ taskId: obk, blockedBy: next }] }
  }

  // The blocked endpoint moved to a different task → drop from old, add to new.
  const puts: DepPut[] = []
  const oldBlocked = byId.get(obk)
  if (oldBlocked && (oldBlocked.blocked_by ?? []).includes(ob)) {
    puts.push({ taskId: obk, blockedBy: (oldBlocked.blocked_by ?? []).filter((x) => x !== ob) })
  }
  const newCurrent = newBlocked.blocked_by ?? []
  if (!newCurrent.includes(nb)) {
    puts.push({ taskId: nbk, blockedBy: [...newCurrent, nb] })
  }
  return puts.length > 0 ? { puts } : { puts: [], error: 'invalid' }
}
