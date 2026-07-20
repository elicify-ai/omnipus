// Pure Board/List tag-filter helpers (ADR-049, SD-C2). The plan half of this
// module (a `PLAN_FILTER_ALL`/`filterByPlan`/`filterByPlanAndTag` sentinel
// filter) was removed with the Plan Swimlane board. Plan filtering was later
// REINSTATED — but as a first-class task filter (`filterTasks` in
// `taskFilters.ts`, ADR-051 D2/D6) driven by `PlansFilterBand`, not here:
// swimlane bands are gone and BoardView is a flat kanban. Only the tag filter
// survives in this module, driving the toolbar's tag multiselect
// (`TagFilterMultiSelect` in `WorkspaceTasksTab.tsx`) via `filterByTags`.

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

/**
 * Multi-tag filter (ADR-051 toolbar — the tag filter is a multiselect). An
 * empty selection passes everything through; otherwise a task is kept if it
 * matches ANY selected tag (union/OR), where the `PLAN_FILTER_UNTAGGED`
 * sentinel matches tasks with zero tags. Pure — always returns a new array.
 */
export function filterByTags(tasks: Task[], activeTags: string[]): Task[] {
  if (activeTags.length === 0) return [...tasks]
  const wantUntagged = activeTags.includes(PLAN_FILTER_UNTAGGED)
  const wanted = new Set(activeTags.filter((t) => t !== PLAN_FILTER_UNTAGGED))
  return tasks.filter((t) => {
    const tags = t.tags ?? []
    if (wantUntagged && tags.length === 0) return true
    return tags.some((tag) => wanted.has(tag))
  })
}

/** Every distinct tag across the given tasks, sorted for stable order (used by the toolbar tag multiselect). */
export function distinctTags(tasks: Task[]): string[] {
  const set = new Set<string>()
  for (const t of tasks) {
    for (const tag of t.tags ?? []) set.add(tag)
  }
  return Array.from(set).sort()
}
