/**
 * taskValidationError.ts
 *
 * Shared by every task-mutation call site that needs to route a server
 * rejection to the specific control it names, instead of a single generic
 * banner — currently CreateTaskSlideOver's Create/Create & Run (POST
 * /api/v1/tasks) and TaskDetailPanel's dependency editor (PUT
 * /api/v1/tasks/{id}/dependencies). One recognizer, not a copy per
 * component — see the live-UAT defects this closes:
 *
 *   - A-11: a duplicate definition-of-done item ("dod[N]: ... restates the
 *     acceptance criterion ...", GOAL-FR-021/FR-048) rejected the Create
 *     dialog with no visible message.
 *   - E-10: a dependency cycle ("blocked_by cycle detected: ...") rejected
 *     the "Depends on" editor's PUT with no visible message — the checkbox
 *     just failed to apply.
 *
 * GOAL-FR-021/FR-048 and the blocked_by cycle guard are backend rules
 * (pkg/task/dod_distinct.go, pkg/task/*.go's cycle check) — the SPA is
 * never the authority for either and must not reimplement them. This file
 * only recognizes the SHAPE of the server's own message well enough to
 * decide where to display it; the message text displayed is always the
 * server's, verbatim (via `getErrorMessage` from '@/lib/api').
 */

export type TaskValidationErrorField = 'criteria' | 'dod' | 'blocked_by'

// pkg/task/store.go's `ErrValidation = errors.New("task validation")` +
// `verr()` wraps every per-item/per-edge task validator into
// "task validation: <detail>". Per-item validators (pkg/task/criterion.go,
// pkg/task/dod_distinct.go) name the offending item as `criteria[N]: ...` /
// `dod[N]: ...`; the dependency-cycle guard instead states
// "blocked_by cycle detected: ...". Both prefixes are matched literally
// rather than parsed further — the SPA only needs to know WHICH control to
// show the message next to, never anything about the message's content.
const FIELD_VALIDATION_ERROR_RE = /task validation: (criteria|dod)\[\d+\]:/
const CYCLE_VALIDATION_ERROR_RE = /task validation: blocked_by cycle detected:/

/**
 * Identifies which control a task-validation rejection names, from the
 * server's own message text. Returns null when the message names no
 * specific control (e.g. a workspace-membership rejection, a 5xx, or a
 * network failure) — callers fall back to a generic banner/toast for that
 * case.
 */
export function fieldFromValidationError(message: string): TaskValidationErrorField | null {
  const fieldMatch = FIELD_VALIDATION_ERROR_RE.exec(message)
  if (fieldMatch) return fieldMatch[1] === 'dod' ? 'dod' : 'criteria'
  if (CYCLE_VALIDATION_ERROR_RE.test(message)) return 'blocked_by'
  return null
}
