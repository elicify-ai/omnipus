// Existing HTTP decoder oracles relocated intact from the oversized API suite.
// D10/D15 changes only the response envelope and normalized result boundary.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { wireHistoryPage } from '../../tests/chat-history-paging/legacy-wire'
import { expectHistoryPage } from '../../tests/chat-history-paging/legacy-fixtures'

// ── fetchSessionMessages renames parameters → params ─────────────────────────────
//
// Regression test for Problem 1. The wire ToolCall schema emits `parameters`
// (matching Go json tag `json:"parameters,omitempty"`). The SPA ToolCall type
// uses `params`. Without the rawToToolCall() transform, params was `undefined`
// and ToolCallBadge rendered `JSON.stringify(undefined, null, 2)` = "undefined".
//
// This test stubs fetch to return the wire shape (with `parameters`) and
// asserts that the returned page.entries[].tool_calls[].params is correctly populated.

describe('fetchSessionMessages: wire parameters → SPA params transform', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal() {
    Reflect.deleteProperty(document, 'cookie')
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    // fetchSessionMessages is a GET, no CSRF needed — but setting the cookie
    // avoids CSRF guard triggering on any incidental state-changing helpers.
    stubCookieLocal('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal()
    vi.resetModules()
  })

  it('renames tool_calls[].parameters to tool_calls[].params', async () => {
    // Wire payload: ToolCall uses `parameters`, not `params`.
    const wirePayload = [
      {
        id: 'msg-1',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'here is the result',
        timestamp: '2026-05-18T10:00:00Z',
        status: 'ok',
        tool_calls: [
          {
            id: 'tc-1',
            tool: 'foo_tool',
            status: 'success',
            parameters: { x: 1, y: 'hello' },
          },
        ],
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-abc', wirePayload, 2)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-abc')
    expectHistoryPage(decoded)
    const messages = decoded.entries

    // The returned message should have the transformed tool call.
    expect(messages).toHaveLength(1)
    expect(messages[0].tool_calls).toHaveLength(1)
    // params must equal the wire `parameters` value — NOT undefined.
    expect(messages[0].tool_calls![0].params).toEqual({ x: 1, y: 'hello' })
    // The raw `parameters` key must NOT appear on the SPA type.
    expect(Reflect.get(messages[0].tool_calls![0], 'parameters')).toBeUndefined()
  })

  it('returns empty params ({}) when wire parameters field is absent', async () => {
    // Wire ToolCall with no parameters field — params should default to {}.
    const wirePayload = [
      {
        id: 'msg-2',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'done',
        timestamp: '2026-05-18T10:01:00Z',
        tool_calls: [
          {
            id: 'tc-2',
            tool: 'bar_tool',
            status: 'success',
          },
        ],
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-xyz', wirePayload, 2)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-xyz')
    expectHistoryPage(decoded)
    const messages = decoded.entries

    expect(messages[0].tool_calls![0].params).toEqual({})
  })

  it('maps wire status "ok" → SPA status "done"', async () => {
    const wirePayload = [
      {
        id: 'msg-3',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'test',
        timestamp: '2026-05-18T10:02:00Z',
        status: 'ok',
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-status', wirePayload, 1)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-status')
    expectHistoryPage(decoded)
    const messages = decoded.entries

    expect(messages[0].status).toBe('done')
  })

  it('maps wire tool_call status "denied" → SPA status "cancelled"', async () => {
    const wirePayload = [
      {
        id: 'msg-4',
        agent_id: 'agent-1',
        role: 'assistant',
        content: '',
        timestamp: '2026-05-18T10:03:00Z',
        tool_calls: [
          { id: 'tc-denied', tool: 'restricted_tool', status: 'denied' },
        ],
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-denied', wirePayload, 1)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-denied')
    expectHistoryPage(decoded)
    const messages = decoded.entries

    expect(messages[0].tool_calls![0].status).toBe('cancelled')
  })


})

// ── fetchSessionMessages renames parameters → params ─────────────────────────────
//
// Regression test for Problem 1. The wire ToolCall schema emits `parameters`
// (matching Go json tag `json:"parameters,omitempty"`). The SPA ToolCall type
// uses `params`. Without the rawToToolCall() transform, params was `undefined`
// and ToolCallBadge rendered `JSON.stringify(undefined, null, 2)` = "undefined".
//
// This test stubs fetch to return the wire shape (with `parameters`) and
// asserts that the returned page.entries[].tool_calls[].params is correctly populated.

describe('fetchSessionMessages: wire parameters → SPA params transform', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal() {
    Reflect.deleteProperty(document, 'cookie')
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    // fetchSessionMessages is a GET, no CSRF needed — but setting the cookie
    // avoids CSRF guard triggering on any incidental state-changing helpers.
    stubCookieLocal('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal()
    vi.resetModules()
  })

  it('carries per-message wire agent_id onto SPA message.agentId (handover replay)', async () => {
    // Regression for the handover reload bug: cold-load (REST) transcripts must
    // expose each message's authoring agent so the row renders under its true
    // author. Mia's pre-handover turn stays Mia; Jim's post-handover turn is Jim.
    const wirePayload = [
      {
        id: 'm-mia',
        agent_id: 'mia',
        role: 'assistant',
        content: 'I will hand this to Jim.',
        timestamp: '2026-05-18T10:00:00Z',
        status: 'ok',
      },
      {
        id: 'm-jim',
        agent_id: 'jim',
        role: 'assistant',
        content: 'On it.',
        timestamp: '2026-05-18T10:01:00Z',
        status: 'ok',
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-handover', wirePayload, 2)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-handover')
    expectHistoryPage(decoded)
    const messages = decoded.entries

    expect(messages).toHaveLength(2)
    expect(messages[0].agentId).toBe('mia')
    expect(messages[1].agentId).toBe('jim')
  })

  // Regression guards for the 2026-05-21 production bug: Message.yaml's
  // `type` enum was missing "tool_call" and "turn_canceled". A jim session
  // with 44 tool_call entries failed the SPA's Zod validation with
  // "Backend response failed validation". After the schema fix these
  // shapes must round-trip without ApiSchemaError.

  it('accepts type:"tool_call" entries (regression for 2026-05-21 bug)', async () => {
    const wirePayload = [
      {
        id: 'call_abc',
        type: 'tool_call',
        agent_id: 'jim',
        timestamp: '2026-05-21T04:20:00Z',
        tool_calls: [
          {
            id: 'call_abc',
            tool: 'write_file',
            status: 'success',
            parameters: { path: '/tmp/x.txt' },
          },
        ],
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-toolcall', wirePayload, 1)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-toolcall')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(1)
    expect(messages[0].id).toBe('call_abc')
  })

  it('accepts type:"turn_canceled" entries with cancel-specific fields', async () => {
    // Includes the cancel-specific fields added to Message.yaml in this
    // branch (turn_id, canceled_by_user, canceled_by_channel, cancel_method,
    // descendants_canceled). These were silently stripped by Zod's non-strict
    // object default; now they're modelled in the schema.
    const wirePayload = [
      {
        id: 'cancel_xyz',
        type: 'turn_canceled',
        agent_id: 'mia',
        timestamp: '2026-05-21T04:25:00Z',
        turn_id: 'turn-T3',
        canceled_by_user: 'admin',
        canceled_by_channel: 'webchat',
        cancel_method: 'graceful',
        descendants_canceled: ['turn-T3-sub-1'],
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-cancel', wirePayload, 0)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-cancel')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(1)
    expect(messages[0].id).toBe('cancel_xyz')
  })

  it('forwards type:"judge_verdict" and the verdict payload onto the SPA message (ADR-049 D2/SD-C10)', async () => {
    // Regression for the "verdict card never appears" bug: rawToMessage's
    // `role: 'system'` branch used to build a SystemMessage without ever
    // reading raw.type/raw.verdict, so a cold-loaded (REST) judge_verdict
    // transcript entry silently lost the one field
    // (shouldRenderJudgeVerdictInThread + JudgeVerdictThreadCard,
    // ChatScreen.tsx) needs to tell it apart from an ordinary system
    // message and render the card.
    const wirePayload = [
      {
        id: 'goal-sid-verdict-judge-1',
        type: 'judge_verdict',
        role: 'system',
        agent_id: 'judge',
        timestamp: '2026-09-14T12:05:00Z',
        verdict: {
          id: 'verdict-1',
          scope: 'goal',
          round: 1,
          met: true,
          per_criterion: [{ criterion_id: 'crit-1', met: true, reason: 'confirmed' }],
          model: 'z-ai/glm-5.3',
          judged_at: '2026-09-14T12:05:00Z',
          judge_agent_id: 'judge',
        },
      },
      // The judged turn's own assistant entry, carrying turn_id —
      // mergeJudgeVerdictHistory anchors the verdict's thread position to
      // this turn (chat.ts); rawToMessage must forward it.
      {
        id: 'assistant-turn-entry',
        type: 'message',
        role: 'assistant',
        agent_id: 'mia',
        content: 'OK-DONE-VERDICT-TEST',
        timestamp: '2026-09-14T12:04:00Z',
        status: 'ok',
        turn_id: 'mia-turn-2',
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-verdict', wirePayload, 2)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-verdict')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(2)
    expect(messages[0].id).toBe('goal-sid-verdict-judge-1')
    expect(messages[0].type).toBe('judge_verdict')
    expect(messages[0].verdict?.id).toBe('verdict-1')
    expect(messages[0].verdict?.scope).toBe('goal')
    expect(messages[0].verdict?.met).toBe(true)
    expect(messages[0].verdict?.per_criterion).toHaveLength(1)
    expect(messages[1].turnId).toBe('mia-turn-2')
  })


})

// ── fetchSessionMessages renames parameters → params ─────────────────────────────
//
// Regression test for Problem 1. The wire ToolCall schema emits `parameters`
// (matching Go json tag `json:"parameters,omitempty"`). The SPA ToolCall type
// uses `params`. Without the rawToToolCall() transform, params was `undefined`
// and ToolCallBadge rendered `JSON.stringify(undefined, null, 2)` = "undefined".
//
// This test stubs fetch to return the wire shape (with `parameters`) and
// asserts that the returned page.entries[].tool_calls[].params is correctly populated.

describe('fetchSessionMessages: wire parameters → SPA params transform', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal() {
    Reflect.deleteProperty(document, 'cookie')
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    // fetchSessionMessages is a GET, no CSRF needed — but setting the cookie
    // avoids CSRF guard triggering on any incidental state-changing helpers.
    stubCookieLocal('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal()
    vi.resetModules()
  })

  it('degrades an unknown entry type to a placeholder instead of rejecting the whole list (Issue 3 / library-uat HIGH)', async () => {
    // Updated for the per-item resilience fix. Previously this asserted
    // fetchSessionMessages REJECTED the whole array on a single out-of-enum
    // `type` value — that all-or-nothing behaviour is exactly the bug the
    // live UAT reproduced (one malformed attachment `type` made an entire
    // session permanently unloadable, Retry included). The contract-drift
    // signal is preserved (still counted via getApiSchemaErrorCount, so a
    // future EntryType added without a schema update is NOT silently
    // invisible) but a single bad row must no longer take the whole
    // transcript down — see the block comment above placeholderMessage()
    // in src/lib/api.ts for the full rationale.
    const wirePayload = [
      {
        id: 'msg_x',
        type: 'wholly_unknown_entry_kind',
        agent_id: 'jim',
        timestamp: '2026-05-21T10:00:00Z',
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-unknown', wirePayload, 1)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, getApiSchemaErrorCount: count } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-unknown')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(1)
    expect(messages[0].role).toBe('system')
    expect(messages[0].content).toBe('This message could not be displayed.')
    expect(count()).toBe(1)
  })

  it('keeps the valid messages and replaces only the malformed one when a list is mixed (Issue 3 / library-uat HIGH)', async () => {
    // Reproduces the exact live-server shape from the UAT finding: an
    // attachment `type` of "document", which is outside the Attachment
    // enum (image|audio|video|file). Sits between two otherwise-valid
    // messages so we can assert order, count, and content are all correct.
    const wirePayload = [
      {
        id: 'msg-before',
        agent_id: 'agent-1',
        role: 'user',
        content: 'here is a file',
        timestamp: '2026-07-20T10:00:00Z',
        status: 'ok',
      },
      {
        id: 'msg-bad-attachment',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'got it',
        timestamp: '2026-07-20T10:00:01Z',
        status: 'ok',
        attachments: [
          { type: 'document', path: '/work/spec.docx', size: 1024, mime_type: 'application/msword' },
        ],
      },
      {
        id: 'msg-after',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'anything else?',
        timestamp: '2026-07-20T10:00:02Z',
        status: 'ok',
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-mixed', wirePayload, 3)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, getApiSchemaErrorCount: count } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-mixed')
    expectHistoryPage(decoded)
    const messages = decoded.entries

    // All three positions are present — the bad one is a visible placeholder,
    // not a silently vanished row (see the judgment-call comment in api.ts).
    expect(messages).toHaveLength(3)
    expect(messages[0].id).toBe('msg-before')
    expect(messages[0].content).toBe('here is a file')
    expect(messages[1].id).toBe('msg-bad-attachment')
    expect(messages[1].role).toBe('system')
    expect(messages[1].content).toBe('This message could not be displayed.')
    expect(messages[2].id).toBe('msg-after')
    expect(messages[2].content).toBe('anything else?')

    // Counted through the existing _recordApiSchemaError path (no parallel counter).
    expect(count()).toBe(1)
  })

  it('leaves a fully valid list completely unaffected — no drops counted', async () => {
    const wirePayload = [
      {
        id: 'msg-a',
        agent_id: 'agent-1',
        role: 'user',
        content: 'hello',
        timestamp: '2026-07-20T10:00:00Z',
        status: 'ok',
      },
      {
        id: 'msg-b',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'hi there',
        timestamp: '2026-07-20T10:00:01Z',
        status: 'ok',
        attachments: [
          { type: 'image', path: '/work/photo.png', size: 2048, mime_type: 'image/png' },
        ],
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireHistoryPage('sid-all-valid', wirePayload, 2)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, getApiSchemaErrorCount: count } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sid-all-valid')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(2)
    expect(messages.map((m) => m.id)).toEqual(['msg-a', 'msg-b'])
    expect(count()).toBe(0)
  })

  it('still throws ApiSchemaError when the body is not a history envelope (genuine contract break, not a list-item defect)', async () => {
    // Scoping guard: per-item recovery is for ENTRIES within a valid page.
    // A response without a history envelope (e.g. an error object)
    // is a real contract break and must still fail loudly.
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'not a list' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, ApiSchemaError } = await import('./api')
    await expect(fetchSessionMessages('sid-not-array')).rejects.toBeInstanceOf(ApiSchemaError)
  })
})
