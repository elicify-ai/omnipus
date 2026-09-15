// plans.ts: Plans — CRUD, execution, approval and the approve-task error parser

import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  // Planning & Goals (ADR-049, contract-first #8):
  Plan as PlanSchema,
  // ADR-052 Wave 2 — plan execute/restart 400 error body (contract-first #8):
  PlanApproveError as PlanApproveErrorSchema,
} from '@/lib/api/generated/schemas'
import type {
  // Planning & Goals (ADR-049, contract-first #8) — Plan container, task
  // acceptance criteria, evidence, and judge verdicts (replaces Milestones):
  Plan,
  PlanCreateRequest,
  PlanUpdateRequest,
  PlanListResponse,
  // ADR-052 Wave 2 — plan execute/restart 400 error body (contract-first #8):
  PlanApproveError,
} from '@/lib/api/generated/openapi-types'
import { friendlyConflictError, request } from './http'

// ── Plans (ADR-049 D1/FR-1 — replaces Milestones; ADR-052 Wave 2 — agent plan
// authoring & execution) ──────────────────────────────────────────────────
//
// A Plan is a first-class entity that groups an executable task DAG under a
// goal, Definition of Done, owner agent, and 5-value state machine
// (draft/approved/running/done/failed). Tasks join a plan via `Task.plan_id`
// (same-workspace FK). Membership + `progress` are computed read-time by the
// backend — never stored on the Plan record (mirrors the removed Milestone's
// computeMilestoneCounts). See contracts/components/schemas/Plan*.yaml.
//
// Endpoints:
//   GET    /workspaces/{id}/plans → PlanListResponse
//   POST   /workspaces/{id}/plans → Plan   (createWorkspacePlan — the ONLY
//                                           create route; bare POST /plans
//                                           deliberately 405s [rest_plans.go]
//                                           since creation/listing are
//                                           workspace-nested. `workspace_id`
//                                           is ALSO required in the body and
//                                           validated to match the path.)
//   PUT    /plans/{id}           → Plan   (partial update — title/goal/
//                                           description/owner/dod/bounds
//                                           ONLY. ADR-052 G2/FR-007: the SPA
//                                           MUST NEVER send `state` here —
//                                           PUT is not a gated transition
//                                           entry point [it skips both the
//                                           FR-084 criteria gate and the
//                                           cap-16 admission check]. The
//                                           single gated entry point into
//                                           `approved` is POST .../approve.)
//   POST   /plans/{id}/approve   → Plan | 400 PlanApproveError
//                                           (executePlan — ADR-052 FR-003;
//                                           the ONLY path draft->approved
//                                           takes; the engine then promotes
//                                           approved->running under the cap
//                                           on its own tick)
//   POST   /plans/{id}/stop      → Plan   (Stop/Clear a running plan, D8)
//   POST   /plans/{id}/restart   → Plan | 409
//                                           (restartPlan — ADR-052 FR-026,
//                                           the ▶ Play route for a plan
//                                           `failed`+`stopped_by_user`)
//   DELETE /plans/{id}           → void   (rejected 400/409 while running)

export const plansQueryKeys = {
  list: (workspaceId: string) => ['plans', workspaceId] as const,
  detail: (workspaceId: string, planId: string) => ['plans', workspaceId, planId] as const,
}

const PlanListResponseSchema = z.object({
  plans: z.array(PlanSchema),
  total: z.number().int(),
})

export function fetchPlans(workspaceId: string): Promise<Plan[]> {
  return request<PlanListResponse>(
    `/workspaces/${encodeURIComponent(workspaceId)}/plans`,
    undefined,
    PlanListResponseSchema as ZodType<PlanListResponse>,
  ).then((res) => res.plans)
}

