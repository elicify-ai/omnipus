// BDD: API functions for projects, board tasks, and token stats.
// Traces to: wave4-level1-project-task-mgmt spec — API layer request shapes.

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

// ── Test setup ────────────────────────────────────────────────────────────────

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

// ── fetchProjects ─────────────────────────────────────────────────────────────

describe('fetchProjects', () => {
  it('calls GET /api/v1/projects and returns the Project array', async () => {
    // BDD: Given a server that returns 2 projects,
    // When fetchProjects() is called,
    // Then GET /api/v1/projects is requested and 2 projects are returned.
    // Traces to: wave4-level1-project-task-mgmt spec — fetchProjects shape
    const payload = [
      { id: 'p1', name: 'Project Alpha', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      { id: 'p2', name: 'Project Beta', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-02T00:00:00Z', updated_at: '2026-01-02T00:00:00Z' },
    ]
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchProjects } = await import('./api')
    const result = await fetchProjects()

    // Verify the correct URL was called.
    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/projects')

    // Verify the result contains both projects.
    expect(result).toHaveLength(2)
    expect(result[0].id).toBe('p1')
    expect(result[0].name).toBe('Project Alpha')
    expect(result[1].id).toBe('p2')
    expect(result[1].name).toBe('Project Beta')
  })

  it('appends status query param when provided', async () => {
    // BDD: Given fetchProjects({ status: "archived" }),
    // When the call is made,
    // Then URL contains ?status=archived.
    // Traces to: wave4-level1-project-task-mgmt spec — project list filters
    fetchSpy.mockResolvedValueOnce(makeJsonResponse([]))

    const { fetchProjects } = await import('./api')
    await fetchProjects({ status: 'archived' })

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('status=archived')
  })
})

// ── createProject ─────────────────────────────────────────────────────────────

describe('createProject', () => {
  it('calls POST /api/v1/projects and returns the created Project', async () => {
    // BDD: Given a valid ProjectCreateRequest,
    // When createProject({ name: "Test" }) is called,
    // Then POST /api/v1/projects is requested with the correct body,
    // And the created project is returned.
    // Traces to: wave4-level1-project-task-mgmt spec — createProject shape
    const created = {
      id: 'new-proj-id',
      name: 'Test',
      status: 'active',
      pinned: false,
      pin_order: 0,
      task_count: 0,
      created_at: '2026-06-01T00:00:00Z',
      updated_at: '2026-06-01T00:00:00Z',
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(created, 201))

    const { createProject } = await import('./api')
    const result = await createProject({ name: 'Test' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/projects')
    expect((init as RequestInit).method).toBe('POST')

    // Verify the request body contains the project name.
    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.name).toBe('Test')

    // Verify the returned project has the correct id and name.
    expect(result.id).toBe('new-proj-id')
    expect(result.name).toBe('Test')
    expect(result.status).toBe('active')
  })

  it('differentiation test: creating two different projects returns different ids', async () => {
    // Anti-hardcode: two POST calls with different names must produce different results.
    // Traces to: wave4-level1-project-task-mgmt spec — createProject differentiation
    const created1 = { id: 'id-alpha', name: 'Alpha', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' }
    const created2 = { id: 'id-beta', name: 'Beta', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-06-01T00:00:01Z', updated_at: '2026-06-01T00:00:01Z' }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(created1, 201))
      .mockResolvedValueOnce(makeJsonResponse(created2, 201))

    const { createProject } = await import('./api')
    const r1 = await createProject({ name: 'Alpha' })
    const r2 = await createProject({ name: 'Beta' })

    expect(r1.id).toBe('id-alpha')
    expect(r2.id).toBe('id-beta')
    expect(r1.id).not.toBe(r2.id)
    expect(r1.name).not.toBe(r2.name)
  })
})

// ── deleteProject ─────────────────────────────────────────────────────────────

describe('deleteProject', () => {
  it('calls DELETE /api/v1/projects/{id} with the correct URL and method', async () => {
    // BDD: Given an existing project id,
    // When deleteProject("id") is called,
    // Then DELETE /api/v1/projects/id is requested.
    // Note: request() always calls res.json(); mock returns {} so it parses cleanly.
    // Traces to: wave4-level1-project-task-mgmt spec — deleteProject shape
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteProject } = await import('./api')
    // deleteProject returns void — should not throw.
    await expect(deleteProject('test-id')).resolves.not.toThrow()

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/projects/test-id')
    expect((init as RequestInit).method).toBe('DELETE')
  })

  it('sends the correct encoded id in the URL', async () => {
    // BDD: Given a project id,
    // When deleteProject is called,
    // Then the URL contains the id.
    // Traces to: wave4-level1-project-task-mgmt spec — deleteProject URL encoding
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteProject } = await import('./api')
    await deleteProject('proj-xyz-001')

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('proj-xyz-001')
  })
})

// ── fetchBoardTasks ───────────────────────────────────────────────────────────

describe('fetchBoardTasks', () => {
  it('calls GET /api/v1/board/tasks?status=inbox and returns the items array', async () => {
    // BDD: Given a server that returns a board task list response,
    // When fetchBoardTasks({ status: "inbox" }) is called,
    // Then GET /api/v1/board/tasks?status=inbox is requested,
    // And the returned array contains the items from the response.
    // Traces to: wave4-level1-project-task-mgmt spec — fetchBoardTasks shape
    const payload = {
      items: [
        { id: 't1', name: 'Fix bug', status: 'inbox', created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' },
        { id: 't2', name: 'Write tests', status: 'inbox', created_at: '2026-06-01T00:01:00Z', updated_at: '2026-06-01T00:01:00Z' },
      ],
      total: 2,
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchBoardTasks } = await import('./api')
    const result = await fetchBoardTasks({ status: 'inbox' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/board/tasks')
    expect(url).toContain('status=inbox')

    // fetchBoardTasks returns res.items (not the full wrapper).
    expect(result).toHaveLength(2)
    expect(result[0].id).toBe('t1')
    expect(result[0].name).toBe('Fix bug')
    expect(result[1].id).toBe('t2')
  })

  it('calls GET /api/v1/board/tasks without query params when no filters given', async () => {
    // BDD: Given no filters,
    // When fetchBoardTasks() is called,
    // Then URL does not contain ?status= or ?project_id=.
    // Traces to: wave4-level1-project-task-mgmt spec — fetchBoardTasks no-filter
    const payload = { items: [], total: 0 }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchBoardTasks } = await import('./api')
    const result = await fetchBoardTasks()

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).not.toContain('status=')
    expect(url).not.toContain('project_id=')
    expect(result).toEqual([])
  })
})
