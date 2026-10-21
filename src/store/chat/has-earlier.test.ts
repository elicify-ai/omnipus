// has-earlier.test.ts — earlier-history carriers land in ONE bucket field
// (founder ruling 2026-10-11; paged snapshot contract).
//
// Covered here: the SessionSnapshotFrame carrier's required has_earlier
// marker and paging metadata, the store's defensive missing-marker fallback
// for deliberately malformed input, and the recordSessionHasEarlier writer.
// The REST carriers
// (SessionDetail field, messages header) are covered at the adapter edge in
// src/lib/api/sessions.has-earlier.test.ts; the render half (the quiet line)
// in src/components/chat/ChatScreen.earlier-note.test.tsx.

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { makeBucketMessages } from './messages'
import { recordSessionHasEarlier } from './has-earlier'
import { useConnectionStore } from '@/store/connection'
import type { ChatMessage, SessionChatState } from './types'
import type { SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'
import { SessionSnapshotFrame as snapshotSchema } from '@/lib/api/generated/ws-schemas'
import { makeSnapshotHistory } from '@/test/factories'

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

function bucket(): SessionChatState {
  const b = useChatStore.getState().sessionsById[SID]
  expect(b, 'bucket exists').toBeTruthy()
  return b!
}

describe('earlier-history carriers → chat state', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      connection: { send: () => true, close: () => {} } as never,
      isConnected: true,
    } as never)
    useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  })

  it('a session_snapshot frame with has_earlier: true records true', () => {
    seedBucket()
    const history = makeSnapshotHistory(2)
    const firstPosition = history.page_info.first_position
    if (!firstPosition) throw new Error('Nonempty snapshot fixture needs a first position')
    // A full latest-page target can retain earlier groups. The continuation
    // must equal its first position (SessionSnapshotFrame's shared contract).
    const snapshot: SessionSnapshotFrame = {
      ...history,
      type: 'session_snapshot', session_id: SID, seq: 1,
      has_earlier: true,
      next_before: firstPosition,
      page_info: {
        ...history.page_info,
        row_count: history.page_info.limit,
        source_group_count: history.page_info.limit,
        projected_bytes: 10_240,
        stop_reason: 'row_target',
      },
    }
    useChatStore.getState().handleFrame(snapshotSchema.parse(snapshot))
    expect(bucket().hasEarlierMessages).toBe(true)
  })

  it('a malformed snapshot missing required has_earlier defensively records false', () => {
    seedBucket()
    const snapshot: SessionSnapshotFrame = {
      ...makeSnapshotHistory(0),
      type: 'session_snapshot', session_id: SID, seq: 2,
    }
    const missingMarker: Partial<SessionSnapshotFrame> = snapshotSchema.parse(snapshot)
    delete missingMarker.has_earlier
    expect(snapshotSchema.safeParse(missingMarker).success).toBe(false)
    // Deliberate invalid-input coverage of the store, not a valid gateway frame.
    useChatStore.getState().handleFrame(missingMarker as never)
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
