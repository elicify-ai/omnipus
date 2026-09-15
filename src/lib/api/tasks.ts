// tasks.ts: Tasks, board moves, evidence, verdicts and run history

import { isApiError as isApiErrorFn, getErrorMessage } from '../api-error'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  Task as TaskSchema,
  // Per-task run history (ADR-050 / task-run-history-spec §4.1):
  TaskRun as TaskRunSchema,
  EvidenceRecord as EvidenceRecordSchema,
  JudgeVerdict as JudgeVerdictSchema,
} from '@/lib/api/generated/schemas'
import type {
  // New wire types (contract-first #8):
  Task,
  // Unified task types (Sprint 2) — imported once here (Task was already imported above):
  TaskCreateRequest,
  TaskUpdateRequest,
  Todo,
  // Per-task run history (ADR-050 / task-run-history-spec §4.1):
  TaskRun,
  RunNowRequest,
  // Planning & Goals (ADR-049, contract-first #8) — Plan container, task
  // acceptance criteria, evidence, and judge verdicts (replaces Milestones):
  Plan,
  EvidenceRecord,
  JudgeVerdict,
} from '@/lib/api/generated/openapi-types'
import { friendlyConflictError, request } from './http'

// ── Tasks (unified Sprint 2 model) ───────────────────────────────────────────
//
// One entity replaces both the legacy workflow Task and GTD BoardTask outright.
// All types are from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Task.yaml / TaskCreateRequest.yaml /
// TaskUpdateRequest.yaml.
//
// Endpoints:
//   GET    /tasks              → Task[]   (list, workspace-scoped)
//   POST   /tasks              → Task     (create, lands in inbox)
//   GET    /tasks/{id}         → Task
//   PATCH  /tasks/{id}         → Task     (partial update — method is PATCH)
//   DELETE /tasks/{id}         → void
//   GET    /tasks/{id}/subtasks → Task[]
//   PUT    /tasks/{id}/todos    → Task     (replace checklist atomically)
//   PUT    /tasks/{id}/dependencies → Task (replace blocked_by atomically)
//   POST   /tasks/{id}/stop     → Task     (Stop/Clear — `stopTask`, ADR-052)
//   POST   /tasks/{id}/restart  → Task | 409 (▶ Play a Stopped task — `restartTask`, ADR-052 FR-026)
//
// "Start"/"Run" semantics: there is no /start or /run endpoint for a
// standalone task (`run_task` on the wire is an AGENT tool, not a REST
// route — ADR-052 G4). Set status=in_progress via PATCH to start/run one
// (drag, the board's Run button, or `runTask` below).

export const tasksQueryKeys = {
  list: (params?: { workspace_id?: string; status?: string; agent_id?: string; plan_id?: string; surface?: string }) => {
    const cleaned = params
      ? Object.fromEntries(Object.entries(params).filter(([, v]) => v !== undefined))
      : {}
    return ['tasks', cleaned] as const
  },
  detail: (id: string) => ['tasks', id] as const,
  subtasks: (id: string) => ['tasks', id, 'subtasks'] as const,
  // Per-task run history (ADR-050 / task-run-history-spec §4.1) — invalidated
  // by the task_run_status WS frame handler (src/store/chat.ts) so the
  // calendar slide-over and TaskDetailPanel's Runs list update live.
  runs: (id: string) => ['tasks', id, 'runs'] as const,
}

// Keep boardTasksQueryKeys as an alias so tests and existing queries still compile
// during the transition — it redirects to the same unified key space.
export const boardTasksQueryKeys = tasksQueryKeys

export function fetchTasks(params?: { workspace_id?: string; status?: string; agent_id?: string; plan_id?: string; surface?: string }): Promise<Task[]> {
  const search = new URLSearchParams()
  if (params?.workspace_id) search.set('workspace_id', params.workspace_id)
  if (params?.status) search.set('status', params.status)
  if (params?.agent_id) search.set('agent_id', params.agent_id)
  if (params?.plan_id) search.set('plan_id', params.plan_id)
  if (params?.surface) search.set('surface', params.surface)
  const qs = search.toString() ? '?' + search.toString() : ''
  return request<Task[]>(`/tasks${qs}`, undefined, z.array(TaskSchema) as ZodType<Task[]>)
}

// Keep fetchBoardTasks as an alias so existing call-sites compile during transition.
export function fetchBoardTasks(params?: { workspace_id?: string; status?: string; agent_id?: string; plan_id?: string }): Promise<Task[]> {
  return fetchTasks(params)
}

