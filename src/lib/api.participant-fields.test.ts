// U8 participant fields on the REST cold-load path (session-core F15/F17,
// architect answer D1). After a reload the persisted entries reach the SPA via
// GET /sessions/{id}/messages; the rawToMessage adapter inside
// fetchSessionMessages (rawToMessage is not exported — the public surface is
// exercised, same harness as api.input-disposition.test.ts) must forward:
//   - participant            → UserMessage.participant
//   - reply_to_message_id    → AssistantMessage.replyToMessageId
//   - reply_to_participant   → AssistantMessage.replyToParticipant
// Only the HTTP edge is stubbed (vi.stubGlobal('fetch')); the generated-schema
// layer and the adapter run for real, so a silent strip at either layer fails
// these tests too.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { wireHistoryPage } from '../../tests/chat-history-paging/legacy-wire'
import { expectHistoryPage } from '../../tests/chat-history-paging/legacy-fixtures'

function makeOkResponse(sessionId: string, body: unknown[]): Response {
  return new Response(JSON.stringify(wireHistoryPage(sessionId, body, body.length)), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('fetchSessionMessages forwards the participant fields', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('a user row with a human participant carries participant on the mapped message', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse('sess-participant', [
      {
        id: 'msg-tg-1',
        role: 'user',
        content: 'hello from the channel',
        timestamp: '2026-10-11T10:00:00Z',
        agent_id: 'jim',
        participant: { kind: 'human', display_name: 'Alice', source: 'telegram' },
      },
    ]))

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sess-participant')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(1)
    const msg = messages[0] as { role?: string; participant?: unknown }
    expect(msg.role).toBe('user')
    expect(msg.participant).toEqual({ kind: 'human', display_name: 'Alice', source: 'telegram' })
  })

  it('a user row WITHOUT a participant keeps today’s shape (field absent — F17 own message)', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse('sess-participant', [
      {
        id: 'msg-own-1',
        role: 'user',
        content: 'my own message',
        timestamp: '2026-10-11T10:01:00Z',
        agent_id: 'jim',
      },
    ]))

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sess-participant')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(1)
    const msg = messages[0] as { participant?: unknown }
    expect(msg.participant).toBeUndefined()
  })

  it('an assistant reply row carries replyToMessageId + replyToParticipant', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse('sess-participant', [
      {
        id: 'msg-reply-1',
        role: 'assistant',
        content: 'the answer',
        timestamp: '2026-10-11T10:02:00Z',
        agent_id: 'agent-ava',
        reply_to_message_id: 'req-1',
        reply_to_participant: { kind: 'human', display_name: 'Alice', source: 'slack' },
      },
    ]))

    const { fetchSessionMessages } = await import('./api')
    const decoded: unknown = await fetchSessionMessages('sess-participant')
    expectHistoryPage(decoded)
    const messages = decoded.entries
    expect(messages).toHaveLength(1)
    const msg = messages[0] as { replyToMessageId?: string; replyToParticipant?: unknown }
    expect(msg.replyToMessageId).toBe('req-1')
    expect(msg.replyToParticipant).toEqual({ kind: 'human', display_name: 'Alice', source: 'slack' })
  })
})
