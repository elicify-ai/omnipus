// U2 — the serialization seam between the "Open browser" launcher and
// POST /sessions.
//
// Why this file exists at all. ChatControls.test.tsx mocks `api.createSession`
// and asserts the ARGUMENTS the launcher passes; browser_launcher_workspace_test.go
// drives the real POST /sessions handler with a hand-written JSON body. Both
// were green while the wire between them was untested — a `workspaceId` key
// instead of `workspace_id`, or a value dropped in the body builder, would have
// left every one of those assertions passing and the operator still refused,
// which is exactly the shape of the bug this unit is fixing. This file asserts
// the bytes.
//
// The stakes are not cosmetic: the live browser panel decides which workspace's
// browser — and whose live logins — it shows by reading the workspace off the
// created session's own meta, server-side (ADR-075 FR-016/FR-017). If the id
// never reaches the request body, the session names no workspace, and an agent
// on more than one workspace's team is refused as ambiguous while being told to
// open the panel from a workspace chat — which is where the click came from.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

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

/** A wire Session that satisfies the generated zod schema `request()` validates against. */
function wireSession(overrides: Record<string, unknown> = {}) {
  return {
    id: 'session_new',
    agent_id: 'mia',
    title: '',
    status: 'active',
    type: 'chat',
    channel: 'webchat',
    partitions: [],
    created_at: '2026-07-11T00:00:00Z',
    updated_at: '2026-07-11T00:00:00Z',
    stats: {
      tokens_in: 0,
      tokens_out: 0,
      tokens_total: 0,
      cost: 0,
      tool_calls: 0,
      message_count: 0,
    },
    ...overrides,
  }
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
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  sessionStorage.clear()
  restoreCookie()
})

/** The JSON body of the single fetch call the spy recorded. */
function sentBody(): Record<string, unknown> {
  expect(fetchSpy).toHaveBeenCalledTimes(1)
  const init = fetchSpy.mock.calls[0][1] as RequestInit
  return JSON.parse(String(init.body)) as Record<string, unknown>
}

describe('createSession — the workspace travels with the create', () => {
  it('puts the workspace id in the POST body under the contract key workspace_id', async () => {
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse(wireSession({ workspace_id: '01M1H9JS5EHRYWDBM0BYA45NFM' })),
    )

    const { createSession } = await import('./api')
    const created = await createSession('mia', '01M1H9JS5EHRYWDBM0BYA45NFM')

    const url = String(fetchSpy.mock.calls[0][0])
    expect(url).toContain('/sessions')
    // The exact key the contract names — `workspaceId` here would be silently
    // dropped by the server and the panel would refuse with nothing to read.
    expect(sentBody()).toEqual({
      agent_id: 'mia',
      workspace_id: '01M1H9JS5EHRYWDBM0BYA45NFM',
    })
    // And the stamp the server echoes back is carried through, so the caller
    // can see which workspace the session actually belongs to.
    expect(created.workspace_id).toBe('01M1H9JS5EHRYWDBM0BYA45NFM')
  })

  it('omits the key entirely for the global chat rather than sending an empty one', async () => {
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(wireSession()))

    const { createSession } = await import('./api')
    await createSession('mia')

    const body = sentBody()
    expect(body).toEqual({ agent_id: 'mia' })
    // Not `workspace_id: ""` — "" is not a workspace, and an absent value must
    // stay absent rather than become a guess at a default.
    expect('workspace_id' in body).toBe(false)
  })

  it('treats a whitespace-only workspace id as no workspace at all', async () => {
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(wireSession()))

    const { createSession } = await import('./api')
    await createSession('mia', '   ')

    expect('workspace_id' in sentBody()).toBe(false)
  })
})
