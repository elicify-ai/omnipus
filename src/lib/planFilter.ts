// Pure Board/List filter functions for the Plan + tag filter bar (ADR-049,
// SD-C2). Replaces `filterByMilestone` (BoardView.tsx:241-247 pre-removal).
//
// Two independent sentinel filters that COMPOSE (AND, per SD-C2 / edge case
// "a task belonging to a plan AND carrying tags"):
//   - Plan filter: `PLAN_FILTER_ALL` (null) = All plans; a plan id = that
//     plan's dependency chain only.
//   - Tag filter: `null` = no tag filter; `PLAN_FILTER_UNTAGGED` = tasks with
//     zero tags; a tag string = tasks carrying that tag.

import type { Task } from '@/lib/api'

/** Sentinel: no plan filter active ("All"). */
export const PLAN_FILTER_ALL = null

/** Sentinel: the "Untagged" tag-filter pill (tasks with zero tags). */
export const PLAN_FILTER_UNTAGGED = '__untagged__'

/** Filter tasks down to a single plan's dependency chain (or all, when `activePlanId` is `PLAN_FILTER_ALL`). */
export function filterByPlan(tasks: Task[], activePlanId: string | null): Task[] {
  if (activePlanId === PLAN_FILTER_ALL) return tasks
  return tasks.filter((t) => t.plan_id === activePlanId)
}

/** Filter tasks by tag (or the `PLAN_FILTER_UNTAGGED` sentinel for "no tags"), or pass through when `activeTagFilter` is `null`. */
export function filterByTag(tasks: Task[], activeTagFilter: string | null): Task[] {
  if (activeTagFilter === null) return tasks
  if (activeTagFilter === PLAN_FILTER_UNTAGGED) {
    return tasks.filter((t) => !t.tags || t.tags.length === 0)
  }
  return tasks.filter((t) => (t.tags ?? []).includes(activeTagFilter))
}

/** Compose the plan + tag filters (AND) — the single entry point Board/List call. */
export function filterByPlanAndTag(
  tasks: Task[],
  activePlanId: string | null,
  activeTagFilter: string | null,
): Task[] {
  return filterByTag(filterByPlan(tasks, activePlanId), activeTagFilter)
}

/** Every distinct tag across the given tasks, sorted for stable chip order (used by `PlanFilterBar`'s tag-chip row). */
export function distinctTags(tasks: Task[]): string[] {
  const set = new Set<string>()
  for (const t of tasks) {
    for (const tag of t.tags ?? []) set.add(tag)
  }
  return Array.from(set).sort()
}
