/**
 * taskValidationError.ts
 *
 * Shared by every task-mutation call site that needs to route a server
 * rejection to the specific control it names, instead of a single generic
 * banner — currently CreateTaskSlideOver's Create/Create & Run (POST
 * /api/v1/tasks). One recognizer, not a copy per component — see the
 * live-UAT defects this closes:
 *
 *   - A-11: a duplicate definition-of-done item rejected the Create dialog
 *     with no visible message.
 *   - E-10: a dependency cycle rejected the "Depends on" editor's PUT with
 *     no visible message — the checkbox just failed to apply.
 *
 * The DoD-distinctness rule and the blocked_by cycle guard are backend
 * rules (pkg/task/dod_distinct.go, pkg/task/blocked_by.go's cycle check) —
 * the SPA is never the authority for either and must not reimplement them.
 * The MESSAGE TEXT displayed is always the server's, verbatim, and is now
 * plain, human-facing prose with no machine prefix to parse (fix wave:
 * task-validation-message-register — a raw "task validation: dod[0]: ..."
 * string used to reach the dialog unedited). This file only recognizes
 * WHICH control to route that text to, via the structured
 * `field` property on the server's `ErrorResponse`
 * (`contracts/components/schemas/ErrorResponse.yaml`) — the SAME channel
 * `jsonErrField`/`ApiError.field` already carry elsewhere (ADR-068) — never
 * by parsing the message.
 */

import { isApiError } from '@/lib/api'

export type TaskValidationErrorField = 'criteria' | 'dod' | 'blocked_by'

const RECOGNIZED_FIELDS: readonly TaskValidationErrorField[] = ['criteria', 'dod', 'blocked_by']

/**
 * Identifies which control a task-validation rejection names, from the
 * server's structured `field` property (never from the message text — the
 * message is plain prose meant for a reader, not a machine format). Returns
 * null when the error carries no recognized field (e.g. a workspace-
 * membership rejection, a 5xx, a network failure, or a non-ApiError) —
 * callers fall back to a generic banner/toast for that case.
 */
export function fieldFromValidationError(err: unknown): TaskValidationErrorField | null {
  if (!isApiError(err) || !err.field) return null
  return (RECOGNIZED_FIELDS as readonly string[]).includes(err.field)
    ? (err.field as TaskValidationErrorField)
    : null
}
