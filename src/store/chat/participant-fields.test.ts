// U8 participant fields on the store path (session-core F15/F17, architect
// answer D1/D4). The three WS carriers must stamp the display fields onto the
// ChatMessage so live and replay render identically for the same message id:
//   - replay_message  → participant (user entries), reply_to_* (assistant)
//   - user_message    → participant (server-written left-side entries)
//   - token           → reply_to_* (the live reply mirror, one token+done pair)
//
// The real frame handler runs (useChatStore.handleFrame); only the WS
// connection is stubbed by construction — same harness as
// discarded-input-fr-024.test.ts. Until the fields are stamped these fail,
// which is exactly the final-gate finding S4 H1 ("no non-test code under src
// reads participant / reply_to_participant").

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { ServerFrame } from '@/lib/ws'

const SID = 'sess-participant-fields'

function bucketMessages(sid: string = SID) {
  const b = useChatStore.getState().sessionsById[sid]
  if (!b) return []
  return b.messageOrder.map((id) => b.messagesById[id])
}

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({ connection: null } as never)
})

describe('replay_message stamps the participant fields', () => {
  it('a replayed user entry with a human participant carries it on the bubble', () => {
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: SID,
      id: 'hist-tg-1',
      role: 'user',
      content: 'hello from the channel',
      timestamp: '2026-10-11T10:00:00Z',
      participant: { kind: 'human', display_name: 'Alice', source: 'telegram' },
    } as unknown as ServerFrame)

    const [msg] = bucketMessages()
    expect(msg?.participant).toEqual({ kind: 'human', display_name: 'Alice', source: 'telegram' })
  })

  it('a replayed user entry WITHOUT a participant keeps today’s shape (field absent)', () => {
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: SID,
      id: 'hist-own-1',
      role: 'user',
      content: 'my own message',
      timestamp: '2026-10-11T10:01:00Z',
    } as unknown as ServerFrame)

    const [msg] = bucketMessages()
    expect(msg?.participant).toBeUndefined()
  })

  it('a replayed connector-reply assistant entry carries reply_to_message_id + reply_to_participant', () => {
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: SID,
      id: 'hist-reply-1',
      role: 'assistant',
      content: 'the answer',
      timestamp: '2026-10-11T10:02:00Z',
      agent_id: 'agent-ava',
      reply_to_message_id: 'req-1',
      reply_to_participant: { kind: 'human', display_name: 'Alice', source: 'telegram' },
    } as unknown as ServerFrame)

    const [msg] = bucketMessages()
    expect(msg?.replyToMessageId).toBe('req-1')
    expect(msg?.replyToParticipant).toEqual({ kind: 'human', display_name: 'Alice', source: 'telegram' })
  })
})

describe('user_message stamps the participant (server-written left-side entries)', () => {
  it('a live user_message frame with a participant carries it on the minted bubble', () => {
    useChatStore.getState().handleFrame({
      type: 'user_message',
      session_id: SID,
      id: 'live-tg-1',
      content: 'bound-connector input',
      timestamp: '2026-10-11T10:03:00Z',
      participant: { kind: 'human', display_name: 'Alice', source: 'slack' },
    } as unknown as ServerFrame)

    const [msg] = bucketMessages()
    expect(msg?.role).toBe('user')
    expect(msg?.participant).toEqual({ kind: 'human', display_name: 'Alice', source: 'slack' })
  })
})

describe('token stamps the live reply pair (guest-reply token+done path)', () => {
  it('a token frame carrying reply_to_* stamps them on the bubble it mints', () => {
    useChatStore.getState().handleFrame({
      type: 'token',
      session_id: SID,
      content: 'the live answer',
      message_id: 'reply-live-1',
      agent_id: 'agent-ava',
      reply_to_message_id: 'req-1',
      reply_to_participant: { kind: 'human', display_name: 'Alice', source: 'telegram' },
    } as unknown as ServerFrame)

    const [msg] = bucketMessages()
    expect(msg?.replyToMessageId).toBe('req-1')
    expect(msg?.replyToParticipant).toEqual({ kind: 'human', display_name: 'Alice', source: 'telegram' })
  })

  it('a plain turn token stamps neither field', () => {
    useChatStore.getState().handleFrame({
      type: 'token',
      session_id: SID,
      content: 'ordinary narration',
      message_id: 'turn-live-1',
      agent_id: 'agent-ava',
    } as unknown as ServerFrame)

    const [msg] = bucketMessages()
    expect(msg?.replyToMessageId).toBeUndefined()
    expect(msg?.replyToParticipant).toBeUndefined()
  })
})
