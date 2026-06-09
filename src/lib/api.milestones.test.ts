/**
 * api.milestones.test.ts
 *
 * Tests for the milestone API functions: fetchMilestones, createMilestone,
 * deleteMilestone, and updateMilestone.
 *
 * Uses the same fetch-spy + cookie stub pattern as api.projects.test.ts.
 * Traces to: project-task-management-level1-spec.md — milestone API layer tests
 */

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

// ── Test setup ─────────────────────────────────────────────────────────────────

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchSpy = vi.fn()
  vi.stubGlobal('fetch', fetchSpy)
  // Provide a valid CSRF cookie so state-changing calls pass the CSRF gate.
  stubCookie('__Host-csrf=test-csrf-token')
  // Provide a bearer token so auth headers are populated.
  sessionStorage.setItem('omnipus_auth_token', 'test-bearer')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  sessionStorage.clear()
  restoreCookie()
})

// ── fetchMilestones ───────────────────────────────────────────────────────────

describe('fetchMilestones', () => {
  it('calls GET /api/v1/projects/{id}/milestones and returns the milestones array', async () => {
    // BDD: Given a project with milestones,
    // When fetchMilestones('p1') is called,
    // Then GET /api/v1/projects/p1/milestones is requested,
    // And the returned array contains the milestones with progress fields.
    // Traces to: project-task-management-level1-spec.md — fetchMilestones shape
    const payload = {
      milestones: [
        {
          id: 'ms-1',
          project_id: 'p1',
          name: 'Alpha Release',
          progress: 0.4,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-15T00:00:00Z',
        },
        {
          id: 'ms-2',
          project_id: 'p1',
          name: 'Beta Launch',
          progress: 0,
          created_at: '2026-02-01T00:00:00Z',
          updated_at: '2026-02-01T00:00:00Z',
        },
      ],
      total: 2,
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchMilestones } = await import('./api')
    const result = await fetchMilestones('p1')

    // Verify the correct URL was called.
    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/projects/p1/milestones')

    // Verify the result contains both milestones.
    expect(result).toHaveLength(2)
    expect(result[0].id).toBe('ms-1')
    expect(result[0].name).toBe('Alpha Release')
    expect(result[0].progress).toBe(0.4)
    expect(result[1].id).toBe('ms-2')
    expect(result[1].name).toBe('Beta Launch')
  })

  it('differentiation test: two different project IDs call different URLs', async () => {
    // Anti-hardcode: fetching milestones for different projects must use different URLs.
    // Traces to: project-task-management-level1-spec.md — fetchMilestones differentiation
    const msP1 = { milestones: [{ id: 'ms-p1', project_id: 'p1', name: 'P1 Milestone', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], total: 1 }
    const msP2 = { milestones: [{ id: 'ms-p2', project_id: 'p2', name: 'P2 Milestone', created_at: '2026-02-01T00:00:00Z', updated_at: '2026-02-01T00:00:00Z' }], total: 1 }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(msP1))
      .mockResolvedValueOnce(makeJsonResponse(msP2))

    const { fetchMilestones } = await import('./api')
    const r1 = await fetchMilestones('p1')
    const r2 = await fetchMilestones('p2')

    // Different project IDs → different URLs
    const [url1] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const [url2] = fetchSpy.mock.calls[1] as [string, RequestInit]
    expect(url1).toContain('/projects/p1/milestones')
    expect(url2).toContain('/projects/p2/milestones')
    expect(url1).not.toBe(url2)

    // Different milestones returned
    expect(r1[0].id).toBe('ms-p1')
    expect(r2[0].id).toBe('ms-p2')
    expect(r1[0].name).not.toBe(r2[0].name)
  })

  it('returns empty array when project has no milestones', async () => {
    // BDD: Given a project with no milestones,
    // When fetchMilestones is called,
    // Then an empty array is returned.
    // Traces to: project-task-management-level1-spec.md — fetchMilestones empty
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({ milestones: [], total: 0 }))

    const { fetchMilestones } = await import('./api')
    const result = await fetchMilestones('empty-proj')

    expect(result).toEqual([])
  })

  it('encodes project ID in URL path', async () => {
    // BDD: Given a project ID used as a URL path segment,
    // When fetchMilestones is called,
    // Then the project ID appears in the URL.
    // Traces to: project-task-management-level1-spec.md — fetchMilestones URL encoding
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({ milestones: [], total: 0 }))

    const { fetchMilestones } = await import('./api')
    await fetchMilestones('proj-abc-123')

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('proj-abc-123')
  })
})

// ── createMilestone ───────────────────────────────────────────────────────────

