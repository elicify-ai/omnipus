// sessions.has-earlier.test.ts — the two REST carriers of has_earlier_messages
// (founder ruling 2026-10-11): SessionDetail.has_earlier_messages on
// GET /sessions/{id} (contracts/components/schemas/SessionDetail.yaml — "true
// when messages starts at the session's last /clear marker and earlier
// transcript entries exist before it"; absent when the view is the whole
// transcript) and the X-Has-Earlier-Messages response header on
// GET /sessions/{id}/messages (contracts/openapi.yaml — "true"|"false",
// always present; the body stays a bare array).
//
// Real adapter path: the global fetch is stubbed at the network edge;
// request(), the Zod shape validation, parseWireMessageList and rawToMessage
// all run for real.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

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
  id: 'sid-earlier',
  type: 'chat',
  agent_id: 'agent-1',
  title: 'Cleared session',
  status: 'active',
  created_at: '2026-10-10T09:00:00Z',
  updated_at: '2026-10-10T09:00:00Z',
  stats: {
    tokens_in: 0,
    tokens_out: 0,
    tokens_total: 0,
    cost: 0,
    tool_calls: 0,
    message_count: 0,
  },
  channel: 'webchat',
  partitions: ['2026-10-10'],
}

describe('has_earlier_messages carriers', () => {
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
    vi.resetModules()
  })

  it('GET /sessions/{id} — SessionDetail.has_earlier_messages: true passes through', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ session: validSession, messages: [], has_earlier_messages: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionDetail } = await import('./sessions')
    const detail = await fetchSessionDetail('sid-earlier')
    expect(detail.has_earlier_messages).toBe(true)
  })

  it('GET /sessions/{id} — absent has_earlier_messages (whole transcript) stays undefined', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ session: validSession, messages: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionDetail } = await import('./sessions')
    const detail = await fetchSessionDetail('sid-earlier')
    expect(detail.has_earlier_messages).toBeUndefined()
  })

  it('GET /sessions/{id}/messages — the X-Has-Earlier-Messages header lands in the keyed record', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { 'Content-Type': 'application/json', 'X-Has-Earlier-Messages': 'true' },
      }),
    )
    const { fetchSessionMessages, sessionMessagesHasEarlier } = await import('./sessions')
    await fetchSessionMessages('sid-earlier')
    expect(sessionMessagesHasEarlier['sid-earlier']).toBe(true)
  })

  it('GET /sessions/{id}/messages — a "false" header records false, not true', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { 'Content-Type': 'application/json', 'X-Has-Earlier-Messages': 'false' },
      }),
    )
    const { fetchSessionMessages, sessionMessagesHasEarlier } = await import('./sessions')
    await fetchSessionMessages('sid-earlier')
    expect(sessionMessagesHasEarlier['sid-earlier']).toBe(false)
  })

  it('GET /sessions/{id}/messages — a missing header records false (no invented true)', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, sessionMessagesHasEarlier } = await import('./sessions')
    await fetchSessionMessages('sid-earlier')
    expect(sessionMessagesHasEarlier['sid-earlier']).toBe(false)
  })
})
