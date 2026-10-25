// #1266 restart cut: a reply this tab watched streaming, that the restarted
// gateway never saved, must not survive the snapshot commit as a live row when
// the server's current state says its turn is not running. Before paging, the
// snapshot rebuilt the transcript and dropped it; the paged commit retained any
// streaming row ("live wins"), so the dead reply stayed as the last row, hid the
// unanswered question and showed "couldn't be finished" instead of Interrupted.
// A turn the server still reports active keeps its streaming row untouched, and
// the user's own pending send always survives.
// Real transport, real store and replay owner; only socket and clock replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ServerFrame, SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { SID, STAMP, entry, expectValidPage, page, pos, replay, snapshot } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

const question = entry(1, { id: 'saved-question', role: 'user', content: 'Write the long report' })

function state(activeTurn?: string): SessionStateFrame {
  return {
    type: 'session_state', session_id: SID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP,
    history_state: { status: 'available' },
    ...(activeTurn ? { active_turn: { turn_id: activeTurn, agent_id: 'mia', started_at: STAMP } } : {}),
  }
}

let transport: ReturnType<typeof historyTransport> | undefined
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(STAMP)
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
})
afterEach(() => {
  transport?.connection.disconnect(); transport = undefined
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

function bucket() {
  const b = useChatStore.getState().sessionsById[SID]
  if (!b) throw new Error('no chat bucket for the test session')
  return b
}
function rows() {
  const b = bucket()
  return b.messageOrder.map((id) => ({ id, role: b.messagesById[id].role, streaming: !!b.messagesById[id].isStreaming }))
}

/** The tab watches a reply stream live, then reattaches to a new boot whose
 * saved page holds only the question (the partial reply was never saved). */
function witnessThenReattach(reason: 'boot_mismatch' | 'unknown_position', activeTurn?: string, extraLive?: () => void) {
  // The live stream reaches the store before the socket drops (the transport's
  // own render-once buffer only covers frames after a new attach).
  const live: ServerFrame = { type: 'token', session_id: SID, turn_id: 'turn-cut', message_id: 'live-reply', content: 'Partial text', seq: 5 }
  useChatStore.getState().handleFrame(live)
  extraLive?.()
  expect(rows(), 'the tab witnessed the reply streaming').toContainEqual({ id: 'live-reply', role: 'assistant', streaming: true })
  transport = historyTransport()
  transport.attach()
  const latest = page([question], { rows: 1 })
  expectValidPage(latest, [pos(1)], 1)
  const frames: ServerFrame[] = [
    { ...snapshot(latest, reason), seq: 4, boot_id: 'boot-2' }, state(activeTurn), replay(question),
    { type: 'done', session_id: SID },
    { type: 'catch_up_complete', session_id: SID, seq: 4, boot_id: 'boot-2', mode: 'snapshot' },
  ]
  transport.deliver(...frames)
  expect(bucket().history.historyLoaded, 'the snapshot page committed').toBe(true)
}

describe('snapshot commit drops a witnessed dead stream, never a running one', () => {
  it('restart (boot_mismatch), no active turn: the unsaved streaming reply is gone and the question is unanswered', () => {
    witnessThenReattach('boot_mismatch')
    expect(rows()).toStrictEqual([{ id: 'saved-question', role: 'user', streaming: false }])
    expect(bucket().unansweredLastUserMessageId).toBe('saved-question')
    expect(bucket().isStreaming, 'nothing streams once the dead reply is gone').toBe(false)
    expect(Object.values(bucket().messagesById).some((row) => row.confirmedUnfinished), 'no "couldn\'t be finished" row').toBe(false)
  })

  it('unknown_position, no active turn: the unsaved streaming reply is gone, nothing claims it unfinished', () => {
    witnessThenReattach('unknown_position')
    expect(rows()).toStrictEqual([{ id: 'saved-question', role: 'user', streaming: false }])
    expect(Object.values(bucket().messagesById).some((row) => row.confirmedUnfinished)).toBe(false)
    expect(bucket().isStreaming).toBe(false)
  })

  it('CONTROL: the server reports the same turn still active, so its streaming row survives untouched', () => {
    witnessThenReattach('boot_mismatch', 'turn-cut')
    expect(bucket().activeTurnId).toBe('turn-cut')
    expect(rows()).toContainEqual({ id: 'live-reply', role: 'assistant', streaming: true })
    expect(bucket().messagesById['live-reply'].content).toBe('Partial text')
    expect(bucket().messagesById['live-reply'].confirmedUnfinished).toBeUndefined()
    expect(bucket().isStreaming, 'a running turn keeps streaming').toBe(true)
  })

  it('the user\'s own pending send survives the same commit that drops the dead reply', () => {
    witnessThenReattach('boot_mismatch', undefined, () => {
      // A send that failed while the socket was down stays in the chat with Retry.
      useChatStore.getState().appendMessage({ id: 'pending-send', role: 'user', content: 'Are you still there?', timestamp: STAMP, deliveryStatus: 'failed' })
    })
    const pending = Object.values(bucket().messagesById).filter((row) => row.role === 'user' && row.content === 'Are you still there?')
    expect(pending, 'the unsent user message is still shown').toHaveLength(1)
    expect(bucket().messagesById['live-reply'], 'the dead reply is still dropped').toBeUndefined()
  })
})

// Q7/r21 Ma: saved-on-page membership must protect the actual witnessed row,
// not just recreate an older prefix from history after silently deleting it.
// D4 live/control overlaps win; D7 preserves current state and real aliases.
const SAVED_PREFIX = 'The saved report prefix. '
const WITNESSED_REST = 'Further words this tab really watched before the restart.'
const WITNESSED_BODY = SAVED_PREFIX + WITNESSED_REST
const CURRENT_TURN = 'new-current-turn'
const CURRENT_REPLY = 'new-current-reply'
const CURRENT_BODY = 'Independent current work after restart.'

function commitSavedWitness(match: 'id' | 'mergedReplayIds') {
  const displayId = match === 'id' ? 'saved-cut-reply' : 'displayed-cut-reply'
  const savedId = 'saved-cut-reply'
  transport = historyTransport()
  transport.attach()
  const initial = page([question], { rows: 1 })
  expectValidPage(initial, [pos(1)], 1)
  transport.deliver(
    { ...snapshot(initial), seq: 4, boot_id: 'boot-1' }, state(), replay(question),
    { type: 'done', session_id: SID },
    { type: 'catch_up_complete', session_id: SID, seq: 4, boot_id: 'boot-1', mode: 'snapshot' },
  )
  transport.deliver({ type: 'token', session_id: SID, turn_id: 'turn-cut', agent_id: 'mia',
    message_id: displayId, content: match === 'id' ? WITNESSED_BODY : SAVED_PREFIX, seq: 5 })
  if (match === 'mergedReplayIds') {
    transport.deliver({ type: 'token', session_id: SID, turn_id: 'turn-cut', agent_id: 'mia',
      message_id: savedId, content: WITNESSED_REST, seq: 6 })
    expect(bucket().messagesById[displayId].mergedReplayIds,
      'instrument: the real token resolver, not a seeded alias field, coalesced the recorded id').toStrictEqual([savedId])
    expect(bucket().messagesById[savedId], 'instrument: there is no separate saved-id bubble before reconnect').toBeUndefined()
  }
  expect(bucket().messagesById[displayId]).toMatchObject({ id: displayId, role: 'assistant',
    content: WITNESSED_BODY, turnId: 'turn-cut', agentId: 'mia', isStreaming: true })
  expect(bucket().messagesById[displayId].historyPosition,
    'instrument: this is a witnessed live candidate, not an already-positioned history row').toBeUndefined()

  transport.connection.disconnect()
  transport = historyTransport()
  transport.attach()
  const saved = entry(2, { id: savedId, role: 'assistant', content: SAVED_PREFIX, agent_id: 'mia', turn_id: 'turn-cut' })
  const latest = page([question, saved], { rows: 2 })
  expectValidPage(latest, [pos(1), pos(2)], 2)
  expect(latest.entries.map(row => row.id), 'instrument: the selected page actually contains the cut reply source').toStrictEqual(['saved-question', savedId])
  expect(latest.entries.some(row => row.id === displayId)).toBe(match === 'id')
  // The candidate's old turn is NOT active. A different authoritative turn
  // keeps the current projection live, rather than protecting the old row by
  // the existing same-active-turn exclusion or pre-finalizing it as completed.
  transport.deliver(
    { ...snapshot(latest, 'boot_mismatch'), seq: 10, boot_id: 'boot-2' }, state(CURRENT_TURN),
    replay(question), replay(saved), { type: 'done', session_id: SID },
    { type: 'token', session_id: SID, turn_id: CURRENT_TURN, agent_id: 'mia', message_id: CURRENT_REPLY, content: CURRENT_BODY },
    { type: 'catch_up_complete', session_id: SID, seq: 10, boot_id: 'boot-2', mode: 'snapshot' },
  )
  return { displayId, savedId }
}

describe('Q7/r21 saved-on-page guard preserves an old witnessed reply alongside a different current turn', () => {
  it.each(['id', 'mergedReplayIds'] as const)('r21-a saved %s candidate survives the committed page without losing current text or display identity', (match) => {
    const { displayId, savedId } = commitSavedWitness(match)
    expect(bucket().history.historyLoaded, 'the real snapshot commit succeeded').toBe(true)
    expect(bucket().history.readerPositions).toStrictEqual([pos(1), pos(2)])
    expect(bucket().cursor).toStrictEqual({ bootId: 'boot-2', seq: 10 })
    expect(bucket().messagesById[displayId],
      'D4/D7: saved membership protects this witnessed display row, not a cold reconstruction of an older prefix').toMatchObject({
      id: displayId, role: 'assistant', content: WITNESSED_BODY, turnId: 'turn-cut', agentId: 'mia',
    })
    expect(bucket().messageOrder, 'one saved source coalesces onto its existing display identity, never a duplicate reply').toStrictEqual(['saved-question', displayId, CURRENT_REPLY])
    expect(bucket().historySourceAliases?.[pos(2)], 'the committed source occurrence keeps its existing display owner').toBe(displayId)
    expect(bucket().messagesById[displayId].historyPosition).toBe(pos(2))
    if (match === 'mergedReplayIds') {
      expect(bucket().messagesById[displayId].mergedReplayIds).toStrictEqual([savedId])
      expect(bucket().messagesById[savedId], 'the alias is not a second displayed reply').toBeUndefined()
    }
    expect(bucket().activeTurnId, 'the saved old reply must not stop unrelated current work').toBe(CURRENT_TURN)
    expect(bucket().messagesById[CURRENT_REPLY]).toMatchObject({ id: CURRENT_REPLY, role: 'assistant', content: CURRENT_BODY, turnId: CURRENT_TURN, isStreaming: true })
    expect(bucket().isStreaming).toBe(true)
  })
})
