// D15 migration: the same three chat-state scenarios now use complete,
// generated, semantically valid bounded snapshots. Missing metadata is NOT false.
import { beforeEach, afterEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { recordSessionHasEarlier } from './has-earlier'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { entry, expectPageAccounting, expectValidFrame, page, pos, replay, snapshot } from '../../../tests/chat-history-paging/fixtures'

const SID = 'sess-has-earlier'
function bucket() {
  const result = useChatStore.getState().sessionsById[SID]
  if (!result) throw new Error('Fixture instrument: expected real session bucket')
  return result
}
function seedBucket() {
  useSessionStore.getState().setActiveSession(SID, 'mia')
  useChatStore.getState().setMessages([])
}
function applySnapshot(earlier: boolean) {
  const sources = earlier ? [entry(1, { type: 'system', role: 'system', content: 'Conversation context cleared' })] : []
  const history = page(sources, { sessionId: SID, earlier, info: earlier ? {
    stopped_at_clear: true, stop_reason: 'clear', latest_clear: { entry_id: 'history-1', history_position: pos(1) },
  } : {} })
  expectPageAccounting(history, earlier ? [pos(1)] : [], sources.length)
  const frames = [snapshot(history), ...sources.map((source) => replay(source, SID)), {
    type: 'catch_up_complete' as const, session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'snapshot' as const,
  }]
  frames.forEach((frame) => { expectValidFrame(frame); useChatStore.getState().handleFrame(frame) })
}

describe('canonical has_earlier → chat state', () => {
  beforeEach(() => {
    queryClient.clear()
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
  })
  afterEach(() => queryClient.clear())

  it('a completed snapshot with canonical has_earlier true records true with its page', () => {
    seedBucket()
    applySnapshot(true)
    expect(bucket().hasEarlierMessages).toBe(true)
    expect(bucket().messageOrder).toStrictEqual(['history-1'])
  })

  it('a completed terminal snapshot explicitly records false with its empty page', () => {
    seedBucket()
    applySnapshot(false)
    expect(bucket().hasEarlierMessages).toBe(false)
    expect(bucket().messageOrder).toStrictEqual([])
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
