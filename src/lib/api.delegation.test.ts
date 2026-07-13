// BDD: API client functions for the per-workspace delegation graph (M5).
// Traces to: sprint4-ui-spec.md Team tab #6 + WorkspaceDelegation contract.
//
// Mirrors api.workspaces.test.ts: stubs global fetch and asserts the request
// method + path + body shape for fetchWorkspaceDelegation / updateWorkspaceDelegation.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { WorkspaceDelegationEdge } from './api'

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

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchSpy = vi.fn()
  vi.stubGlobal('fetch', fetchSpy)
  stubCookie('__Host-csrf=test-csrf-token')
  sessionStorage.setItem('omnipus_auth_token', 'test-bearer')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  sessionStorage.clear()
  restoreCookie()
})

describe('fetchWorkspaceDelegation', () => {
  it('calls GET /api/v1/workspaces/{id}/delegation and returns the graph', async () => {
    // Given the server returns a delegation graph for a workspace,
    // When fetchWorkspaceDelegation(id) is called,
    // Then GET /api/v1/workspaces/{id}/delegation is requested (no method override),
    // And the workspace_id, edges, and team are returned.
    const payload = {
      workspace_id: 'ws-1',
      team: ['mia', 'jim', 'planner'],
      edges: [
        { from_agent: 'jim', to_agent: 'planner', modes: ['direct', 'task'], depth: 3 },
      ],
      default_depth: 3,
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(payload))

    const { fetchWorkspaceDelegation } = await import('./api')
    const result = await fetchWorkspaceDelegation('ws-1')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces/ws-1/delegation')
    // GET — no mutating method.
    expect((init as RequestInit | undefined)?.method).toBeUndefined()

    expect(result.workspace_id).toBe('ws-1')
    expect(result.team).toEqual(['mia', 'jim', 'planner'])
    expect(result.edges).toHaveLength(1)
    expect(result.edges[0].from_agent).toBe('jim')
    expect(result.edges[0].to_agent).toBe('planner')
    expect(result.edges[0].modes).toEqual(['direct', 'task'])
    expect(result.edges[0].depth).toBe(3)
    expect(result.default_depth).toBe(3)
  })

  it('encodes the workspace id in the URL', async () => {
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse({ workspace_id: 'ws weird/id', edges: [], default_depth: 3 }),
    )
    const { fetchWorkspaceDelegation } = await import('./api')
    await fetchWorkspaceDelegation('ws weird/id')
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces/ws%20weird%2Fid/delegation')
  })

  it('throws ApiError on 404 response', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'workspace not found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchWorkspaceDelegation, isApiError, ApiError } = await import('./api')
    let thrown: unknown
    try {
      await fetchWorkspaceDelegation('missing')
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(404)
  })
})

describe('updateWorkspaceDelegation', () => {
  it('calls PUT /api/v1/workspaces/{id}/delegation with an { edges } body', async () => {
    // Given a set of delegation edges,
    // When updateWorkspaceDelegation(id, edges) is called,
    // Then PUT /api/v1/workspaces/{id}/delegation is requested,
    // And the body is { edges: [...] } (the WorkspaceDelegationUpdateRequest shape).
    const edges: WorkspaceDelegationEdge[] = [
      { from_agent: 'jim', to_agent: 'explorer', modes: ['direct'], depth: 2 },
    ]
    const resp = {
      workspace_id: 'ws-2',
      team: ['jim', 'explorer'],
      edges: [{ from_agent: 'jim', to_agent: 'explorer', modes: ['direct'], depth: 2 }],
      default_depth: 3,
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(resp))

    const { updateWorkspaceDelegation } = await import('./api')
    const result = await updateWorkspaceDelegation('ws-2', [...edges])

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/workspaces/ws-2/delegation')
    expect((init as RequestInit).method).toBe('PUT')

    // Body must be { edges: [...] } — not a bare array, not a different wrapper.
    const body = JSON.parse((init as RequestInit).body as string)
    expect(Array.isArray(body)).toBe(false)
    expect(Array.isArray(body.edges)).toBe(true)
    expect(body.edges).toHaveLength(1)
    expect(body.edges[0].from_agent).toBe('jim')
    expect(body.edges[0].to_agent).toBe('explorer')
    expect(body.edges[0].modes).toEqual(['direct'])
    expect(body.edges[0].depth).toBe(2)

    expect(result.workspace_id).toBe('ws-2')
    expect(result.edges[0].to_agent).toBe('explorer')
  })

  it('sends { edges: [] } to clear all delegation', async () => {
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse({ workspace_id: 'ws-3', team: ['mia'], edges: [], default_depth: 3 }),
    )
    const { updateWorkspaceDelegation } = await import('./api')
    await updateWorkspaceDelegation('ws-3', [])
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse((init as RequestInit).body as string)
    expect(body.edges).toEqual([])
  })

  it('differentiation: two workspaces hit different URLs with different bodies', async () => {
    fetchSpy
      .mockResolvedValueOnce(
        makeJsonResponse({ workspace_id: 'ws-a', edges: [], default_depth: 3 }),
      )
      .mockResolvedValueOnce(
        makeJsonResponse({ workspace_id: 'ws-b', edges: [], default_depth: 3 }),
      )
    const { updateWorkspaceDelegation } = await import('./api')
    await updateWorkspaceDelegation('ws-a', [
      { from_agent: 'mia', to_agent: 'jim', modes: ['direct'] },
    ])
    await updateWorkspaceDelegation('ws-b', [
      { from_agent: 'jim', to_agent: 'ray', modes: ['task'] },
    ])
    const [url1, init1] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const [url2, init2] = fetchSpy.mock.calls[1] as [string, RequestInit]
    expect(url1).toContain('/workspaces/ws-a/delegation')
    expect(url2).toContain('/workspaces/ws-b/delegation')
    expect(url1).not.toBe(url2)
    const b1 = JSON.parse((init1 as RequestInit).body as string)
    const b2 = JSON.parse((init2 as RequestInit).body as string)
    expect(b1.edges[0].from_agent).toBe('mia')
    expect(b2.edges[0].from_agent).toBe('jim')
  })

  it('throws ApiError on 422 (self-edge / invalid agent rejected by server)', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'self-edge rejected' }), {
        status: 422,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { updateWorkspaceDelegation, isApiError, ApiError } = await import('./api')
    let thrown: unknown
    try {
      await updateWorkspaceDelegation('ws-x', [
        { from_agent: 'mia', to_agent: 'mia', modes: ['direct'] },
      ])
    } catch (err) {
      thrown = err
    }
    expect(isApiError(thrown)).toBe(true)
    expect(thrown instanceof ApiError && thrown.status).toBe(422)
  })
})
