import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ServerFrame, TokenFrame } from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { emptySessionState } from './chat/session'
import { pendingCancelAckSids } from './chat/runtime-state'

// Oracle: /workspace/logs/1534/arch-decision.md §§1,5,6. Real stores/reducers
// and cancelStream; only the outbound socket is a recording process edge.
// Observe counts through actual cancel payload, not an invented counter API.
const SID = 'stop-shown-1534'
const TURN = 'turn-shown-1534'
const MID = 'message-shown-1534'
const sender = { send: vi.fn<WsConnection['send']>() }

function frame(value: ServerFrame) { useChatStore.getState().handleFrame(value) }
function token(content: string, extra: Partial<TokenFrame> = {}) {
  frame({ type: 'token', session_id: SID, turn_id: TURN, message_id: MID, content, ...extra })
}
function bucket(sid = SID) { return useChatStore.getState().sessionsById[sid] }
function rows(sid = SID) { const b = bucket(sid); return b.messageOrder.map(id => b.messagesById[id]) }
function stop(sid = SID) { expect(useChatStore.getState().cancelStream(sid)).toBe(true) }
function expectShown(messageId: string, codePoints: number) {
  expect(sender.send.mock.calls).toEqual([[{
    type: 'cancel', session_id: SID,
    shown: { message_id: messageId, turn_id: TURN, code_points: codePoints },
  }]])
}
function reset() {
  pendingCancelAckSids.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useUiStore.setState({ toasts: [] })
}
beforeEach(() => {
  reset()
  sender.send.mockReset().mockReturnValue(true)
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia' })
  useChatStore.setState({ sessionsById: { [SID]: emptySessionState() } })
  useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
})
afterEach(() => { reset(); vi.restoreAllMocks() })

