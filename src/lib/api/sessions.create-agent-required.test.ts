// sessions.create-agent-required.test.ts — K16 (omnipus-system-agent FR-122):
// SessionCreateRequest.agent_id is REQUIRED and nonblank; there is no default
// agent. POST /sessions refuses a missing or blank one with 400 "an agent
// must be named" — the SPA refuses it client-side (createSession) instead of
// spending the round-trip, with the server's own wording.
//
// Real adapter path: the global fetch is stubbed at the network edge (same
// harness as sessions.has-earlier.test.ts); request() runs for real.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession } from './sessions'

function stubCookieLocal(value: string) {
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => value,
  })
}

function restoreCookieLocal() {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

const validSession = {
  id: 'sid-new',
  type: 'chat',
  agent_id: 'mia',
  title: 'New chat',
  status: 'active',
  created_at: '2026-10-14T09:00:00Z',
  updated_at: '2026-10-14T09:00:00Z',
  stats: {
    tokens_in: 0,
    tokens_out: 0,
    tokens_total: 0,
    cost: 0,
    tool_calls: 0,
    message_count: 0,
  },
  channel: 'webchat',
  partitions: ['2026-10-14'],
}

describe('createSession — agent_id is required and nonblank (K16)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookieLocal('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal()
  })

  it.each([
    ['empty string', ''],
    ['whitespace-only', '   '],
  ])('refuses a %s agent_id client-side — fetch never called, server wording kept', async (_label, agentId) => {
    await expect(createSession(agentId)).rejects.toThrow('an agent must be named')
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('sends a named agent on the wire — body carries the nonblank agent_id', async () => {
    fetchSpy.mockResolvedValue(
      new Response(JSON.stringify(validSession), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const session = await createSession('mia')
    expect(session.agent_id).toBe('mia')
    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(String(init.method)).toBe('POST')
    expect(JSON.parse(String(init.body))).toMatchObject({ agent_id: 'mia' })
  })
})
