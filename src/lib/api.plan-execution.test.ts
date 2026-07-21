// BDD: ADR-052 Wave 2 (agent plan authoring & execution) — the SPA API layer.
// Traces to: docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md
//            docs/internal/specs/autonomous-agent-plan-execution-spec.md
//            FR-003 (execute_plan), FR-007/FR-008 (PUT lockdown / gated
//            approve), FR-016/FR-017/FR-026 (restart), FR-019 (run_task/G4),
//            US-3, US-5, US-9, US-10.
//
// Covers exactly the deliverables owned by this wave's spa-wiring agent:
//   1. The G2 repoint — approvePlan/executePlan MUST POST /plans/{id}/approve,
//      never PUT {state:'approved'} (the PUT path used to bypass BOTH the
//      FR-084 criteria gate and the cap-16 admission check).
//   2. The new restartPlan/restartTask/runTask/stopTask functions, generated-
//      type-backed, with 409 -> friendly "not restartable"/"not runnable"
//      error mapping (every other status passes through unchanged).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// ── Helpers ────────────────────────────────────────────────────────────────────

function stubCookie(value: string) {
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => value,
  })
}

function restoreCookie() {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

function makeJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function makeErrorResponse(body: unknown, status: number): Response {
  return new Response(typeof body === 'string' ? body : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function makePlanResponse(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Launch',
    state: 'draft',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-07-20T10:00:00Z',
    updated_at: '2026-07-20T10:00:00Z',
    ...overrides,
  }
}

function makeTaskResponse(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'task-1',
    title: 'Write report',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-07-20T10:00:00Z',
    updated_at: '2026-07-20T10:00:00Z',
    ...overrides,
  }
}

// ── Test setup ────────────────────────────────────────────────────────────────

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchSpy = vi.fn()
  vi.stubGlobal('fetch', fetchSpy)
  stubCookie('__Host-csrf=test-csrf-token')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  sessionStorage.clear()
  restoreCookie()
})

// ── executePlan / approvePlan — the G2 repoint ──────────────────────────────

describe('executePlan (the G2 repoint)', () => {
  it('calls POST /api/v1/plans/{id}/approve — NOT PUT — and returns the approved Plan', async () => {
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(makePlanResponse({ id: 'plan-9', state: 'approved' })))

    const { executePlan } = await import('./api')
    const result = await executePlan('plan-9')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/plans/plan-9/approve')
    expect(init.method).toBe('POST')
    // The G2 bug was a PUT with a JSON {state:'approved'} body — assert
    // there is no body at all on the new call (nothing to gate around).
    expect(init.body).toBeUndefined()

    expect(result.id).toBe('plan-9')
    expect(result.state).toBe('approved')
  })

  it('a 400 rejection surfaces via ApiError — the caller parses task_errors with parsePlanApproveTaskErrors', async () => {
    const body = JSON.stringify({
      task_errors: [{ task_id: 't1', title: 'Write report', reason: 'task has no acceptance criteria' }],
    })
    fetchSpy.mockResolvedValueOnce(makeErrorResponse(body, 400))

    const { executePlan, parsePlanApproveTaskErrors, isApiError } = await import('./api')

    await expect(executePlan('plan-9')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(400)
      const parsed = parsePlanApproveTaskErrors(err.body)
      expect(parsed).toEqual([
        { task_id: 't1', title: 'Write report', reason: 'task has no acceptance criteria' },
      ])
      return true
    })
  })
})

// ── createPlan (Wave-3 hotfix — bare POST /plans 405s; creation is
// workspace-nested per rest_plans.go's HandlePlans) ─────────────────────────

describe('createPlan', () => {
  it('calls POST /api/v1/workspaces/{workspace_id}/plans — NOT bare /plans — and returns the created Plan', async () => {
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(makePlanResponse({ id: 'plan-9' }), 201))

    const { createPlan } = await import('./api')
    const result = await createPlan({
      workspace_id: 'ws-1',
      title: 'Launch',
      owner_agent_id: 'jim',
    })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces/ws-1/plans')
    expect(url).not.toMatch(/\/api\/v1\/plans$/)
    expect(init.method).toBe('POST')
    const sentBody = JSON.parse(init.body as string)
    expect(sentBody).toEqual({ workspace_id: 'ws-1', title: 'Launch', owner_agent_id: 'jim' })

    expect(result.id).toBe('plan-9')
  })

  it('a bare-route 405 (the pre-fix regression) surfaces via ApiError with a readable message', async () => {
    fetchSpy.mockResolvedValueOnce(makeErrorResponse({ error: 'method not allowed' }, 405))

    const { createPlan, isApiError } = await import('./api')

    await expect(
      createPlan({ workspace_id: 'ws-1', title: 'Launch', owner_agent_id: 'jim' }),
    ).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(405)
      return true
    })
  })
})

