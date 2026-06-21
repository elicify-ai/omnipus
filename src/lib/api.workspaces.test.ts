// BDD: API functions for workspaces, board tasks, and token stats.
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

// ── fetchWorkspaces ─────────────────────────────────────────────────────────────

describe('fetchWorkspaces', () => {
  it('calls GET /api/v1/workspaces and returns the Workspace array', async () => {
    // BDD: Given a server that returns 2 workspaces,
    // When fetchWorkspaces() is called,
    // Then GET /api/v1/workspaces is requested and 2 workspaces are returned.
    // Traces to: wave4-level1-project-task-mgmt spec — fetchWorkspaces shape
    const payload = [
      { id: 'p1', name: 'Project Alpha', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      { id: 'p2', name: 'Project Beta', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-02T00:00:00Z', updated_at: '2026-01-02T00:00:00Z' },
    ]
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchWorkspaces } = await import('./api')
    const result = await fetchWorkspaces()

    // Verify the correct URL was called.
    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces')

    // Verify the result contains both projects.
    expect(result).toHaveLength(2)
    expect(result[0].id).toBe('p1')
    expect(result[0].name).toBe('Project Alpha')
    expect(result[1].id).toBe('p2')
    expect(result[1].name).toBe('Project Beta')
  })

  it('appends status query param when provided', async () => {
    // BDD: Given fetchWorkspaces({ status: "archived" }),
    // When the call is made,
    // Then URL contains ?status=archived.
    // Traces to: wave4-level1-project-task-mgmt spec — workspace list filters
    fetchSpy.mockResolvedValueOnce(makeJsonResponse([]))

    const { fetchWorkspaces } = await import('./api')
    await fetchWorkspaces({ status: 'archived' })

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('status=archived')
  })
})

// ── createWorkspace ─────────────────────────────────────────────────────────────

describe('createWorkspace', () => {
  it('calls POST /api/v1/workspaces and returns the created Workspace', async () => {
    // BDD: Given a valid WorkspaceCreateRequest,
    // When createWorkspace({ name: "Test" }) is called,
    // Then POST /api/v1/workspaces is requested with the correct body,
    // And the created workspace is returned.
    // Traces to: wave4-level1-project-task-mgmt spec — createWorkspace shape
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

    const { createWorkspace } = await import('./api')
    const result = await createWorkspace({ name: 'Test' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces')
    expect((init as RequestInit).method).toBe('POST')

    // Verify the request body contains the project name.
    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.name).toBe('Test')

    // Verify the returned project has the correct id and name.
    expect(result.id).toBe('new-proj-id')
    expect(result.name).toBe('Test')
    expect(result.status).toBe('active')
  })

  it('differentiation test: creating two different workspaces returns different ids', async () => {
    // Anti-hardcode: two POST calls with different names must produce different results.
    // Traces to: wave4-level1-project-task-mgmt spec — createWorkspace differentiation
    const created1 = { id: 'id-alpha', name: 'Alpha', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' }
    const created2 = { id: 'id-beta', name: 'Beta', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-06-01T00:00:01Z', updated_at: '2026-06-01T00:00:01Z' }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(created1, 201))
      .mockResolvedValueOnce(makeJsonResponse(created2, 201))

    const { createWorkspace } = await import('./api')
    const r1 = await createWorkspace({ name: 'Alpha' })
    const r2 = await createWorkspace({ name: 'Beta' })

    expect(r1.id).toBe('id-alpha')
    expect(r2.id).toBe('id-beta')
    expect(r1.id).not.toBe(r2.id)
    expect(r1.name).not.toBe(r2.name)
  })
})

// ── deleteWorkspace ─────────────────────────────────────────────────────────────

describe('deleteWorkspace', () => {
  it('calls DELETE /api/v1/workspaces/{id} with the correct URL and method', async () => {
    // BDD: Given an existing workspace id,
    // When deleteWorkspace("id") is called,
    // Then DELETE /api/v1/workspaces/id is requested.
    // Note: request() always calls res.json(); mock returns {} so it parses cleanly.
    // Traces to: wave4-level1-project-task-mgmt spec — deleteWorkspace shape
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteWorkspace } = await import('./api')
    // deleteWorkspace returns void — should not throw.
    await expect(deleteWorkspace('test-id')).resolves.not.toThrow()

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces/test-id')
    expect((init as RequestInit).method).toBe('DELETE')
  })

  it('sends the correct encoded id in the URL', async () => {
    // BDD: Given a project id,
    // When deleteWorkspace is called,
    // Then the URL contains the id.
    // Traces to: wave4-level1-project-task-mgmt spec — deleteWorkspace URL encoding
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteWorkspace } = await import('./api')
    await deleteWorkspace('proj-xyz-001')

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('proj-xyz-001')
  })

  it('throws ApiError on 404 response', async () => {
    // BDD: Given a project id that does not exist,
    // When deleteWorkspace is called,
    // Then an ApiError with status 404 is thrown.
    // Traces to: project-task-management-level1-spec.md — deleteWorkspace error path
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'project not found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { deleteWorkspace, ApiError, isApiError } = await import('./api')
    let thrown: unknown
    try {
      await deleteWorkspace('missing-id')
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(404)
  })
})

// ── updateWorkspace ─────────────────────────────────────────────────────────────

describe('updateWorkspace', () => {
  it('calls PUT /api/v1/workspaces/{id} and returns the updated Workspace', async () => {
    // BDD: Given an existing workspace id and a valid WorkspaceUpdateRequest,
    // When updateWorkspace("proj-123", { name: "Renamed" }) is called,
    // Then PUT /api/v1/workspaces/proj-123 is requested with the correct body,
    // And the updated workspace is returned.
    // Traces to: project-task-management-level1-spec.md — updateWorkspace shape
    const updated = {
      id: 'proj-123',
      name: 'Renamed',
      status: 'active',
      pinned: false,
      pin_order: 0,
      task_count: 0,
      created_at: '2026-06-01T00:00:00Z',
      updated_at: '2026-06-08T00:00:00Z',
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(updated))

    const { updateWorkspace } = await import('./api')
    const result = await updateWorkspace('proj-123', { name: 'Renamed' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces/proj-123')
    expect((init as RequestInit).method).toBe('PUT')

    // Verify request body contains the update fields.
    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.name).toBe('Renamed')

    // Verify returned project has the updated fields.
    expect(result.id).toBe('proj-123')
    expect(result.name).toBe('Renamed')
    expect(result.status).toBe('active')
  })

  it('differentiation test: updating two different projects returns different results', async () => {
    // Anti-hardcode: two PUT calls with different ids and bodies must produce different results.
    // Traces to: project-task-management-level1-spec.md — updateWorkspace differentiation
    const first = { id: 'id-one', name: 'First Updated', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-08T01:00:00Z' }
    const second = { id: 'id-two', name: 'Archived Project', status: 'archived', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-08T02:00:00Z' }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(first))
      .mockResolvedValueOnce(makeJsonResponse(second))

    const { updateWorkspace } = await import('./api')
    const r1 = await updateWorkspace('id-one', { name: 'First Updated' })
    const r2 = await updateWorkspace('id-two', { status: 'archived' })

    expect(r1.id).toBe('id-one')
    expect(r2.id).toBe('id-two')
    expect(r1.name).not.toBe(r2.name)
    expect(r1.status).not.toBe(r2.status)
  })

  it('throws ApiError on 404 response', async () => {
    // BDD: Given a project id that does not exist,
    // When updateWorkspace is called,
    // Then an ApiError with status 404 is thrown.
    // Traces to: project-task-management-level1-spec.md — updateWorkspace error path
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'project not found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { updateWorkspace, ApiError, isApiError } = await import('./api')
    let thrown: unknown
    try {
      await updateWorkspace('missing-id', { name: 'Ghost' })
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(404)
  })
})

// ── fetchWorkspaceSessions ──────────────────────────────────────────────────────

describe('fetchWorkspaceSessions', () => {
  it('calls GET /api/v1/workspaces/{id}/sessions and returns WorkspaceSessionLink array', async () => {
    // BDD: Given a workspace id with linked sessions,
    // When fetchWorkspaceSessions("proj-abc") is called,
    // Then GET /api/v1/workspaces/proj-abc/sessions is requested,
    // And the returned array contains the session links.
    // Traces to: project-task-management-level1-spec.md — fetchWorkspaceSessions shape
    const payload: { session_id: string; created_at: string }[] = [
      { session_id: 'sess_001', created_at: '2026-06-01T10:00:00Z' },
      { session_id: 'sess_002', created_at: '2026-06-01T11:00:00Z' },
    ]
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchWorkspaceSessions } = await import('./api')
    const result = await fetchWorkspaceSessions('proj-abc')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces/proj-abc/sessions')
    // GET request — no method override needed, but should not be a mutating method.
    expect((init as RequestInit | undefined)?.method).toBeUndefined()

    // Verify result is an array with the expected session links.
    expect(Array.isArray(result)).toBe(true)
    expect(result).toHaveLength(2)
    expect(result[0].session_id).toBe('sess_001')
    expect(result[1].session_id).toBe('sess_002')
    expect(result[0].created_at).toBe('2026-06-01T10:00:00Z')
  })

  it('differentiation test: two different workspace ids produce different session lists', async () => {
    // Anti-hardcode: two GET calls with different workspace ids must return different results.
    // Traces to: project-task-management-level1-spec.md — fetchWorkspaceSessions differentiation
    const sessions1 = [{ session_id: 'sess_alpha', created_at: '2026-06-01T10:00:00Z' }]
    const sessions2 = [
      { session_id: 'sess_beta', created_at: '2026-06-02T10:00:00Z' },
      { session_id: 'sess_gamma', created_at: '2026-06-02T11:00:00Z' },
    ]

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(sessions1))
      .mockResolvedValueOnce(makeJsonResponse(sessions2))

    const { fetchWorkspaceSessions } = await import('./api')
    const r1 = await fetchWorkspaceSessions('proj-x')
    const r2 = await fetchWorkspaceSessions('proj-y')

    expect(r1).toHaveLength(1)
    expect(r2).toHaveLength(2)
    expect(r1[0].session_id).not.toBe(r2[0].session_id)
  })

  it('returns empty array when workspace has no linked sessions', async () => {
    // BDD: Given a workspace with no sessions,
    // When fetchWorkspaceSessions is called,
    // Then an empty array is returned without error.
    // Traces to: project-task-management-level1-spec.md — fetchWorkspaceSessions empty
    fetchSpy.mockResolvedValueOnce(makeJsonResponse([]))

    const { fetchWorkspaceSessions } = await import('./api')
    const result = await fetchWorkspaceSessions('proj-empty')

    expect(result).toEqual([])
  })
})

// ── fetchTasks (unified Sprint 2 model) ──────────────────────────────────────
//
// Replaces the old fetchBoardTasks tests. The unified endpoint is GET /tasks
// (not /board/tasks). Returns Task[] directly (no wrapper object).

describe('fetchTasks', () => {
  it('calls GET /api/v1/tasks?status=inbox and returns the Task array', async () => {
    // BDD: Given a server that returns a task list,
    // When fetchTasks({ status: "inbox" }) is called,
    // Then GET /api/v1/tasks?status=inbox is requested,
    // And the returned array contains the tasks.
    // Traces to: sprint2-spec.md §6 — GET /tasks endpoint
    const payload = [
      { id: 't1', title: 'Fix bug', action: 'llm', status: 'inbox', priority: 3, workspace_id: 'ws-1', surface: 'user', owner: 'alice', created_by: 'alice', created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' },
      { id: 't2', title: 'Write tests', action: 'llm', status: 'inbox', priority: 2, workspace_id: 'ws-1', surface: 'user', owner: 'alice', created_by: 'alice', created_at: '2026-06-01T00:01:00Z', updated_at: '2026-06-01T00:01:00Z' },
    ]
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchTasks } = await import('./api')
    const result = await fetchTasks({ status: 'inbox' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/tasks')
    expect(url).toContain('status=inbox')

    expect(result).toHaveLength(2)
    expect(result[0].id).toBe('t1')
    expect(result[0].title).toBe('Fix bug')
    expect(result[1].id).toBe('t2')
  })

  it('calls GET /api/v1/tasks without query params when no filters given', async () => {
    // BDD: Given no filters,
    // When fetchTasks() is called,
    // Then URL does not contain ?status= or ?workspace_id=.
    fetchSpy.mockResolvedValueOnce(makeJsonResponse([]))

    const { fetchTasks } = await import('./api')
    const result = await fetchTasks()

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).not.toContain('status=')
    expect(url).not.toContain('workspace_id=')
    expect(result).toEqual([])
  })

  it('fetchTasks includes workspace_id in query string when provided', async () => {
    // BDD: Given fetchTasks({ workspace_id: 'proj-abc' }) is called,
    // When the API request is made,
    // Then the URL contains 'workspace_id=proj-abc'.
    // Traces to: sprint2-spec.md §6 — workspace-scoped task list
    const payload = [
      { id: 't-ws-1', title: 'Workspace task', action: 'llm', status: 'next', priority: 3, workspace_id: 'proj-abc', surface: 'user', owner: 'alice', created_by: 'alice', created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' },
    ]
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchTasks } = await import('./api')
    const result = await fetchTasks({ workspace_id: 'proj-abc' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]

    expect(url).toContain('workspace_id=proj-abc')
    expect(url).not.toContain('status=')

    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('t-ws-1')
    expect(result[0].title).toBe('Workspace task')
  })
})

// ── createTask (unified Sprint 2 model) ──────────────────────────────────────
//
// Replaces the old createBoardTask tests. POST /tasks, body is TaskCreateRequest
// (title, action, priority, workspace_id, surface — no status field, server seeds inbox).

describe('createTask', () => {
  it('calls POST /api/v1/tasks and returns the created Task', async () => {
    // BDD: Given a valid TaskCreateRequest,
    // When createTask({ title: "New task", action: "llm", ... }) is called,
    // Then POST /api/v1/tasks is requested with the correct body,
    // And the created Task (in inbox) is returned.
    // Traces to: sprint2-spec.md §6 — POST /tasks
    const created = {
      id: 'task-001',
      title: 'New task',
      action: 'llm',
      status: 'inbox',
      priority: 3,
      workspace_id: 'ws-test',
      surface: 'user',
      owner: 'alice',
      created_by: 'alice',
      created_at: '2026-06-08T10:00:00Z',
      updated_at: '2026-06-08T10:00:00Z',
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(created, 201))

    const { createTask } = await import('./api')
    const result = await createTask({ title: 'New task', action: 'llm', priority: 3, workspace_id: 'ws-test', surface: 'user' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/tasks')
    expect((init as RequestInit).method).toBe('POST')

    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.title).toBe('New task')
    expect(body.action).toBe('llm')

    expect(result.id).toBe('task-001')
    expect(result.title).toBe('New task')
    expect(result.status).toBe('inbox')
  })

  it('differentiation test: creating two different tasks returns different Tasks', async () => {
    // Anti-hardcode: two POST calls with different titles must produce different results.
    const task1 = {
      id: 'task-alpha', title: 'Alpha task', action: 'llm', status: 'inbox', priority: 3,
      workspace_id: 'ws-1', surface: 'user', owner: 'alice', created_by: 'alice',
      created_at: '2026-06-08T10:00:00Z', updated_at: '2026-06-08T10:00:00Z',
    }
    const task2 = {
      id: 'task-beta', title: 'Beta task', action: 'llm', status: 'inbox', priority: 1,
      workspace_id: 'ws-1', surface: 'user', owner: 'alice', created_by: 'alice',
      created_at: '2026-06-08T11:00:00Z', updated_at: '2026-06-08T11:00:00Z',
    }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(task1, 201))
      .mockResolvedValueOnce(makeJsonResponse(task2, 201))

    const { createTask } = await import('./api')
    const r1 = await createTask({ title: 'Alpha task', action: 'llm', priority: 3, workspace_id: 'ws-1', surface: 'user' })
    const r2 = await createTask({ title: 'Beta task', action: 'llm', priority: 1, workspace_id: 'ws-1', surface: 'user' })

    expect(r1.id).toBe('task-alpha')
    expect(r2.id).toBe('task-beta')
    expect(r1.id).not.toBe(r2.id)
    expect(r1.title).not.toBe(r2.title)
  })

  it('throws ApiError on 400 response', async () => {
    // BDD: Given an invalid request body,
    // When createTask is called,
    // Then an ApiError with status 400 is thrown.
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'title is required' }), {
        status: 400,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { createTask, ApiError, isApiError } = await import('./api')
    let thrown: unknown
    try {
      await createTask({ title: '', action: 'llm', priority: 3, workspace_id: 'ws-1', surface: 'user' })
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(400)
  })
})

// ── updateTask (unified Sprint 2 model) ──────────────────────────────────────
//
// Replaces the old updateBoardTask tests. PATCH /tasks/{id} (not PUT /board/tasks/{id}).
// Accepts 7-state status vocab: inbox/next/planning/in_progress/blocked/done/failed.

describe('updateTask', () => {
  it('calls PATCH /api/v1/tasks/{id} and returns the updated Task', async () => {
    // BDD: Given an existing task id and a TaskUpdateRequest,
    // When updateTask("task-001", { status: "next" }) is called,
    // Then PATCH /api/v1/tasks/task-001 is requested with the correct body,
    // And the updated Task is returned.
    // Traces to: sprint2-spec.md §6 — PATCH /tasks/{id}
    const updated = {
      id: 'task-001',
      title: 'Fix bug',
      action: 'llm',
      status: 'next',
      priority: 3,
      workspace_id: 'ws-1',
      surface: 'user',
      owner: 'alice',
      created_by: 'alice',
      created_at: '2026-06-08T10:00:00Z',
      updated_at: '2026-06-08T12:00:00Z',
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(updated))

    const { updateTask } = await import('./api')
    const result = await updateTask('task-001', { status: 'next' })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/tasks/task-001')
    expect((init as RequestInit).method).toBe('PATCH')

    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.status).toBe('next')

    expect(result.id).toBe('task-001')
    expect(result.status).toBe('next')
  })

  it('accepts all 7 status values', async () => {
    // Verify the 7-state lifecycle statuses are accepted as update values.
    const statuses = ['inbox', 'next', 'planning', 'in_progress', 'blocked', 'done', 'failed'] as const
    for (const status of statuses) {
      const updated = {
        id: 'task-001', title: 'T', action: 'llm', status, priority: 3,
        workspace_id: 'ws-1', surface: 'user', owner: 'alice', created_by: 'alice',
        created_at: '2026-06-08T10:00:00Z', updated_at: '2026-06-08T12:00:00Z',
      }
      fetchSpy.mockResolvedValueOnce(makeJsonResponse(updated))
      const { updateTask } = await import('./api')
      const result = await updateTask('task-001', { status })
      expect(result.status).toBe(status)
    }
  })

  it('differentiation test: updating two different tasks returns different results', async () => {
    const t1 = {
      id: 'task-x', title: 'Task X', action: 'llm', status: 'in_progress', priority: 2,
      workspace_id: 'ws-1', surface: 'user', owner: 'alice', created_by: 'alice',
      created_at: '2026-06-08T10:00:00Z', updated_at: '2026-06-08T13:00:00Z',
    }
    const t2 = {
      id: 'task-y', title: 'Task Y Done', action: 'llm', status: 'done', priority: 3,
      workspace_id: 'ws-1', surface: 'user', owner: 'alice', created_by: 'alice',
      created_at: '2026-06-08T10:00:00Z', updated_at: '2026-06-08T14:00:00Z',
    }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(t1))
      .mockResolvedValueOnce(makeJsonResponse(t2))

    const { updateTask } = await import('./api')
    const r1 = await updateTask('task-x', { status: 'in_progress' })
    const r2 = await updateTask('task-y', { status: 'done' })

    expect(r1.id).toBe('task-x')
    expect(r2.id).toBe('task-y')
    expect(r1.status).not.toBe(r2.status)
  })
})

// ── deleteTask (unified Sprint 2 model) ──────────────────────────────────────
//
// Replaces the old deleteBoardTask tests. DELETE /tasks/{id} (not /board/tasks/{id}).

describe('deleteTask', () => {
  it('calls DELETE /api/v1/tasks/{id} and resolves without error', async () => {
    // BDD: Given an existing task id,
    // When deleteTask("task-del-001") is called,
    // Then DELETE /api/v1/tasks/task-del-001 is requested,
    // And the function resolves without throwing.
    // Traces to: sprint2-spec.md §6 — DELETE /tasks/{id}
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteTask } = await import('./api')
    await expect(deleteTask('task-del-001')).resolves.not.toThrow()

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/tasks/task-del-001')
    expect((init as RequestInit).method).toBe('DELETE')
  })

  it('sends the correct encoded id in the URL', async () => {
    fetchSpy.mockResolvedValueOnce(makeJsonResponse({}, 200))

    const { deleteTask } = await import('./api')
    await deleteTask('task-xyz-999')

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('task-xyz-999')
  })

  it('throws ApiError on 404 response', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'task not found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { deleteTask, ApiError, isApiError } = await import('./api')
    let thrown: unknown
    try {
      await deleteTask('missing-task-id')
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(404)
  })
})

// ── fetchTokenStats ───────────────────────────────────────────────────────────

describe('fetchTokenStats', () => {
  it('calls GET /api/v1/stats/tokens?period=month and returns TokenUsageSummary', async () => {
    // BDD: Given a server that returns a token usage summary,
    // When fetchTokenStats() is called,
    // Then GET /api/v1/stats/tokens?period=month is requested,
    // And the returned value has period_start, period_end, and agents array.
    // Traces to: project-task-management-level1-spec.md — fetchTokenStats shape
    const payload = {
      agents: [
        { agent_id: 'mia', agent_name: 'Mia', tokens_in: 1000, tokens_out: 500, tokens_total: 1500 },
        { agent_id: 'jim', agent_name: 'Jim', tokens_in: 2000, tokens_out: 1000, tokens_total: 3000 },
      ],
      period_start: '2026-06-01T00:00:00Z',
      period_end: '2026-07-01T00:00:00Z',
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchTokenStats } = await import('./api')
    const result = await fetchTokenStats()

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/stats/tokens')
    expect(url).toContain('period=month')

    // Verify returned summary has correct structure and content.
    expect(result.period_start).toBe('2026-06-01T00:00:00Z')
    expect(result.period_end).toBe('2026-07-01T00:00:00Z')
    expect(Array.isArray(result.agents)).toBe(true)
    expect(result.agents).toHaveLength(2)
    expect(result.agents[0].agent_id).toBe('mia')
    expect(result.agents[0].tokens_total).toBe(1500)
    expect(result.agents[1].agent_id).toBe('jim')
  })

  it('differentiation test: two different summary responses return different data', async () => {
    // Anti-hardcode: two calls must return distinct data.
    // Traces to: project-task-management-level1-spec.md — fetchTokenStats differentiation
    const summary1 = {
      agents: [{ agent_id: 'mia', agent_name: 'Mia', tokens_in: 100, tokens_out: 50, tokens_total: 150 }],
      period_start: '2026-05-01T00:00:00Z',
      period_end: '2026-06-01T00:00:00Z',
    }
    const summary2 = {
      agents: [{ agent_id: 'ava', agent_name: 'Ava', tokens_in: 9999, tokens_out: 4000, tokens_total: 13999 }],
      period_start: '2026-06-01T00:00:00Z',
      period_end: '2026-07-01T00:00:00Z',
    }

    fetchSpy
      .mockResolvedValueOnce(makeJsonResponse(summary1))
      .mockResolvedValueOnce(makeJsonResponse(summary2))

    const { fetchTokenStats } = await import('./api')
    const r1 = await fetchTokenStats()
    const r2 = await fetchTokenStats()

    expect(r1.period_start).not.toBe(r2.period_start)
    expect(r1.agents[0].agent_id).not.toBe(r2.agents[0].agent_id)
    expect(r1.agents[0].tokens_total).not.toBe(r2.agents[0].tokens_total)
  })

  it('throws ApiError on 500 response', async () => {
    // BDD: Given a server error,
    // When fetchTokenStats is called,
    // Then an ApiError with status 500 is thrown.
    // Traces to: project-task-management-level1-spec.md — fetchTokenStats error path
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'internal server error' }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchTokenStats, ApiError, isApiError } = await import('./api')
    let thrown: unknown
    try {
      await fetchTokenStats()
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(500)
  })
})
