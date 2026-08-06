// taskRuns — shared types/helpers for the ADR-050 per-task run-history
// surfaces (task-run-history-spec.md §4.3/§4.4).
//
// `TaskRunOccurrenceContext` folds a calendar occurrence's instant together
// with its resolved run (M7 fix): TaskRunStatusField, TaskResultField, and
// OpenInChatButton previously each took an INDEPENDENT `run?: TaskRun` prop
// (plus `occurrenceMs?` on TaskRunStatusField alone) with no enforced
// pairing — nothing stopped a caller from passing one occurrence's `run` next
// to a different occurrence's `occurrenceMs`. Folding them into one prop,
// built ONCE by the slide-over and passed identically to all three siblings,
// makes that mismatch unrepresentable.

import type { Task, TaskRun } from '@/lib/api'

export interface TaskRunOccurrenceContext { // not-wire-format: internal UI prop-grouping (occurrence instant + its already-resolved TaskRun); never serialized or sent across the gateway/SPA boundary
  /** The scheduled occurrence instant (ms) this field/row is scoped to. */
  ms: number
  /** The resolved run for that occurrence, when one exists (ADR-050 RD8). */
  run?: TaskRun
}

/**
 * Whether a task/run's result should be rendered — the resolved status has
 * reached a terminal outcome (done/failed) AND actually carries a result
 * string. Shared by TaskResultField (task/run) and TaskRunsList (run rows)
 * so the "does this row have a result to show" predicate can't drift between
 * the two surfaces (Q3 dedup).
 */
export function hasVisibleResult(
  status: Task['status'] | TaskRun['status'],
  result: string | null | undefined,
): boolean {
  return (status === 'done' || status === 'failed') && !!result
}
