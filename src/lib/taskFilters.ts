// taskFilters.ts — ADR-051 D2/D6 (Tasks Screen: Plans-as-Filter).
//
// Pure, framework-free filter helper for the combined task board. The Plans
// band (D2) filters by plan; the board toolbar's Owner dropdown (D6) filters
// by owning agent. The two stack as an AND (Plan filter AND Owner filter),
// entirely client-side over the already-loaded task list — no new endpoint.

import type { Task } from '@/lib/api'

export interface TaskFilter { // not-wire-format: client-side board filter UI state, never crosses the gateway/SPA boundary
  /** `null` = no plan filter (all plans + plan-less tasks). Otherwise keep only tasks whose `plan_id` matches. */
  planId: string | null
  /** `null` = no owner filter. Otherwise keep only tasks whose `agent_id` matches. */
  ownerAgentId: string | null
}

/**
 * Filter `tasks` by the given `TaskFilter`. Both fields `null` returns every
 * task (unfiltered, in a new array); either or both set narrows via AND
 * (never OR) — a task must satisfy every active filter to be kept. Pure:
 * never mutates `tasks` or its elements; `Array.prototype.filter` always
 * returns a fresh array.
 */
export function filterTasks(tasks: Task[], f: TaskFilter): Task[] {
  return tasks.filter((task) => {
    if (f.planId !== null && task.plan_id !== f.planId) return false
    if (f.ownerAgentId !== null && task.agent_id !== f.ownerAgentId) return false
    return true
  })
}
