// FR-024 / U7 (docs/internal/specs/session-core-spec.md, BDD-07.3): "Show
// discarded/not delivered … preserve archived bytes". A user input that Stop
// discarded before it was delivered into the agent's model input is persisted
// with a read-only `input_disposition` marker on the archived entry
// (contracts/components/schemas/Message.yaml). After a reload, the REST
// history adapter (rawToMessage inside fetchSessionMessages — rawToMessage is
// not exported, so the public surface is exercised) must map it onto the SAME
// quiet `deliveryStatus: 'discarded'` the live `message_status` frame already
// drives (ConnectionStatus.tsx's "Not delivered" state) — never as an error —
// and the archived message text stays.
//
// Only the HTTP edge is stubbed (vi.stubGlobal('fetch')); the zod schema
// layer, the envelope handling and rawToMessage all run for real, so a
// generated-schema silent-strip of `input_disposition` fails these tests too.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

function makeOkResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('FR-024 — fetchSessionMessages maps input_disposition onto the SPA deliveryStatus', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('a history row discarded by Stop carries deliveryStatus "discarded" and keeps its text', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse([
      {
        id: 'msg-discarded-1',
        role: 'user',
        content: 'summarize the quarter',
        timestamp: '2026-10-11T10:00:00Z',
        agent_id: 'jim',
        input_disposition: {
          message_id: 'msg-discarded-1',
          state: 'discarded',
          reason: 'stopped_before_delivery',
        },
      },
    ]))

    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sess-discarded')

    expect(messages).toHaveLength(1)
    const msg = messages[0] as { role?: string; content?: string; deliveryStatus?: string }
    expect(msg.role).toBe('user')
    // The mapping target: the same quiet state the live message_status frame
    // stamps — renders as "Not delivered", never as an error.
    expect(msg.deliveryStatus).toBe('discarded')
    // BDD-07.3: the archived bytes are preserved — the text stays on the bubble.
    expect(msg.content).toBe('summarize the quarter')
  })

  it('a row without input_disposition keeps today’s rendering — no deliveryStatus stamped', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse([
      {
        id: 'msg-plain-1',
        role: 'user',
        content: 'an ordinary delivered message',
        timestamp: '2026-10-11T10:01:00Z',
        agent_id: 'jim',
      },
    ]))

    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sess-plain')

    expect(messages).toHaveLength(1)
    const msg = messages[0] as { content?: string; deliveryStatus?: string }
    expect(msg.deliveryStatus).toBeUndefined()
    expect(msg.content).toBe('an ordinary delivered message')
  })
})
