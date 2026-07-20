// Pure Board/List tag-filter helpers (ADR-049, SD-C2). The plan half of this
// module (a `PLAN_FILTER_ALL`/`filterByPlan`/`filterByPlanAndTag` sentinel
// filter) was removed by the Plan Swimlane board redesign — the Board no
// longer filters by plan, it groups ALL plans into swimlane bands instead
// (see BoardView.tsx). Only the tag filter survives as a toolbar control
// (`TagFilterBar.tsx`).

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