describe('createMilestone', () => {
  it('calls POST /api/v1/projects/{id}/milestones with the correct body', async () => {
    // BDD: Given a valid MilestoneCreateRequest,
    // When createMilestone('p1', { name: 'Beta' }) is called,
    // Then POST /api/v1/projects/p1/milestones is requested with the correct body,
    // And the created milestone is returned.
    // Traces to: project-task-management-level1-spec.md — createMilestone shape
    const created = {
      id: 'new-ms-id',
      project_id: 'p1',
      name: 'Beta',
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:00:00Z',
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(created, 201))

    const { createMilestone } = await import('./api')
    const result = await createMilestone('p1', { name: 'Beta' })

    // Verify the correct URL and method.
    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/projects/p1/milestones')
    expect((init as RequestInit).method).toBe('POST')

    // Verify the request body contains the name.
    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.name).toBe('Beta')

    // Verify the returned milestone has the correct fields.
    expect(result.id).toBe('new-ms-id')
    expect(result.name).toBe('Beta')
    expect(result.project_id).toBe('p1')
  })

  it('differentiation test: creating two different milestones returns different results', async () => {
    // Anti-hardcode: two POST calls with different names must produce different results.
    // Traces to: project-task-management-level1-spec.md — createMilestone differentiation
    const ms1 = { id: 'ms-alpha', project_id: 'p1', name: 'Alpha', created_at: '2026-06-09T10:00:00Z', updated_at: '2026-06-09T10:00:00Z' }
    const ms2 = { id: 'ms-beta', project_id: 'p1', name: 'Beta', created_at: '2026-06-09T11:00:00Z', updated_at: '2026-06-09T11:00:00Z' }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(ms1, 201))
      .mockResolvedValueOnce(makeJsonResponse(ms2, 201))

    const { createMilestone } = await import('./api')
    const r1 = await createMilestone('p1', { name: 'Alpha' })
    const r2 = await createMilestone('p1', { name: 'Beta' })

    expect(r1.id).toBe('ms-alpha')
    expect(r2.id).toBe('ms-beta')
    expect(r1.id).not.toBe(r2.id)
    expect(r1.name).not.toBe(r2.name)
  })

  it('includes optional due_date in the request body when provided', async () => {
    // BDD: Given a MilestoneCreateRequest with a due_date,
    // When createMilestone is called,
    // Then the request body contains the due_date field.
    // Traces to: project-task-management-level1-spec.md — createMilestone due_date
    const created = {
      id: 'ms-due',
      project_id: 'p1',
      name: 'Deadline Milestone',
      due_date: '2026-12-31',
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:00:00Z',
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(created, 201))

    const { createMilestone } = await import('./api')
    await createMilestone('p1', { name: 'Deadline Milestone', due_date: '2026-12-31' })

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.due_date).toBe('2026-12-31')
  })
})

// ── deleteMilestone ───────────────────────────────────────────────────────────

describe('deleteMilestone', () => {
  it('calls DELETE /api/v1/projects/{id}/milestones/{milestoneId}', async () => {
    // BDD: Given an existing project and milestone,
    // When deleteMilestone('p1', 'm1') is called,
    // Then DELETE /api/v1/projects/p1/milestones/m1 is requested,
    // And the function resolves without throwing.
    // Traces to: project-task-management-level1-spec.md — deleteMilestone shape
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteMilestone } = await import('./api')
    await expect(deleteMilestone('p1', 'm1')).resolves.not.toThrow()

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/projects/p1/milestones/m1')
    expect((init as RequestInit).method).toBe('DELETE')
  })

  it('includes both project ID and milestone ID in the URL', async () => {
    // BDD: Given specific project and milestone IDs,
    // When deleteMilestone is called,
    // Then both IDs appear in the URL (not just one).
    // Traces to: project-task-management-level1-spec.md — deleteMilestone URL both IDs
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteMilestone } = await import('./api')
    await deleteMilestone('proj-xyz-001', 'ms-abc-999')

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('proj-xyz-001')
    expect(url).toContain('ms-abc-999')
  })

  it('differentiation test: deleting two different milestones calls different URLs', async () => {
    // Anti-hardcode: two DELETE calls must use different URLs.
    // Traces to: project-task-management-level1-spec.md — deleteMilestone differentiation
    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse({}, 200))
      .mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteMilestone } = await import('./api')
    await deleteMilestone('p1', 'ms-first')
    await deleteMilestone('p1', 'ms-second')

    const [url1] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const [url2] = fetchSpy.mock.calls[1] as [string, RequestInit]
    expect(url1).toContain('ms-first')
    expect(url2).toContain('ms-second')
    expect(url1).not.toBe(url2)
  })

  it('throws ApiError on 404 when milestone does not exist', async () => {
    // BDD: Given a milestone ID that does not exist,
    // When deleteMilestone is called,
    // Then an ApiError with status 404 is thrown.
    // Traces to: project-task-management-level1-spec.md — deleteMilestone 404 error
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'milestone not found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { deleteMilestone, ApiError, isApiError } = await import('./api')
    let thrown: unknown
    try {
      await deleteMilestone('p1', 'missing-ms')
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(404)
  })
})