describe('approvePlan (deprecated alias)', () => {
  it('is the same function as executePlan — POSTs /approve, does not PUT', async () => {
    const { executePlan, approvePlan } = await import('./api')
    expect(approvePlan).toBe(executePlan)

    fetchSpy.mockResolvedValueOnce(makeJsonResponse(makePlanResponse({ id: 'plan-legacy', state: 'approved' })))
    await approvePlan('plan-legacy')

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/plans/plan-legacy/approve')
    expect(init.method).toBe('POST')
  })
})

describe('updatePlan (unaffected by the G2 fix — still PUTs non-state fields)', () => {
  it('PUTs /api/v1/plans/{id} with the given partial body', async () => {
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(makePlanResponse({ id: 'plan-9', title: 'New title' })))

    const { updatePlan } = await import('./api')
    const result = await updatePlan('plan-9', { title: 'New title' })

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/plans/plan-9')
    expect(url).not.toContain('/approve')
    expect(init.method).toBe('PUT')
    const sentBody = JSON.parse(init.body as string)
    expect(sentBody).toEqual({ title: 'New title' })
    expect(result.title).toBe('New title')
  })
})

// ── restartPlan ──────────────────────────────────────────────────────────────

describe('restartPlan', () => {
  it('calls POST /api/v1/plans/{id}/restart and returns the restarted Plan', async () => {
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse(makePlanResponse({ id: 'plan-9', state: 'approved', failed_reason: undefined })),
    )

    const { restartPlan } = await import('./api')
    const result = await restartPlan('plan-9')

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/plans/plan-9/restart')
    expect(init.method).toBe('POST')
    expect(result.state).toBe('approved')
  })

  it('maps a 409 ("not restartable") to a friendly, actionable ApiError', async () => {
    fetchSpy.mockResolvedValueOnce(makeErrorResponse({ error: 'plan is not failed' }, 409))

    const { restartPlan, isApiError } = await import('./api')

    await expect(restartPlan('plan-9')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(409)
      expect(err.userMessage).toMatch(/not restartable/i)
      return true
    })
  })

  it('leaves a non-409 error (404) unchanged — no friendly rewrite', async () => {
    fetchSpy.mockResolvedValueOnce(makeErrorResponse({ error: 'plan not found' }, 404))

    const { restartPlan, isApiError } = await import('./api')

    await expect(restartPlan('missing')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(404)
      expect(err.userMessage).not.toMatch(/not restartable/i)
      return true
    })
  })

  it('leaves a transport failure (status 0) unchanged', async () => {
    fetchSpy.mockRejectedValueOnce(new TypeError('network down'))

    const { restartPlan, isApiError } = await import('./api')

    await expect(restartPlan('plan-9')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(0)
      return true
    })
  })
})

// ── restartTask ──────────────────────────────────────────────────────────────

describe('restartTask', () => {
  it('calls POST /api/v1/tasks/{id}/restart and returns the restarted Task', async () => {
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse(makeTaskResponse({ id: 'task-9', status: 'next', cancel_reason: null })),
    )

    const { restartTask } = await import('./api')
    const result = await restartTask('task-9')

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/tasks/task-9/restart')
    expect(init.method).toBe('POST')
    expect(result.status).toBe('next')
  })

  it('maps a 409 ("belongs to a plan" / "not restartable") to a friendly ApiError', async () => {
    fetchSpy.mockResolvedValueOnce(makeErrorResponse({ error: 'task belongs to a plan' }, 409))

    const { restartTask, isApiError } = await import('./api')

    await expect(restartTask('task-9')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(409)
      expect(err.userMessage).toMatch(/not restartable/i)
      expect(err.userMessage).toMatch(/plan/i)
      return true
    })
  })

  it('leaves a non-409 error (404) unchanged', async () => {
    fetchSpy.mockResolvedValueOnce(makeErrorResponse({ error: 'task not found' }, 404))

    const { restartTask, isApiError } = await import('./api')

    await expect(restartTask('missing')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(404)
      return true
    })
  })
})

// ── runTask (G4 — no REST /run route; PATCH status=in_progress) ────────────

