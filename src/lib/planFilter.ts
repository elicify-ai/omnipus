// Pure Board/List tag-filter helpers (ADR-049, SD-C2). The plan half of this
// module (a `PLAN_FILTER_ALL`/`filterByPlan`/`filterByPlanAndTag` sentinel
// filter) was removed with the Plan Swimlane board. Plan filtering was later
// REINSTATED — but as a first-class task filter (`filterTasks` in
// `taskFilters.ts`, ADR-051 D2/D6) driven by `PlansFilterBand`, not here:
// swimlane bands are gone and BoardView is a flat kanban. Only the tag filter
// survives in this module, as a toolbar control (`TagFilterBar.tsx`).

import type { Task } from '@/lib/api'

/** Sentinel: the "Untagged" tag-filter pill (tasks with zero tags). */
export const PLAN_FILTER_UNTAGGED = '__untagged__'

/** Filter tasks by tag (or the `PLAN_FILTER_UNTAGGED` sentinel for "no tags"), or pass through when `activeTagFilter` is `null`. */
export function filterByTag(tasks: Task[], activeTagFilter: string | null): Task[] {
  if (activeTagFilter === null) return tasks
  if (activeTagFilter === PLAN_FILTER_UNTAGGED) {
    return tasks.filter((t) => !t.tags || t.tags.length === 0)
  }
  return tasks.filter((t) => (t.tags ?? []).includes(activeTagFilter))
}

/** Every distinct tag across the given tasks, sorted for stable chip order (used by `TagFilterBar`'s tag-chip row). */
export function distinctTags(tasks: Task[]): string[] {
  const set = new Set<string>()
  for (const t of tasks) {
    for (const tag of t.tags ?? []) set.add(tag)
  }
  return Array.from(set).sort()
}
