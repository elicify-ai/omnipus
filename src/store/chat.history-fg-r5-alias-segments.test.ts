// D2 distinguishes canonical sources from display aliases. D4 says preserve
// duplicate suppression WITHOUT losing output; all saved public text survives.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { replay } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup, open, socket, connect, AVAILABLE, SID, BOOT, entry, page, pos, expectValidPage, snapshotAt, complete, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R5: same-boot completed snapshot with BOTH selected alias sources retains both saved answer segments', () => {
  const question = entry(1, { id: 'r5-question', role: 'user', content: 'Give both parts of the answer' })
  open([question], [replay(question)])
  const segmentA = 'First independently saved answer segment. '
  const segmentB = 'Second independently saved answer segment.'
  socket().deliver(
    { type: 'token', session_id: SID, turn_id: 'r5-turn', agent_id: 'mia', message_id: 'r5-A', content: segmentA, seq: 101 },
    { type: 'token', session_id: SID, turn_id: 'r5-turn', agent_id: 'mia', message_id: 'r5-B', content: segmentB, seq: 102 },
  )
  expect(bucket().messagesById['r5-A']).toMatchObject({ content: segmentA + segmentB, turnId: 'r5-turn', agentId: 'mia', isStreaming: true })
  expect(bucket().messagesById['r5-A'].mergedReplayIds, 'instrument: actual token resolver joined B to A').toStrictEqual(['r5-B'])
  expect(bucket().messagesById['r5-B']).toBeUndefined()
  const a = entry(2, { id: 'r5-A', role: 'assistant', content: segmentA, agent_id: 'mia', turn_id: 'r5-turn' })
  const b = entry(3, { id: 'r5-B', role: 'assistant', content: segmentB, agent_id: 'mia', turn_id: 'r5-turn' })
  const saved = page([question, a, b])
  expectValidPage(saved, [pos(1), pos(2), pos(3)], 3)
  // The turn finishes while disconnected: snapshot reports no active turn.
  // Same boot; do not let restart-cut handling mask selected-source alias loss.
  const wire = connect(); wire.attach()
  wire.deliver({ ...snapshotAt(saved), seq: 103 }, AVAILABLE, replay(question), replay(a), replay(b), { type: 'done', session_id: SID }, { ...complete(), seq: 103 })
  expect(bucket().history.historyLoaded, 'the selected page really committed').toBe(true)
  expect(bucket().cursor).toStrictEqual({ bootId: BOOT, seq: 103 })
  expect(bucket().history.readerPositions).toStrictEqual([pos(1), pos(2), pos(3)])
  const readable = bucket().messageOrder.map(id => bucket().messagesById[id]).filter(row => row.role === 'assistant').map(row => row.content).join('')
  expect(readable, 'D2/D4: both independently selected saved segments remain readable exactly once, not silently truncated by alias rekey/dedup').toBe(segmentA + segmentB)
  expect(bucket().activeTurnId).toBeNull()
  expect(bucket().isStreaming).toBe(false)
})
