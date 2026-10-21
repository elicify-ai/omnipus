// T08 + semantic-fixture pack. B06/B08/B11/B23/B26/B31/B43.
// REAL: store/reducers, WsConnection/parser and attach batching. Network +
// browser frame delivery only are controlled. No second replay buffer.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { queryClient } from '@/lib/queryClient'
import type { SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import {
  SID, STAMP, UNAVAILABLE, entry, expectPageAccounting, expectValidPage, page, pos, replay, snapshot,
} from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

let transport: ReturnType<typeof historyTransport> | undefined
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(STAMP)
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
})
afterEach(() => {
  transport?.connection.disconnect()
  transport = undefined
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals()
  vi.clearAllTimers()
  vi.useRealTimers()
})

function state(available = true): SessionStateFrame {
  return {
    type: 'session_state', session_id: SID, user_id: 'history-reader',
    pending_approvals: [], emitted_at: STAMP,
    history_state: available ? { status: 'available' } : { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE },
  }
}
function seedCached() {
  useChatStore.getState().setMessages([{ id: 'cached-reader', role: 'user', content: 'Keep this cached row', timestamp: STAMP }])
  useChatStore.setState((s) => ({ sessionsById: {
    ...s.sessionsById, [SID]: { ...s.sessionsById[SID], cursor: { bootId: 'prior-boot', seq: 5 }, replayCompletedForSession: null, hasEarlierMessages: true, unansweredLastUserMessageId: null },
  } }))
}

function complete() {
  return { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'snapshot' } as const
}

describe('T08 replay completion composes with render-once base — B06/B26', () => {
  it('commits historical rows once only after complete, including projection delivered after replay done', () => {
    seedCached()
    const one = entry(1)
    const two = entry(2, { role: 'assistant', type: 'message', content: 'Final saved answer' })
    const history = page([one, two])
    expectPageAccounting(history, [pos(1), pos(2)], 2)
    transport = historyTransport()
    transport.attach()
      transport.deliver(snapshot(history), state(), replay(one), replay(two), { type: 'done', session_id: SID })
      expect(useChatStore.getState().messages.map((m) => m.id), 'D7 interim releases at COMPLETE, not done').toStrictEqual(['cached-reader'])
      expect(transport.batches, 'no partial snapshot dispatch').toStrictEqual([])
      transport.deliver({ ...state(), active_turn: { turn_id: 'active-now', agent_id: 'mia', started_at: STAMP } })
      expect(useChatStore.getState().sessionsById[SID].cursor).toStrictEqual({ bootId: 'prior-boot', seq: 5 })
      transport.deliver(complete())
      expect(useChatStore.getState().messages.map(({ id, content }) => ({ id, content }))).toStrictEqual([
        { id: 'history-1', content: 'Saved row 1' }, { id: 'history-2', content: 'Final saved answer' },
      ])
      expect(transport.batches, 'one bounded transport batch; no partial replay dispatch').toHaveLength(1)
      expect(useChatStore.getState().sessionsById[SID].activeTurnId, 'projection after done was applied').toBe('active-now')
      expect(useChatStore.getState().sessionsById[SID].cursor).toStrictEqual({ bootId: 'history-boot', seq: 100 })
  })

  it('live_only completion advances only the live cursor, not reconstructed-history readiness', () => {
    seedCached()
    transport = historyTransport()
    transport.attach()
    transport.deliver(state(false), { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'live_only' })
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.cursor, 'D7a completed controls have a valid live cursor').toStrictEqual({ bootId: 'history-boot', seq: 100 })
    expect(bucket.awaitingCatchUp, 'live controls are unlocked').toBe(false)
    expect(bucket.isReplaying).toBe(false)
    expect(bucket.replayCompletedForSession, 'D7a live_only is NOT successful reconstructed history').toBeNull()
    expect(bucket.messageOrder, 'no history wipe or fabricated partial page').toStrictEqual(['cached-reader'])
    expect(bucket.hasEarlierMessages, 'unavailability is not false terminal continuation').toBe(true)
    expect(bucket.unansweredLastUserMessageId, 'no history-derived unanswered sweep').toBeNull()
    expect(Reflect.get(bucket, 'history'), 'agreed owner separates history readiness from current-control readiness').toMatchObject({
      historyLoaded: false, liveControlsReady: true,
      serverState: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE },
    })
  })

  it('incomplete socket catch-up discards staged history and keeps the prior cursor and reader', () => {
    seedCached()
    const history = page([entry(1)])
    expectPageAccounting(history, [pos(1)], 1)
    transport = historyTransport()
    transport.attach()
    transport.deliver(snapshot(history), state(), replay(entry(1)))
    transport.socket.close(1006, 'transport test disconnect')
    transport.flush()
    expect(useChatStore.getState().messages.map((m) => m.id), 'D7 no partial successful snapshot').toStrictEqual(['cached-reader'])
    expect(useChatStore.getState().sessionsById[SID].cursor).toStrictEqual({ bootId: 'prior-boot', seq: 5 })
    expect(useChatStore.getState().sessionsById[SID].replayCompletedForSession).toBeNull()
  })
})

