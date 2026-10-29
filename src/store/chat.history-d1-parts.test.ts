// #1452 D1: independent oracles B79-B85, S1/S2/S2a/S6-S8.
// GREEN and mutation proof are deferred to an independent CHECK instance.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useChatStore } from './chat'
import { pos, replay } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup, open, socket, response } from '../../tests/chat-history-paging/final-gate-harness'
import { A, B, TURN, SID, LIVE_A, LIVE_B, SAVED_A, SAVED_B, assistantRows, bucket, commitSnapshot, entry, finish, latestTail, livePair, page, readable, rest, sources } from '../../tests/chat-history-paging/d1-alias-harness'

beforeEach(reset)
afterEach(cleanup)

it('T56 B79: saved A remains readable when latest selects B and older later supplies A', async () => {
  livePair(); finish()
  const [a, b] = sources()
  await rest(latestTail(b), 'latest', true)
  await rest([a], 'older', false, true)
  expect(readable(), 'saved segment A and saved segment B must both remain readable exactly once').toBe(SAVED_A + SAVED_B)
  expect(assistantRows(), 'canonical source aliases do not create duplicate display rows').toHaveLength(1)
  const owner = assistantRows()[0].id
  expect([bucket().historySourceAliases?.[pos(1)], bucket().historySourceAliases?.[pos(3)]]).toEqual([owner, owner])
})

it('T57 B80: Return to latest during streaming shows B once and later C extends the same bubble', async () => {
  livePair()
  const selected = page(sources(LIVE_A, LIVE_B), { info: { limit: 20, min_rows: 1 } })
  const read = vi.fn(async () => response(selected)); vi.stubGlobal('fetch', read)
  await useChatStore.getState().returnToLatestHistory(SID, { limit: 20, min_rows: 1 })
  expect(read).toHaveBeenCalledTimes(1)
  expect(bucket().history.requests.latest.status).toBe('idle')
  expect(assistantRows(), 'N3: exactly one assistant bubble, never a second saved B').toHaveLength(1)
  expect(readable()).toBe(LIVE_A + LIVE_B)
  socket().deliver({ type: 'token', session_id: SID, turn_id: TURN, agent_id: 'mia', message_id: 'd1-source-C', content: ' Later C.', seq: 103 })
  expect(assistantRows()).toHaveLength(1)
  expect(readable()).toBe(LIVE_A + LIVE_B + ' Later C.')
})

it('T58 B81: the real closed-window refresh binds both saved parts without splitting a running reply', async () => {
  livePair()
  const selected = sources(LIVE_A, LIVE_B)
  await rest(selected)
  useChatStore.getState().enterHistoryReading(SID)
  expect(bucket().history.readerRange).toEqual({ window_start: pos(1), window_end: pos(3) })
  const reads = await rest(selected, 'refresh')
  const url = new URL(String(reads.mock.calls[0]?.[0]), 'http://fixture.invalid')
  expect(url.pathname).toBe(`/api/v1/sessions/${SID}/messages`)
  expect([url.searchParams.get('window_start'), url.searchParams.get('window_end')]).toEqual([pos(1), pos(3)])
  expect(assistantRows(), 'S6: refresh must use the same part binding, not messagesById-only merging').toHaveLength(1)
  expect(readable()).toBe(LIVE_A + LIVE_B)
  expect([bucket().historySourceAliases?.[pos(1)], bucket().historySourceAliases?.[pos(3)]]).toEqual([A, A])
})

for (const knownTurn of [true, false]) {
  it.each(['one page', 'split pages'] as const)(`T59 B83/B95: tool-only T turn=${knownTurn} has exact ownership inside or outside A+B (%s)`, async delivery => {
    livePair(); finish()
    const [a, b] = sources()
    const tool = entry(2, { id: 'd1-tool-owner', type: 'tool_call', role: 'assistant', agent_id: 'mia', turn_id: knownTurn ? TURN : undefined, content: '', tool_calls: [
      { id: 'd1-saved-call', tool: 'bash', status: 'success', content_state: 'full', parameters: { command: 'true' }, result: { text: 'saved result' } },
    ] })
    if (delivery === 'one page') await rest([a, tool, b])
    else { await rest(latestTail(b), 'latest', true); await rest([a, tool], 'older', false, true) }
    const rows = assistantRows()
    // Founder Q3/S11 changes only the same-known-turn oracle. livePair's
    // recorded empty joiner stays immutable despite the newly learned T.
    expect(rows.map(row => row.content), 'S8: the combined row occupies its first bound position; unknown T stays separate').toEqual(knownTurn ? [SAVED_A + SAVED_B] : [SAVED_A + SAVED_B, ''])
    expect(rows.flatMap(row => row.tool_calls ?? []).filter(call => call.id === 'd1-saved-call')).toHaveLength(1)
    const owner = knownTurn ? A : 'd1-tool-owner'
    expect(bucket().toolCallOwnerMessageId?.['d1-saved-call']).toBe(owner)
    expect(bucket().historySourceAliases?.[pos(2)]).toBe(owner)
    expect(bucket().messagesById[owner].tool_calls?.map(call => call.id)).toEqual(['d1-saved-call'])
    if (knownTurn) {
      expect(Reflect.get(bucket().messagesById[A].tool_calls![0], 'textOffset')).toBe(SAVED_A.length)
      expect(bucket().messagesById[A].historyParts?.map(part => part.sourceId)).toEqual([A, 'd1-tool-owner', B])
      expect(bucket().messagesById[A].mergedReplayIds).toEqual([B])
      expect(Object.keys(bucket().mergedReplayMessageIds ?? {})).toEqual([B])
    }
  })
}

it.each(['replay paragraph', 'token tool boundary'] as const)('T60 B84: completed settlement preserves the actual recorded joiner (%s)', kind => {
  if (kind === 'token tool boundary') livePair(true)
  else {
    open([], [])
    const [a, b] = sources(LIVE_A, LIVE_B)
    socket().deliver(replay(a), replay(b))
    expect(assistantRows(), 'instrument: actual replay combine site produced one row').toHaveLength(1)
    expect(readable(), 'instrument: replay uses its actual paragraph joiner').toBe(LIVE_A + '\n\n' + LIVE_B)
  }
  commitSnapshot(sources())
  expect(readable(), 'S2a: replace per-part text, retain the exact real joiner').toBe(SAVED_A + '\n\n' + SAVED_B)
  expect(assistantRows()).toHaveLength(1)
})

it('T61 B85: REST and native snapshot commit have identical text, order and source bindings', async () => {
  livePair(); finish()
  await rest(sources())
  const restView = { rows: assistantRows().map(row => ({ id: row.id, content: row.content })), aliases: { ...bucket().historySourceAliases } }
  cleanup(); reset()
  livePair(); finish()
  commitSnapshot(sources())
  const wsView = { rows: assistantRows().map(row => ({ id: row.id, content: row.content })), aliases: { ...bucket().historySourceAliases } }
  expect(restView.rows.map(row => row.content), 'parity alone cannot certify two equally broken carriers').toEqual([SAVED_A + SAVED_B])
  expect(wsView).toEqual(restView)
  expect(restView.aliases[pos(1)]).toBe(A)
  expect(restView.aliases[pos(3)]).toBe(A)
  expect(assistantRows().some(row => row.id === B)).toBe(false)
})
