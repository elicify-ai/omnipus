// has-earlier.test.ts — the chat-state half of has_earlier_messages
// (founder ruling 2026-10-11): every carrier lands in ONE bucket field.
//
// Covered here: the SessionSnapshotFrame carrier (the attach replay's
// session_snapshot frame — the generated field is optional; ABSENT means the
// view is the whole transcript, i.e. false) and the recordSessionHasEarlier
// writer the other carriers' consumers call. The REST carriers
// (SessionDetail field, messages header) are covered at the adapter edge in
// src/lib/api/sessions.has-earlier.test.ts; the render half (the quiet line)
// in src/components/chat/ChatScreen.earlier-note.test.tsx.

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { makeBucketMessages } from './messages'
import { recordSessionHasEarlier } from './has-earlier'
import { useConnectionStore } from '@/store/connection'
import type { ChatMessage } from './types'

const SID = 'sess-has-earlier'

function seedBucket(messages: ChatMessage[] = []): void {
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      ...s.sessionsById,
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...makeBucketMessages(messages),
        isStreaming: false,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
      },
    },
    messages,
    messagesById: {},
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
}

function bucket(): Record<string, unknown> {
  const b = useChatStore.getState().sessionsById[SID]
  expect(b, 'bucket exists').toBeTruthy()
  return b as Record<string, unknown>
}

describe('has_earlier_messages → chat state', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      connection: { send: () => true, close: () => {} } as never,
      isConnected: true,
    } as never)
    useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  })

  it('a session_snapshot frame with has_earlier_messages: true records true', () => {
    seedBucket()
    useChatStore.getState().handleFrame({
      type: 'session_snapshot', session_id: SID, seq: 1, has_earlier_messages: true,
    } as never)
    expect(bucket().hasEarlierMessages).toBe(true)
  })

  it('a session_snapshot frame WITHOUT the field records false (the view is the whole transcript)', () => {
    seedBucket()
    useChatStore.getState().handleFrame({
      type: 'session_snapshot', session_id: SID, seq: 2,
    } as never)
    expect(bucket().hasEarlierMessages).toBe(false)
  })

  it('recordSessionHasEarlier writes the bucket flag; null and the __pending sentinel are no-ops', () => {
    seedBucket()
    recordSessionHasEarlier(SID, true)
    expect(bucket().hasEarlierMessages).toBe(true)
    recordSessionHasEarlier(SID, false)
    expect(bucket().hasEarlierMessages).toBe(false)
    recordSessionHasEarlier(null, true)
    recordSessionHasEarlier('__pending', true)
    expect(bucket().hasEarlierMessages).toBe(false)
  })
})
