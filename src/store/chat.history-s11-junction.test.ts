// S11 J2: two legal native attach orders, with independently fixed literals.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { reset, cleanup } from '../../tests/chat-history-paging/final-gate-harness'
import { A, B, P, T, TURN, SID, assistantRows, begin, bucket, done, empty, liveTool, native, pos, prose, rest, segment, snapshotStart, token, tool } from '../../tests/chat-history-paging/s11-fold-harness'

beforeEach(() => { reset(); begin() })
afterEach(cleanup)

for (const shape of ['tool-first', 'prose-first'] as const) {
  it.each(['token-before-complete', 'commit-before-token'] as const)(`T71 B88: ${shape} ${'%s'} rekeys once and repeats without duplication`, async arrival => {
    const textID = shape === 'tool-first' ? P : B
    const initial = shape === 'tool-first' ? [empty(1), tool(2)] : [prose(1, A, 'First.'), tool(2)]
    const commit = snapshotStart(initial, true)
    // A projected token before catch_up_complete is an unsequenced replace;
    // a post-commit live token is a normal delta. These are deliberate legal
    // orders, not a claim to have collected an unavailable CI frame trace.
    if (arrival === 'token-before-complete') {
      token(textID, 'Answer.', { replace: true })
      expect(assistantRows(), 'B88: exactly one row during the pre-completion projection; earliest A may already be progressively known').toHaveLength(1)
      commit()
    } else {
      commit()
      expect(assistantRows().map(row => row.id), 'tool-first uses a provisional key until the first text arrives').toEqual([shape === 'tool-first' ? T : A])
      token(textID, 'Answer.')
    }
    const key = shape === 'tool-first' ? P : A
    const expected = shape === 'tool-first' ? 'Answer.' : 'First.\n\nAnswer.'
    expect(assistantRows().map(row => row.id), 'J2 binds saved and live sources into one segment').toEqual([key])
    expect(bucket().messagesById[key].content).toBe(expected)
    expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBe(key)
    expect(bucket().historySourceAliases?.[pos(2)]).toBe(key)
    expect(bucket().messagesById[key].mergedReplayIds ?? []).toEqual(shape === 'tool-first' ? [] : [B])
    token(textID, ' More.')
    expect(assistantRows().map(row => row.id)).toEqual([key])
    expect(bucket().messagesById[key].content, 'later token still resolves through the atomic rekey').toBe(expected + ' More.')
    done()
    expect(bucket().messagesById[key].isStreaming).toBe(false)
    const saved = [...initial, prose(3, textID, 'Answer. More.')]
    const assertSettled = () => segment(key, shape === 'tool-first' ? 'Answer. More.' : 'First.\n\nAnswer. More.', shape === 'tool-first' ? [T, P] : [A, T, B], shape === 'tool-first' ? [pos(2), pos(3)] : [pos(1), pos(2), pos(3)], ['s11-call'], [shape === 'tool-first' ? 0 : 6], shape === 'tool-first' ? [] : [B])
    native(saved); assertSettled()
    native(saved); assertSettled()
    useChatStore.getState().enterHistoryReading(SID)
    await rest(saved, 'refresh'); assertSettled()
    expect(bucket().history.readerPositions).toEqual([pos(1), pos(2), pos(3)])
    const comparableRows = () => assistantRows().map(row => ({ id: row.id, text: row.content, calls: row.tool_calls?.map(call => [call.id, Reflect.get(call, 'textOffset')]) ?? [] }))
    const joinedTab = comparableRows()
    // Supplemental first-tab control. Literal expectations above remain the
    // oracle even if both paths were to share the same defect.
    cleanup(); reset(); begin()
    if (shape === 'prose-first') token(A, 'First.')
    liveTool(); token(textID, 'Answer.'); token(textID, ' More.'); done()
    expect(comparableRows(), 'B88: the joined tab equals the same turn driven live from its beginning').toEqual(joinedTab)
  })
}

it('T80 B95: committed unknown-turn tool-only row is not adopted by a live active-turn token', async () => {
  await rest([tool(1, T, 's11-call', { turn_id: undefined })])
  expect(assistantRows().map(row => row.id)).toEqual([T])
  token(P, 'New live answer.')
  expect(assistantRows().map(row => row.id), 'an unknown saved turn never borrows the active turn').toEqual([T, P])
  expect(bucket().messagesById[T].content).toBe('')
  expect(bucket().messagesById[P].content).toBe('New live answer.')
  expect(bucket().historySourceAliases?.[pos(1)]).toBe(T)
  expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBe(T)
  expect(bucket().messagesById[P].mergedReplayIds ?? []).toEqual([])
})

it('T82 B97: a live-only provisional tool placeholder atomically adopts the first text ID', () => {
  liveTool()
  const provisional = assistantRows()[0]?.id
  expect(provisional, 'instrument: actual tool start minted a provisional row').toBeDefined()
  const snapshots: { ids: string[]; owner: string | undefined; aliases: string[]; live: string[] }[] = []
  const unsubscribe = useChatStore.subscribe(state => {
    const b = state.sessionsById[SID]
    if (b?.messagesById[P]) snapshots.push({ ids: b.messageOrder, owner: b.toolCallOwnerMessageId?.['s11-call'], aliases: Object.keys(b.mergedReplayMessageIds ?? {}), live: b.historyLiveOrder ?? [] })
  })
  try { token(P, 'Answer.') } finally { unsubscribe() }
  expect(snapshots.length, 'instrument: observed actual synchronous store commit').toBeGreaterThan(0)
  for (const snap of snapshots) {
    expect(snap.ids, 'no observable half-rekey update').toEqual([P])
    expect(snap.owner).toBe(P)
    expect(snap.aliases).toEqual([])
    expect(snap.live.filter(id => id === provisional && id !== P)).toEqual([])
  }
  expect(assistantRows().map(row => row.id)).toEqual([P])
  expect(bucket().messagesById[P]).toMatchObject({ content: 'Answer.', turnId: TURN })
})
