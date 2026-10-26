// R29 alias-negative oracles: chat-history-paging-spec D2/D4/D6.
// "Canonical source IDs and source positions remain distinguishable from
// coalesced display-bubble aliases." Preserve turn/agent association, live
// values, display aliases and pending input; never choose an ambiguous owner.
// Real token resolver, store, REST adapter and page merger; only fetch/Date.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { SID, STAMP, entry, expectValidPage, page, pos } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

const DISPLAY = 'r29-displayed-reply'
const OTHER_DISPLAY = 'r29-other-displayed-reply'
const SAVED = 'r29-saved-alias'
const TURN = 'r29-displayed-turn'
const PREFIX = 'What this displayed reply actually said. '
const CONTINUATION = 'Its genuinely coalesced continuation.'
const DISPLAY_BODY = PREFIX + CONTINUATION
const OTHER_BODY = 'A separate displayed reply must not become the saved source.'
const SAVED_BODY = 'The selected recorded source has its own identity.'
let transport: ReturnType<typeof historyTransport> | undefined
function bucket() {
  const b = useChatStore.getState().sessionsById[SID]
  if (!b) throw new Error('R29 instrument: missing addressed bucket')
  return b
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(STAMP)
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
  useChatStore.getState().releaseSessionHistory(SID)
})
afterEach(() => {
  transport?.connection.disconnect(); transport = undefined
  useChatStore.getState().releaseSessionHistory(SID)
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('r29 an alias claim is not sufficient saved-source ownership', () => {
  it.each(['two displayed replies', 'different turn', 'different agent'] as const)('r29 alias guard rejects %s without rekeying or stealing a source position', async boundary => {
    transport = historyTransport()
    transport.deliver(
      { type: 'token', session_id: SID, turn_id: TURN, agent_id: 'mia', message_id: DISPLAY, content: PREFIX, seq: 1 },
      { type: 'token', session_id: SID, turn_id: TURN, agent_id: 'mia', message_id: SAVED, content: CONTINUATION, seq: 2 },
    )
    expect(bucket().messagesById[DISPLAY], 'instrument: the real resolver created this actual alias-bearing live display row').toMatchObject({
      id: DISPLAY, role: 'assistant', content: DISPLAY_BODY, turnId: TURN, agentId: 'mia', isStreaming: true,
    })
    expect(bucket().messagesById[DISPLAY].mergedReplayIds).toStrictEqual([SAVED])
    expect(bucket().messagesById[SAVED], 'the recorded alias is not yet a separate displayed source').toBeUndefined()
    expect(bucket().messagesById[DISPLAY].historyPosition).toBeUndefined()

    if (boundary === 'two displayed replies') {
      // Deliberate ambiguous-input negative, not a claimed valid gateway
      // emission: duplicate a genuinely resolved alias claim through the public
      // appendMessage boundary. Never forge a successful owner/merge flag.
      useChatStore.getState().appendMessage({ ...bucket().messagesById[DISPLAY], id: OTHER_DISPLAY, content: OTHER_BODY })
      expect(Object.values(bucket().messagesById).filter(row => row.mergedReplayIds?.includes(SAVED)).map(row => row.id)).toStrictEqual([DISPLAY, OTHER_DISPLAY])
    }
    const sourceTurn = boundary === 'different turn' ? 'r29-different-saved-turn' : TURN
    const sourceAgent = boundary === 'different agent' ? 'jim' : 'mia'
    const saved = entry(1, { id: SAVED, role: 'assistant', content: SAVED_BODY, turn_id: sourceTurn, agent_id: sourceAgent })
    const selected = page([saved])
    expectValidPage(selected, [pos(1)], 1)
    vi.stubGlobal('fetch', async () => new Response(JSON.stringify(selected), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await useChatStore.getState().loadLatestHistory(SID)

    expect(bucket().history.historyLoaded, 'the negative ownership match is not a rejected or absent page').toBe(true)
    expect(bucket().messagesById[SAVED], 'D2/D4: an ambiguous or different-owner alias cannot steal the canonical saved source').toMatchObject({
      id: SAVED, role: 'assistant', content: SAVED_BODY, turnId: sourceTurn, agentId: sourceAgent, historyPosition: pos(1),
    })
    expect(bucket().history.readerPositions).toStrictEqual([pos(1)])
    expect(bucket().historySourceAliases?.[pos(1)], 'the source position belongs to its own saved ID, not an invalid display claimant').toBe(SAVED)
    expect(bucket().messagesById[DISPLAY], 'D4/D6: the distinct live display retains its actual body/producer/turn and real alias').toMatchObject({
      id: DISPLAY, content: DISPLAY_BODY, turnId: TURN, agentId: 'mia', isStreaming: true, mergedReplayIds: [SAVED],
    })
    expect(bucket().messagesById[DISPLAY].historyPosition, 'the invalid claimant must not acquire the saved source position').toBeUndefined()
    if (boundary === 'two displayed replies') {
      expect(bucket().messageOrder).toStrictEqual([SAVED, DISPLAY, OTHER_DISPLAY])
      expect(bucket().messagesById[OTHER_DISPLAY]).toMatchObject({ id: OTHER_DISPLAY, content: OTHER_BODY, turnId: TURN, agentId: 'mia', isStreaming: true, mergedReplayIds: [SAVED] })
      expect(bucket().messagesById[OTHER_DISPLAY].historyPosition).toBeUndefined()
    } else {
      expect(bucket().messageOrder).toStrictEqual([SAVED, DISPLAY])
    }
  })
})