export function fetchPlan(id: string): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}`, undefined, PlanSchema as ZodType<Plan>)
}

/**
 * Create a plan — POST /workspaces/{id}/plans (createWorkspacePlan). Bare
 * POST /plans 405s (`rest_plans.go` HandlePlans: "Bare /plans has no
 * GET/POST"); creation is workspace-nested, mirroring `fetchPlans`.
 * `body.workspace_id` drives the path (also required/validated in the body).
 */
export function createPlan(body: PlanCreateRequest): Promise<Plan> {
  return request<Plan>(
    `/workspaces/${encodeURIComponent(body.workspace_id)}/plans`,
    { method: 'POST', body: JSON.stringify(body) },
    PlanSchema as ZodType<Plan>,
  )
}

/**
 * Partial plan update — title/goal/description/owner_agent_id/dod/bounds
 * ONLY. ADR-052 §6.3/FR-007 (G2 fix): the SPA must NEVER send `state` in this
 * body — PUT is not, and must never become, a state-transition entry point
 * (the backend endpoint that previously accepted `state` here bypassed both
 * the FR-084 per-task criteria gate and the cap-16 admission check). Use
 * `executePlan` / `stopPlan` / `restartPlan` for every state transition.
 */
export function updatePlan(id: string, body: Omit<PlanUpdateRequest, 'state'>): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }, PlanSchema as ZodType<Plan>)
}

/**
 * Execute (Approve) a draft plan — POST /plans/{id}/approve (ADR-052 FR-003/
 * FR-007/FR-008/US-3/US-5). This is the SOLE gated entry point into
 * `approved`: it runs the tiered Definition-of-Done check plus the
 * unconditional per-member-task criteria gate (FR-084), then the single
 * plan-engine instance promotes `approved` -> `running` under the global cap
 * (16) on its own tick — this call returns once the plan reaches `approved`,
 * it does not wait for a cap slot ("queued behind cap" is a normal outcome,
 * not an error). SD-C4: confirm-on-success, no optimistic flip — a `400`
 * carries a `PlanApproveError` body (`error` and/or `task_errors`); parse it
 * with `parsePlanApproveTaskErrors`.
 *
 * Named `executePlan` (the ▶ Execute button's semantics — G4/FR-003) rather
 * than the historical `approvePlan`, which used to PUT `{state:'approved'}`
 * and — per the G2 bug this repoints — silently bypassed BOTH the criteria
 * gate and the cap. `approvePlan` survives below as a deprecated alias for
 * any not-yet-swept caller; new call sites should use `executePlan`.
 */
export function executePlan(id: string): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}/approve`, { method: 'POST' }, PlanSchema as ZodType<Plan>)
}

/** @deprecated Use `executePlan` — this name predates the ADR-052 G2 fix (PUT-based approve bypassed the criteria gate + cap). Same signature/behavior, POST /approve underneath. */
export const approvePlan = executePlan

/** Stop/Clear (D8) — stops a running plan's loop. May be optimistic (SD-C5): it cannot validation-fail like Approve. */
export function stopPlan(id: string): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}/stop`, { method: 'POST' }, PlanSchema as ZodType<Plan>)
}

/**
 * Restart (▶ Play) a plan previously Stopped by the user — POST
 * /plans/{id}/restart (ADR-052 FR-016/FR-017/FR-026, US-9). Resets every
 * non-`done` member to `next`/`blocked` with `attempt_count` reset to 0,
 * resets the plan's `judge_rounds` to 0, preserves `done` members + their
 * evidence, clears `failed_reason`, and returns the plan in `approved` state
 * (NOT `running` — the engine promotes it under the cap on its own tick,
 * exactly like a first execute, so a restart can never skip cap admission).
 *
 * A `409` means "not restartable": the plan isn't `failed`, or its
 * `failed_reason` isn't `stopped_by_user` (a GENUINE failure —
 * `judge_rounds_exhausted` / `idle_expired` — is a terminal state with no
 * Play offered, FR-018). Rewritten into a specific, actionable message here
 * rather than the generic "conflicts with current state" default.
 */
export async function restartPlan(id: string): Promise<Plan> {
  try {
    return await request<Plan>(`/plans/${encodeURIComponent(id)}/restart`, { method: 'POST' }, PlanSchema as ZodType<Plan>)
  } catch (err) {
    throw friendlyConflictError(
      err,
      'This plan is not restartable — it must have been Stopped by a user (not a genuine failure) to Play it again.',
    )
  }
}

export function deletePlan(id: string): Promise<void> {
  return request<void>(`/plans/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/**
 * PlanApproveTaskError — one entry of the generated `PlanApproveError.task_errors`
 * array (contracts/components/schemas/PlanApproveError.yaml — now a real,
 * generated Constraint #8 wire type; this alias exists only so callers don't
 * need to reach into `NonNullable<PlanApproveError['task_errors']>[number]`
 * themselves).
 */
export type PlanApproveTaskError = NonNullable<PlanApproveError['task_errors']>[number]

/**
 * Parse a `POST /plans/{id}/approve` `400` body (`ApiError.body`) into the
 * generated `PlanApproveError` shape, edge-validated with the generated Zod
 * schema (Constraint #8 — no hand-rolled parsing of the response shape).
 * Returns `null` when the body is empty, not JSON, doesn't validate against
 * the schema, or validates but carries no `task_errors` — callers fall back
 * to `err.userMessage` (which already carries the plan-level `error` string
 * for the non-task-errors rejection case, e.g. an empty DoD).
 */
export function parsePlanApproveTaskErrors(body: string | undefined): PlanApproveTaskError[] | null {
  if (!body) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(body)
  } catch {
    return null
  }
  const result = PlanApproveErrorSchema.safeParse(parsed)
  if (!result.success) return null
  const taskErrors = result.data.task_errors
  return taskErrors && taskErrors.length > 0 ? taskErrors : null
}