export function fetchTask(id: string): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}`, undefined, TaskSchema as ZodType<Task>)
}

export function fetchSubtasks(taskId: string): Promise<Task[]> {
  return request<Task[]>(`/tasks/${encodeURIComponent(taskId)}/subtasks`, undefined, z.array(TaskSchema) as ZodType<Task[]>)
}

export function createTask(body: TaskCreateRequest): Promise<Task> {
  return request<Task>('/tasks', { method: 'POST', body: JSON.stringify(body) }, TaskSchema as ZodType<Task>)
}

export function updateTask(id: string, data: TaskUpdateRequest): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(data) }, TaskSchema as ZodType<Task>)
}

export function setTaskTodos(taskId: string, todos: Todo[]): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(taskId)}/todos`, { method: 'PUT', body: JSON.stringify(todos) }, TaskSchema as ZodType<Task>)
}

export function setTaskDependencies(taskId: string, blockedBy: string[]): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(taskId)}/dependencies`, { method: 'PUT', body: JSON.stringify(blockedBy) }, TaskSchema as ZodType<Task>)
}

export function deleteTask(id: string): Promise<void> {
  return request<void>(`/tasks/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/**
 * Stop/Clear (D8) a task's own goal loop — POST /tasks/{id}/stop (ADR-049 —
 * "clear affordances at every level", distinct from `stopPlan`, which stops
 * a Plan's loop; ADR-052 FR-010/FR-022/US-7 — `RequestCancelForSession`
 * reaches the worker turn + its subagents + its shells, the same chat
 * cancel cascade `handleTaskStop` already uses).
 */