describe('runTask', () => {
  it('PATCHes /api/v1/tasks/{id} with status "in_progress" (no dedicated /run route exists)', async () => {
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(makeTaskResponse({ id: 'task-9', status: 'in_progress' })))

    const { runTask } = await import('./api')
    const result = await runTask('task-9')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/tasks/task-9')
    expect(url).not.toContain('/run')
    expect(init.method).toBe('PATCH')
    const sentBody = JSON.parse(init.body as string)
    expect(sentBody).toEqual({ status: 'in_progress' })
    expect(result.status).toBe('in_progress')
  })

  it('maps a 409 (e.g. an in-plan member — its plan drives its start) to a friendly "can\'t be run" ApiError', async () => {
    fetchSpy.mockResolvedValueOnce(makeErrorResponse({ error: 'task is a plan member' }, 409))

    const { runTask, isApiError } = await import('./api')

    await expect(runTask('task-9')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(409)
      expect(err.userMessage).toMatch(/can't be run/i)
      return true
    })
  })

  it('leaves a non-409 error (400 — invalid transition) unchanged', async () => {
    fetchSpy.mockResolvedValueOnce(makeErrorResponse({ error: 'invalid status transition' }, 400))

    const { runTask, isApiError } = await import('./api')

    await expect(runTask('task-9')).rejects.toSatisfy((err: unknown) => {
      expect(isApiError(err)).toBe(true)
      if (!isApiError(err)) return false
      expect(err.status).toBe(400)
      expect(err.userMessage).not.toMatch(/can't be run/i)
      return true
    })
  })
})

// ── stopPlan / deletePlan (D8 — the P0 stop route) ──────────────────────────

describe('stopPlan', () => {
  it('calls POST /api/v1/plans/{id}/stop and returns the stopped Plan', async () => {
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse(makePlanResponse({ id: 'plan-x', state: 'failed', failed_reason: 'stopped_by_user' })),
    )

    const { stopPlan } = await import('./api')
    const result = await stopPlan('plan-x')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/plans/plan-x/stop')
    expect(init.method).toBe('POST')
    expect(result.id).toBe('plan-x')
  })
})

describe('deletePlan', () => {
  it('calls DELETE /api/v1/plans/{id}', async () => {
    fetchSpy.mockResolvedValueOnce(new Response(null, { status: 204 }))

    const { deletePlan } = await import('./api')
    await deletePlan('plan-x')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/plans/plan-x')
    expect(init.method).toBe('DELETE')
  })
})

// ── stopTask / stopTaskGoalLoop ─────────────────────────────────────────────

describe('stopTask', () => {
  it('calls POST /api/v1/tasks/{id}/stop and returns the stopped Task', async () => {
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse(makeTaskResponse({ id: 'task-9', status: 'failed', cancel_reason: 'stopped_by_user' })),
    )

    const { stopTask } = await import('./api')
    const result = await stopTask('task-9')

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/tasks/task-9/stop')
    expect(init.method).toBe('POST')
    expect(result.cancel_reason).toBe('stopped_by_user')
  })

  it('stopTaskGoalLoop (deprecated alias) is the same function as stopTask', async () => {
    const { stopTask, stopTaskGoalLoop } = await import('./api')
    expect(stopTaskGoalLoop).toBe(stopTask)
  })
})

// ── parsePlanApproveTaskErrors — now backed by the generated PlanApproveError schema ──

describe('parsePlanApproveTaskErrors', () => {
  it('parses a valid PlanApproveError body into the task_errors array', async () => {
    const { parsePlanApproveTaskErrors } = await import('./api')
    const body = JSON.stringify({
      task_errors: [
        { task_id: 't1', title: 'Write report', reason: 'task has no acceptance criteria' },
        { task_id: 't2', title: 'Deploy', reason: 'task has no acceptance criteria' },
      ],
    })
    expect(parsePlanApproveTaskErrors(body)).toEqual([
      { task_id: 't1', title: 'Write report', reason: 'task has no acceptance criteria' },
      { task_id: 't2', title: 'Deploy', reason: 'task has no acceptance criteria' },
    ])
  })

  it('returns null for a plan-level-only error (no task_errors)', async () => {
    const { parsePlanApproveTaskErrors } = await import('./api')
    const body = JSON.stringify({ error: 'plan requires a Definition of Done before approval' })
    expect(parsePlanApproveTaskErrors(body)).toBeNull()
  })

  it('returns null for an empty task_errors array', async () => {
    const { parsePlanApproveTaskErrors } = await import('./api')
    expect(parsePlanApproveTaskErrors(JSON.stringify({ task_errors: [] }))).toBeNull()
  })

  it('returns null for malformed JSON', async () => {
    const { parsePlanApproveTaskErrors } = await import('./api')
    expect(parsePlanApproveTaskErrors('{not json')).toBeNull()
  })

  it('returns null for a body that does not match the PlanApproveError schema', async () => {
    const { parsePlanApproveTaskErrors } = await import('./api')
    expect(parsePlanApproveTaskErrors(JSON.stringify({ task_errors: 'not-an-array' }))).toBeNull()
  })

  it('returns null for undefined body', async () => {
    const { parsePlanApproveTaskErrors } = await import('./api')
    expect(parsePlanApproveTaskErrors(undefined)).toBeNull()
  })
})
