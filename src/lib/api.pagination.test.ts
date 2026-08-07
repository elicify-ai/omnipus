// ADR-057 FR-091/FR-098 post-review fix: GET /sessions is paginated
// server-side (default limit 50, pkg/gateway/rest.go's
// u18DefaultSessionPageLimit). Before this fix, fetchSessions() discarded
// `next_cursor` and returned only page 1 — every production caller
// (Sidebar, SearchModal, UsageScreen) silently saw a truncated set as if it
// were complete. These tests exercise the REAL fetchSessions()/
// fetchSessionPage() implementation against a mocked `fetch`, proving the
// wrapper now exhausts every page.
//
// New file (not appended to the existing api.test.ts) per this repo's
// per-unit test-file convention (see api.session-tree.test.ts's header).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { fetchSessions } from './api'

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

// Minimal, schema-valid wire RawSession (contracts/components/schemas/Session.yaml
// via the generated Zod schema) — every field the schema requires, nothing more.
function wireSession(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    agent_id: 'agent-1',
    title: `Session ${id}`,
    status: 'active',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
    channel: 'webchat',
    partitions: [],
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

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('fetchSessions: pagination completeness (post-review fix)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookie('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    restoreCookie()
  })

  it('pages through next_cursor and returns the COMPLETE set, not just page 1 (does not silently truncate)', async () => {
    // Page 1: 2 sessions + next_cursor pointing past them.
    fetchSpy.mockResolvedValueOnce(
      jsonResponse({ sessions: [wireSession('s1'), wireSession('s2')], next_cursor: '2' }),
    )
    // Page 2: 1 more session, no next_cursor — end of the list.
    fetchSpy.mockResolvedValueOnce(
      jsonResponse({ sessions: [wireSession('s3')] }),
    )

    const result = await fetchSessions()

    // Positive lower bound (anti-vacuity): assert the EXACT set, including
    // the id that only exists on page 2 — a broken loop that stopped after
    // page 1 would fail this with only ['s1', 's2'].
    expect(result.map((s) => s.id).sort()).toEqual(['s1', 's2', 's3'])
    expect(result).toHaveLength(3)

    // Exactly two requests were made, and the second one carried the cursor
    // from the first as its offset — proves the loop actually re-sent
    // next_cursor, not just happened to fetch twice for some other reason.
    expect(fetchSpy).toHaveBeenCalledTimes(2)
    const secondUrl = String(fetchSpy.mock.calls[1][0])
    expect(secondUrl).toContain('offset=2')
  })

  it('stops after a single fetch when the server reports no next_cursor (no truncation, no runaway loop)', async () => {
    fetchSpy.mockResolvedValueOnce(
      jsonResponse({ sessions: [wireSession('only-1'), wireSession('only-2')] }),
    )

    const result = await fetchSessions()

    expect(result.map((s) => s.id).sort()).toEqual(['only-1', 'only-2'])
    expect(fetchSpy).toHaveBeenCalledTimes(1)
  })

  it('three-page fan-out: every page contributes its sessions to the final result', async () => {
    fetchSpy
      .mockResolvedValueOnce(jsonResponse({ sessions: [wireSession('a')], next_cursor: '1' }))
      .mockResolvedValueOnce(jsonResponse({ sessions: [wireSession('b')], next_cursor: '2' }))
      .mockResolvedValueOnce(jsonResponse({ sessions: [wireSession('c')] }))

    const result = await fetchSessions(undefined, undefined, { flat: true })

    expect(result.map((s) => s.id).sort()).toEqual(['a', 'b', 'c'])
    expect(fetchSpy).toHaveBeenCalledTimes(3)
  })

  it('safety valve: aborts after MAX_PAGES rather than looping forever against a server that never stops paging', async () => {
    // Every response reports a next_cursor equal to its own call index, so
    // the loop would run forever without the cap. Real max-page counts on
    // any actual install are nowhere near this.
    let call = 0
    fetchSpy.mockImplementation(() => {
      const cursor = String(call)
      call += 1
      return Promise.resolve(jsonResponse({ sessions: [wireSession(`x${cursor}`)], next_cursor: String(call) }))
    })

    const result = await fetchSessions()

    // Capped at MAX_PAGES (1000) fetch calls, not an infinite loop — and the
    // capped result is still non-trivial (positive lower bound), not empty.
    expect(fetchSpy).toHaveBeenCalledTimes(1000)
    expect(result.length).toBe(1000)
  }, 20_000)
})