export function stopTask(id: string): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}/stop`, { method: 'POST' }, TaskSchema as ZodType<Task>)
}

/** @deprecated Use `stopTask` — kept as an alias for callers not yet swept (e.g. `TaskDetailPanel.tsx`). Same signature/behavior. */
export const stopTaskGoalLoop = stopTask

/**
 * Restart (▶ Play) a standalone task previously Stopped by the user — POST
 * /tasks/{id}/restart (ADR-052 FR-026/US-9/US-10). Resets `attempt_count` to
 * 0, clears `cancel_reason`, and transitions the task to `next` so the goal
 * loop picks it up again and drives the FULL attempt loop (run -> judge ->
 * retry to the limit — same as `run_task` / a plan member, A3).
 *
 * A `409` means "not restartable": the task belongs to a plan (an in-plan
 * member restarts only via its plan — restart the plan instead, `restartPlan`),
 * or the task isn't `failed`, or its `cancel_reason` isn't `stopped_by_user`.
 * Rewritten into a specific, actionable message here rather than the generic
 * "conflicts with current state" default.
 */
export async function restartTask(id: string): Promise<Task> {
  try {
    return await request<Task>(`/tasks/${encodeURIComponent(id)}/restart`, { method: 'POST' }, TaskSchema as ZodType<Task>)
  } catch (err) {
    throw friendlyConflictError(
      err,
      "This task is not restartable — it belongs to a plan (restart the plan instead), or wasn't Stopped by a user.",
    )
  }
}

/**
 * Run a standalone task now (▶ Play on an idle task — ADR-052 FR-019/G4).
 *
 * There is NO dedicated REST route for this: `run_task` on the wire is an
 * AGENT tool only (verified against the generated contract — no
 * `/tasks/{id}/run` operation exists in `src/lib/api/generated/openapi-types.ts`).
 * The UI's "run now" path is the SAME one the board's drag-to-`in_progress`
 * move and `CreateTaskSlideOver`'s "Create & Run" already use ("Start"
 * semantics, see the Tasks section header above): PATCH the task to
 * `status: 'in_progress'`. The engine's goal loop then drives the task
 * through the FULL attempt loop (run -> judge -> retry to the limit),
 * identical to `run_task` / a plan member (A3) — this is not a lesser,
 * single-shot run.
 *
 * The engine — not this call — rejects an in-plan member (G4: in-plan tasks
 * start only via their plan); a `409` from that case is rewritten into a
 * friendlier "not runnable" message rather than the generic conflict default.
 */
export async function runTask(id: string): Promise<Task> {
  try {
    return await updateTask(id, { status: 'in_progress' })
  } catch (err) {
    throw friendlyConflictError(
      err,
      "This task can't be run right now — it may already be running, or be an in-plan member (its plan drives its start, not a standalone run).",
    )
  }
}

// ── Board drag-to-column move: 409 message mapping (UAT round-2 N1/N2) ─────
//
// The board's drag-to-column move (BoardView.tsx -> WorkspaceTasksTab.tsx's
// moveMutation) PATCHes `status` straight through `updateTask` above — it
// does NOT go through `runTask`'s `friendlyConflictError` wrapper, so a plan
// member dragged into `in_progress` hit the generic
// `ApiError.fromResponse` 409 default ("This conflicts with the current
// state. Please refresh and try again.") verbatim. That default is actively
// WRONG for this endpoint's most common 409 cause (see below) — refreshing
// can never fix it — so this is a dedicated mapper, not a reuse of
// `friendlyConflictError` (whose single hardcoded string-per-endpoint
// shape can't express "different message per plan state").
//
// pkg/gateway/rest_tasks.go's handleTaskPatch returns 409 from exactly two
// call sites (verified by reading the handler, not assumed) when the PATCH
// sets `status: in_progress` and StartTaskNow then fails:
//   1. `agent.ErrDispatchCapReached` — the global dispatch semaphore is
//      full. Genuinely transient congestion; retrying shortly can help.
//   2. `agent.ErrPlanNotExecuting` / `agent.ErrPlanStateUnresolvable` — the
//      S1 plan-state gate (pkg/agent/task_executor.go's
//      `requirePlanExecuting`, commit 5d77f26a) refusing dispatch because
//      the task's parent plan isn't `approved`/`running`-and-unpaused.
//      Refreshing NEVER helps here — the plan itself has to change state
//      (Execute a draft, restart an eligible stopped plan, or re-enable a
//      disabled owner agent to clear a pause).
// `pkg/task/store.go`'s plain `Update` (what this PATCH ultimately calls)
// has no optimistic-concurrency/version check at all — illegal lifecycle
// transitions there resolve to `task.ErrValidation` (400 Bad Request via
// `isTaskValidationErr`), never 409 — so there is currently no THIRD,
// generic-concurrency 409 cause on this endpoint to confuse with the above
// two. (`src/lib/queryClient.ts`'s retry-exclusion comment already
// documents this same overload for the RETRY question — that finding
// stands; nothing here changes retry behavior, only display text, and both
// causes are read from the same plain-text `{"error": string}` body that
// comment says has "no machine-readable field distinguishing the two
// cases" — true for a structured/typed field, but the two sentinel error
// strings ARE textually distinguishable, which is all a display mapper
// needs.)
//
// Exported (not `friendlyConflictError`-private) because BOTH the toast
// (WorkspaceTasksTab's moveMutation.onError) and the screen-reader live
// region (BoardView's own post-drop announcement) need the IDENTICAL text —
// a screen-reader user must never hear a different reason than the sighted
// toast shows for the same rejected drop.
export function describeTaskMoveConflict(err: unknown, plans: Plan[]): string | undefined {
  if (!isApiErrorFn(err) || err.status !== 409 || !err.body) return undefined

  let raw = err.body
  try {
    const parsed = JSON.parse(err.body) as { error?: unknown; message?: unknown }
    if (typeof parsed.error === 'string') raw = parsed.error
    else if (typeof parsed.message === 'string') raw = parsed.message
  } catch {
    // Not JSON (H3-FE already guards the oversized/binary cases upstream in
    // ApiError.fromResponse) — fall back to the raw text as-is.
  }

  if (raw.includes('global dispatch cap reached')) {
    return 'Too many tasks are starting at once — the server is at its dispatch limit. Try moving this task again in a moment.'
  }

  // Mirrors task_executor.go's ErrPlanNotExecuting wrap exactly:
  //   "...parent plan is not in a dispatchable state (approved/running, unpaused): plan "<id>" is <state> (paused_reason="<reason>")"
  const gateMatch = raw.match(
    /parent plan is not in a dispatchable state.*?: plan "([^"]*)" is (\w+) \(paused_reason="([^"]*)"\)/,
  )
  if (gateMatch) {
    const [, planId, state, pausedReason] = gateMatch
    // PermitsMemberDispatch (pkg/plan/plan.go) checks PausedReason FIRST,
    // before State — a paused plan is refused regardless of state, so this
    // takes precedence here too.
    if (pausedReason) {
      if (pausedReason === 'owner_disabled') {
        return "This plan is paused because its owner agent is disabled — re-enable the agent to resume this plan's tasks."
      }
      return `This plan is paused (${pausedReason}) — resolve that before this task can run.`
    }
    switch (state) {
      case 'draft':
        return 'This plan is still a draft — Execute it (from the Plans band above) before this task can run.'
      case 'done':
        return "This plan has already finished — its tasks can't be started this way."
      case 'failed': {
        // Cross-reference the already-loaded plans list (BoardView already
        // receives it) for `failed_reason` — the gate's own message doesn't
        // carry it, and it's the difference between "Restart the plan" (a
        // real, offered action for `stopped_by_user`) and "not restartable"
        // (every other failure reason — PlanActionButton offers no restart
        // for those either, US-9 Acceptance 2).
        const plan = plans.find((p) => p.id === planId)
        return plan?.failed_reason === 'stopped_by_user'
          ? 'This plan was stopped — Restart it (from the Plans band above) before this task can run.'
          : "This plan has failed and can't be restarted — its tasks can no longer run."
      }
      default:
        // A future 6th plan state the client doesn't know about yet — fall
        // through to the generic-but-honest 409 fallback below rather than
        // guessing at a state-specific message we can't stand behind.
        return undefined
    }
  }

  // Mirrors ErrPlanStateUnresolvable's wrap: "...parent plan's state could not be verified: plan "<id>": <err>"
  if (raw.includes("parent plan's state could not be verified")) {
    return "This plan's current state couldn't be verified — try moving this task again in a moment."
  }

  return undefined
}

/**
 * The single message both the move-conflict toast (WorkspaceTasksTab) and
 * the drag-and-drop live-region announcement (BoardView) render for a failed
 * board move — `describeTaskMoveConflict`'s specific mapping when it
 * recognizes the 409 body, else an honest, non-committal fallback that never
 * repeats `ApiError`'s generic 409 default ("refresh and try again") since
 * that claim is exactly what's false for the plan-gate case above. Non-409
 * errors (500s, network failures, etc.) fall through to the ordinary
 * `getErrorMessage` priority (ApiError.userMessage > Error.message >
 * fallback) unchanged.
 */
export function taskMoveErrorMessage(err: unknown, plans: Plan[]): string {
  const specific = describeTaskMoveConflict(err, plans)
  if (specific) return specific
  if (isApiErrorFn(err) && err.status === 409) {
    return 'This move was rejected by the server — the task or its plan may be in a state that does not allow it right now.'
  }
  return getErrorMessage(err, 'Failed to move task')
}

// ── Task evidence & judge verdicts (ADR-049 D2, Planning & Goals) ───────────
//
// Read-only surfaces backing the acceptance-criteria editor's evidence viewer
// and per-attempt verdict list. See contracts/components/schemas/EvidenceRecord.yaml
// / JudgeVerdict.yaml (contract rows C10/C11).

export const taskEvidenceQueryKeys = {
  list: (taskId: string) => ['tasks', taskId, 'evidence'] as const,
}

export const taskVerdictsQueryKeys = {
  list: (taskId: string) => ['tasks', taskId, 'verdicts'] as const,
}

export function fetchTaskEvidence(taskId: string): Promise<EvidenceRecord[]> {
  return request<EvidenceRecord[]>(
    `/tasks/${encodeURIComponent(taskId)}/evidence`,
    undefined,
    z.array(EvidenceRecordSchema) as ZodType<EvidenceRecord[]>,
  )
}

export function fetchTaskVerdicts(taskId: string): Promise<JudgeVerdict[]> {
  return request<JudgeVerdict[]>(
    `/tasks/${encodeURIComponent(taskId)}/verdicts`,
    undefined,
    z.array(JudgeVerdictSchema) as ZodType<JudgeVerdict[]>,
  )
}

// ── Per-task run history (ADR-050 / task-run-history-spec §4.1) ────────────────
//
// TaskRun is a purely additive execution-record layer — Task.status/result/
// session_id keep their existing behaviour unchanged. GET /tasks/{id}/runs is
// the authoritative history list (retention-bounded, newest first, full
// result strings); POST /tasks/{id}/runs ("Run now") opens + dispatches a new
// run and returns 202 (fire-and-forget — observe progress via the
// task_run_status WS frame, see src/store/chat.ts, or by refetching this
// list). Foundation for the calendar slide-over + TaskDetailPanel Runs
// section (both consume the shared TaskRunsList component).

export function fetchTaskRuns(taskId: string): Promise<TaskRun[]> {
  return request<TaskRun[]>(`/tasks/${encodeURIComponent(taskId)}/runs`, undefined, z.array(TaskRunSchema) as ZodType<TaskRun[]>)
}

/**
 * POST /tasks/{id}/runs ("Run now", ADR-050 RD7).
 *
 * - `occurrenceMs` provided (including explicit `null`) → body carries
 *   `{occurrence_ms}`: materializes/re-runs that specific recurring
 *   occurrence (idempotent against a concurrent scheduler fire for the same
 *   instant).
 * - `occurrenceMs` omitted (`undefined`) → empty body: re-runs a normal/once
 *   task as a fresh run: the prior run (if any) is preserved in the run
 *   history, not overwritten.
 *
 * Returns 202 with no body — the run executes asynchronously; no response
 * schema to validate (request() resolves `undefined` for a schema-less,
 * bodyless 2xx).
 */
export function runTaskNow(taskId: string, occurrenceMs?: number | null): Promise<void> {
  const init: RequestInit = { method: 'POST' }
  if (occurrenceMs !== undefined) {
    const body: RunNowRequest = { occurrence_ms: occurrenceMs }
    init.body = JSON.stringify(body)
  }
  return request<void>(`/tasks/${encodeURIComponent(taskId)}/runs`, init)
}
