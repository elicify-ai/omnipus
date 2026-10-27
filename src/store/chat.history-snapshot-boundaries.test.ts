// R29 C10 boundary oracles: chat-history-paging-spec D7.
// "Live cursor can commit W because live catch-up completed; this is
// independent of reader revalidation." A rejected page retains the prior
// cursor/view, while the separately completed live controls become usable.
// Real buffer, store/history owner and REST adapter; only fetch/Date replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ServerFrame, SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { AttachReplayBuffer, isProgressiveAttachFrame } from '@/lib/attach-replay'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { SID, STAMP, entry, expectValidFrame, expectValidPage, page, pos, replay, snapshot } from '../../tests/chat-history-paging/fixtures'

const available: SessionStateFrame = {
  type: 'session_state', session_id: SID, user_id: 'r29-reader', pending_approvals: [],
  emitted_at: STAMP, history_state: { status: 'available' },
}
function bucket() {
  const b = useChatStore.getState().sessionsById[SID]
  if (!b) throw new Error('R29 instrument: missing addressed bucket')
  return b
}
function feed(frames: ServerFrame[], buffer?: AttachReplayBuffer) {
  frames.forEach(expectValidFrame)
  for (const frame of frames) {
    for (const released of buffer ? buffer.accept(frame) : [frame]) useChatStore.getState().handleFrame(released)
  }
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(STAMP)
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
  useChatStore.getState().releaseSessionHistory(SID)
})
afterEach(() => {
  useChatStore.getState().releaseSessionHistory(SID)
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('r29 snapshot page outcome is separate from live completion', () => {
  it('r29 skipped snapshot: a newer loaded reader stays while successful catch-up still commits its cursor', async () => {
    // Tiny REAL buffer allocation forces progressive delivery, not a forged
    // stage/success flag or a huge text fixture. The page is still provisional.
    const buffer = new AttachReplayBuffer({ maxFrames: 1 })
    buffer.begin(SID)
    const provisional = entry(1, { id: 'r29-provisional', role: 'assistant', content: 'Provisional page' })
    const selected = page([provisional])
    expectValidPage(selected, [pos(1)], 1)
    const announced: ServerFrame = { ...snapshot(selected), seq: 42, boot_id: 'r29-boot' }
    feed([announced, available, replay(provisional), { type: 'done', session_id: SID }], buffer)
    expect(isProgressiveAttachFrame(announced), 'instrument: actual bounded-buffer delivery marked the snapshot progressive').toBe(true)
    expect(bucket().messageOrder, 'instrument: the provisional source was really reconstructed before the newer read').toStrictEqual(['r29-provisional'])
    expect(bucket().history.historyLoaded, 'no COMPLETE has certified this provisional page').toBe(false)
    expect(bucket().cursor).toBeNull()

    const newer = entry(2, { id: 'r29-newer-reader', role: 'assistant', content: 'Newer loaded view wins' })
    const replacement = page([newer])
    expectValidPage(replacement, [pos(2)], 1)
    vi.stubGlobal('fetch', async () => new Response(JSON.stringify(replacement), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await useChatStore.getState().loadLatestHistory(SID)
    expect(bucket().history.historyLoaded, 'instrument: the newer real REST read committed before the old attach completed').toBe(true)
    expect(bucket().messageOrder).toStrictEqual(['r29-newer-reader'])
    expect(bucket().history.readerPositions).toStrictEqual([pos(2)])
    expect(bucket().cursor, 'a page read cannot mint the live cursor').toBeNull()

    feed([{ type: 'catch_up_complete', session_id: SID, seq: 42, boot_id: 'r29-boot', mode: 'snapshot' }], buffer)
    expect(bucket().cursor, 'D7: skipping an obsolete page does not withhold a successful live catch-up cut').toStrictEqual({ bootId: 'r29-boot', seq: 42 })
    expect(bucket().messageOrder, 'the skipped snapshot cannot overwrite the newer loaded reader').toStrictEqual(['r29-newer-reader'])
    expect(bucket().messagesById['r29-newer-reader']).toMatchObject({ id: 'r29-newer-reader', content: 'Newer loaded view wins', historyPosition: pos(2) })
    expect(bucket().messagesById['r29-provisional']).toBeUndefined()
    expect(bucket().history.readerPositions).toStrictEqual([pos(2)])
    expect(bucket().history.historyLoaded).toBe(true)
    expect(bucket().awaitingCatchUp).toBe(false)
    expect(bucket().isReplaying).toBe(false)
    expect(bucket().history.liveControlsReady).toBe(true)
  })

  it('r29 rejected snapshot: completed current controls unlock despite the rejected historical replacement', () => {
    const prior = entry(1, { id: 'r29-old-reader', role: 'assistant', content: 'Previous saved answer' })
    const initial = page([prior])
    expectValidPage(initial, [pos(1)], 1)
    const current: SessionStateFrame = { ...available, active_turn: { turn_id: 'r29-current-turn', agent_id: 'mia', started_at: STAMP } }
    feed([
      { ...snapshot(initial), seq: 7, boot_id: 'r29-prior-boot' }, current, replay(prior),
      { type: 'done', session_id: SID },
      { type: 'catch_up_complete', session_id: SID, seq: 7, boot_id: 'r29-prior-boot', mode: 'snapshot' },
    ])
    useChatStore.getState().appendMessage({ id: 'r29-pending', role: 'user', content: 'Pending input survives', timestamp: STAMP, deliveryStatus: 'failed' })
    const before = structuredClone(bucket())
    expect(before.history.historyLoaded, 'instrument: an actual prior page was committed').toBe(true)
    expect(before.cursor).toStrictEqual({ bootId: 'r29-prior-boot', seq: 7 })

    const incoming = entry(2, { id: 'r29-invalid-replacement', role: 'assistant', content: 'Never publish' })
    // Structurally generated-valid, deliberately semantically invalid: one
    // narration source cannot satisfy the advertised two drawn rows.
    const contradictory = page([incoming], { rows: 2 })
    feed([{ ...snapshot(contradictory), seq: 42, boot_id: 'r29-new-boot' }, current, replay(incoming), { type: 'done', session_id: SID }])
    expect(bucket().awaitingCatchUp, 'instrument: the incomplete replacement really held the attach guard').toBe(true)
    feed([{ type: 'catch_up_complete', session_id: SID, seq: 42, boot_id: 'r29-new-boot', mode: 'snapshot' }])

    expect(bucket().awaitingCatchUp, 'D7: historical rejection cannot strand the completed live controls behind catch-up').toBe(false)
    expect(bucket().isReplaying, 'D7: completed control delivery leaves no replay lock').toBe(false)
    expect(bucket().history.liveControlsReady, 'D7: current controls are usable independently of the rejected page').toBe(true)
    expect(bucket().history.requests.latest.status).toBe('error')
    expect(bucket().history.historyLoaded).toBe(false)
    expect(bucket().cursor, 'rejection still retains the actual old cut').toStrictEqual({ bootId: 'r29-prior-boot', seq: 7 })
    expect(bucket().messageOrder).toStrictEqual(before.messageOrder)
    expect(bucket().messagesById['r29-old-reader']).toStrictEqual(before.messagesById['r29-old-reader'])
    expect(bucket().messagesById['r29-pending']).toStrictEqual(before.messagesById['r29-pending'])
    expect(bucket().messagesById['r29-invalid-replacement']).toBeUndefined()
    expect(bucket().activeTurnId, 'unlocking controls is not falsely ending current work').toBe('r29-current-turn')
    expect(bucket().activeTurnAgentId).toBe('mia')
  })
})