describe('#1534 applied code-point checkpoint', () => {
  it('append counts Unicode code points including emoji and combining characters, not UTF-16 or bytes', () => {
    token('naïve 名前 😀 ')
    token('é')
    expect(rows().map(m => m.content)).toEqual(['naïve 名前 😀 é'])
    stop()
    expectShown(MID, 13) // 5+1+2+1+1+1+1+1, combining mark is a separate point
  })

  it('first token rekeying an optimistic placeholder is included in the applied counter', () => {
    useChatStore.setState({ sessionsById: { [SID]: {
      ...emptySessionState(), isStreaming: true,
      messageOrder: ['local-placeholder'], messagesById: { 'local-placeholder': {
        id: 'local-placeholder', role: 'assistant', content: '', timestamp: 't', status: 'streaming', isStreaming: true,
      } },
    } } })
    token('😀A')
    expect(rows().map(m => [m.id, m.content])).toEqual([[MID, '😀A']])
    stop()
    expectShown(MID, 2)
  })

  it('replace resets this wire ID count rather than accumulating the old snapshot', () => {
    token('old text that must not count')
    token('😀', { replace: true })
    token('é')
    expect(rows().map(m => m.content)).toEqual(['😀é'])
    stop()
    expectShown(MID, 3)
  })

  it('new wire ID in the same bubble counts only its own tokens, never the inserted paragraph separator', () => {
    token('FIRST')
    frame({ type: 'tool_call_start', session_id: SID, call_id: 'tc', tool: 'web_search', params: {} })
    frame({ type: 'tool_call_result', session_id: SID, call_id: 'tc', tool: 'web_search', status: 'success', result: {} })
    token('😀', { message_id: 'second-message' })
    token('é', { message_id: 'second-message' })
    expect(rows().map(m => m.content)).toEqual(['FIRST\n\n😀é'])
    stop()
    expectShown('second-message', 3)
  })

  it('zero-content applied token with a wire ID sends zero for that ID', () => {
    token('')
    stop()
    expectShown(MID, 0)
  })

  it('reads shown from exactly the bubble already frozen before connection.send', () => {
    token('SCREEN')
    // Keep an unrelated completed answer in the thread: its counters and
    // identity must not leak into the currently frozen answer's checkpoint.
    const b = bucket()
    useChatStore.setState({ sessionsById: { [SID]: {
      ...b, messageOrder: ['other-done', ...b.messageOrder],
      messagesById: { ...b.messagesById, 'other-done': {
        id: 'other-done', role: 'assistant', content: 'OTHER', timestamp: 't', status: 'done', turnId: 'other-turn',
      } },
    } } })
    sender.send.mockImplementation((payload) => {
      expect(rows().map(m => [m.id, m.content, m.status])).toEqual([
        ['other-done', 'OTHER', 'done'], [MID, 'SCREEN', 'interrupted'],
      ])
      expect(payload).toEqual({ type: 'cancel', session_id: SID, shown: { message_id: MID, turn_id: TURN, code_points: 6 } })
      // A token delivered reentrantly at the transport boundary cannot alter
      // the already-captured checkpoint or the frozen body.
      token('UNSEEN')
      return true
    })
    stop()
    expectShown(MID, 6)
    expect(rows().map(m => m.content)).toEqual(['OTHER', 'SCREEN'])
  })

  it('background Stop takes the named session checkpoint, never the foreground answer', () => {
    token('BACKGROUND')
    const other = 'foreground-1534'
    useChatStore.setState({ sessionsById: { ...useChatStore.getState().sessionsById, [other]: emptySessionState() } })
    useSessionStore.setState({ activeSessionId: other })
    token('FOREGROUND', { session_id: other, message_id: 'foreground-message', turn_id: 'foreground-turn' })
    stop(SID)
    expectShown(MID, 10)
    expect(rows(other).map(m => [m.content, m.status])).toEqual([['FOREGROUND', 'streaming']])
  })

  it('freezing an empty placeholder sends code_points zero without a fabricated message ID', () => {
    useChatStore.setState({ sessionsById: { [SID]: { ...emptySessionState(), isStreaming: true } } })
    stop()
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID, shown: { code_points: 0 } }]])
    expect(rows().map(m => [m.content, m.status])).toEqual([['', 'interrupted']])
  })

  it('empty optimistic placeholder sends zero with the known turn but no local placeholder ID', () => {
    useChatStore.setState({ sessionsById: { [SID]: {
      ...emptySessionState(), isStreaming: true, activeTurnId: TURN,
      messageOrder: ['local-placeholder'], messagesById: { 'local-placeholder': {
        id: 'local-placeholder', role: 'assistant', content: '', timestamp: 't', status: 'streaming', isStreaming: true,
      } },
    } } })
    stop()
    expect(sender.send.mock.calls).toEqual([[{
      type: 'cancel', session_id: SID, shown: { turn_id: TURN, code_points: 0 },
    }]])
    expect(rows().map(m => [m.id, m.content, m.status])).toEqual([['local-placeholder', '', 'interrupted']])
  })

  it('message-ID-less fallback sends no shown record', () => {
    token('fallback', { message_id: undefined })
    stop()
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID }]])
  })

  it('idle goal-gap Stop sends no shown record and leaves completed text alone', () => {
    token('COMPLETE')
    frame({ type: 'done', session_id: SID, turn_id: TURN })
    // Arrange an already-running goal in an idle gap. Activating a new goal
    // (and its separate acknowledgement row) is not the Stop under test.
    useChatStore.setState({ sessionsById: { [SID]: { ...bucket(), goalStatus: {
      type: 'goal_status', session_id: SID, goal_id: 'g', state: 'active', condition: 'finish',
      round: 1, max_rounds: 5, latest_reason: '', active_loops: 1, cap: 3,
    } } } })
    stop()
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID }]])
    expect(rows().map(m => [m.content, m.status])).toEqual([['COMPLETE', 'done']])
  })

  it('confirming tree press omits shown even if another answer is streaming', () => {
    token('SCREEN')
    expect(useChatStore.getState().cancelStream(SID, 'tree')).toBe(true)
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID, scope: 'tree' }]])
  })
})

describe('#1534 post-freeze bubble guards', () => {
  it('same-turn token under a new message ID cannot open another bubble after Stop', () => {
    token('SCREEN')
    stop()
    expect(pendingCancelAckSids.has(SID)).toBe(true)
    token('UNSEEN NEXT STEP', { message_id: 'later-minted' })
    expect(rows().map(m => [m.id, m.content, m.status])).toEqual([[MID, 'SCREEN', 'interrupted']])
    expect(bucket().isStreaming).toBe(false)
  })

  it('frozen empty placeholder blocks the first real tokens while cancel ack is pending', () => {
    useChatStore.setState({ sessionsById: { [SID]: { ...emptySessionState(), isStreaming: true } } })
    stop()
    const placeholder = rows()[0].id
    expect(pendingCancelAckSids.has(SID)).toBe(true)
    token('FIRST REAL TOKEN')
    expect(rows().map(m => [m.id, m.content, m.status])).toEqual([[placeholder, '', 'interrupted']])
    expect(bucket().isStreaming).toBe(false)
  })

  it('terminal ack releases the session so a genuinely new turn can stream normally', () => {
    token('SCREEN')
    stop()
    frame({ type: 'done', session_id: SID, turn_id: TURN })
    expect(pendingCancelAckSids.has(SID)).toBe(false)
    token('NEW ANSWER', { message_id: 'new-answer', turn_id: 'new-turn' })
    expect(rows().map(m => [m.content, m.status])).toEqual([['SCREEN', 'interrupted'], ['NEW ANSWER', 'streaming']])
  })
})