describe('T08 snapshot semantic accounting — B23 / earlier CHECK survivor', () => {
  it('one narration owner and a finished start/result pair retain one source position and exactly two logical rows', () => {
    const source = entry(1, { id: 'history-tool-owner', type: 'message', role: 'assistant', content: 'Read a file', tool_calls: [{
      id: 'grouped-read', tool: 'read_file', status: 'success', parameters: { path: 'report.txt' },
      result: { content: 'Stored bytes' }, content_state: 'full',
    }] })
    const origin = { position: pos(1), entry_id: source.id }
    const start = { type: 'tool_call_start', session_id: SID, call_id: 'grouped-read', tool: 'read_file', params: { path: 'report.txt' }, history_origin: origin } as const
    const result = { type: 'tool_call_result', session_id: SID, call_id: 'grouped-read', tool: 'read_file', status: 'success', result: { content: 'Stored bytes' }, history_origin: origin } as const
    const history = page([source], { rows: 2 })
    // Independently encode this fixture's paired carriers, excluding diagnostic
    // accounting and separately governed current controls from their meter.
    const { soft_byte_budget, projected_bytes, byte_budget_exceeded, budget_override, ...metadata } = history.page_info
    void soft_byte_budget; void projected_bytes; void byte_budget_exceeded; void budget_override
    const historical = [{ ...snapshot(history), page_info: metadata }, replay(source), start, result, { type: 'done', session_id: SID }]
    history.page_info.projected_bytes = Math.max(Buffer.byteLength(JSON.stringify({ ...history, page_info: metadata })), Buffer.byteLength(JSON.stringify(historical)))
    expectValidPage(history, [pos(1)], 2) // D4: narration1 + tool pair1, NOT three frames or one row.
    transport = historyTransport()
    transport.attach()
    transport.deliver(snapshot(history), state(), replay(source), start, result, { type: 'done', session_id: SID }, complete())
    expect(useChatStore.getState().messages).toHaveLength(1)
    expect(useChatStore.getState().messages).toMatchObject([{
      id: 'history-tool-owner', content: 'Read a file', historyPosition: pos(1),
      tool_calls: [{ id: 'grouped-read', tool: 'read_file', status: 'success', result: { content: 'Stored bytes' }, textOffset: 11 }],
    }])
  })

  it.each([
    ['source_group_count', 0], ['source_group_count', 1], ['source_group_count', 3],
    ['row_count', 0], ['row_count', 1], ['row_count', 3],
  ] as const)('rejects a wrong snapshot %s=%i rather than certifying history', (field, wrongCount) => {
    const sources = [entry(1), entry(2, { type: 'message', role: 'assistant', content: 'Saved answer' })]
    const valid = page(sources)
    expectPageAccounting(valid, [pos(1), pos(2)], 2)
    transport = historyTransport()
    transport.attach()
    transport.deliver(snapshot(valid), state(), ...sources.map((source) => replay(source)), { type: 'done', session_id: SID }, complete())
    expect(useChatStore.getState().messages.map((m) => m.id), 'positive control: consumer accepted the valid two-source snapshot')
      .toStrictEqual(['history-1', 'history-2'])
    const malformed = snapshot(page([entry(3), entry(4)]))
    malformed.page_info[field] = wrongCount // structurally valid zero/under/over count; semantically false.
    transport.attach()
    transport.deliver(malformed, state(), replay(entry(3)), replay(entry(4)), { type: 'done', session_id: SID }, complete())
    expect(useChatStore.getState().messages.map((m) => m.id), `D10 wrong ${field} must reject the whole staged page`)
      .toStrictEqual(['history-1', 'history-2'])
  })
})
