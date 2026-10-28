// S11 B87/B89–B92. All keys, texts and offsets below derive from spec literals.
// GREEN and mutation proof belong to an independent CHECK, not this author.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { reset, cleanup } from '../../tests/chat-history-paging/final-gate-harness'
import { A, B, E, P, T, SID, assistantRows, begin, bucket, done, empty, liveTool, native, pos, prose, rest, segment, tailPadding, textToolText, token, tool, toolFirst } from '../../tests/chat-history-paging/s11-fold-harness'

beforeEach(() => { reset(); begin() })
afterEach(cleanup)

it('T69 B87: REST tool-first accounting/tool/prose becomes exactly one P row', async () => {
  await rest(toolFirst())
  segment(P, 'Answer.', [T, P], [pos(2), pos(3)], ['s11-call'], [0])
  expect(bucket().history.readerPositions).toEqual([pos(1), pos(2), pos(3)])
  expect(bucket().messagesById[E]?.content, 'accounting stays silent and separate, never an invented text part').toBe('')
})
it('T70 B87/B85: real tool-only origin frames commit the same literal single row', () => {
  native(toolFirst())
  segment(P, 'Answer.', [T, P], [pos(2), pos(3)], ['s11-call'], [0])
  expect(bucket().history.readerPositions).toEqual([pos(1), pos(2), pos(3)])
})
it.each(['REST', 'snapshot'] as const)('T72 B89: %s keeps both tool rounds at offset0 before the first text', async carrier => {
  const sources = [empty(1, 's11-E1'), tool(2, 's11-T1', 's11-c1'), empty(3, 's11-E2'), tool(4, 's11-T2', 's11-c2'), prose(5, P, 'Answer.')]
  if (carrier === 'REST') await rest(sources); else native(sources)
  segment(P, 'Answer.', ['s11-T1', 's11-T2', P], [pos(2), pos(4), pos(5)], ['s11-c1', 's11-c2'], [0, 0])
})

for (const shape of ['prose-first', 'tool-first'] as const) {
  for (const cut of [1, 2]) {
    it.each(['older', 'newer'] as const)(`T73 B90: ${shape} cut${cut} folds without loss in %s direction`, async direction => {
      const sources = shape === 'prose-first' ? textToolText() : toolFirst()
      const left = sources.slice(0, cut), right = sources.slice(cut)
      if (direction === 'older') {
        await rest([...right, ...tailPadding().slice(0, 20 - right.length)], 'latest', true)
        useChatStore.getState().enterHistoryReading(SID)
        await rest(left, 'older', false, true)
      } else {
        const tail = tailPadding().slice(0, 20 - right.length)
        await rest([...right, ...tail], 'latest', true)
        useChatStore.getState().enterHistoryReading(SID)
        const adjacentHead = Array.from({ length: 20 - left.length }, (_, i) => prose(-100 + i, `s11-adjacent-${i}`, `Earlier question ${i}`, { role: 'user', turn_id: undefined }))
        await rest([...adjacentHead, ...left], 'older', true, true)
        // Actual500-source eviction establishes the newer edge at the cut;
        // every nonterminal read reaches its legal row target of20.
        const padding = Array.from({ length: 480 }, (_, i) => prose(-1000 + i, `s11-head-${i}`, `Independent ${i}`, { role: 'user', turn_id: undefined }))
        for (let end = padding.length; end > 0; end -= 20) {
          await rest(padding.slice(Math.max(0, end - 20), end), 'older', end > 20, true)
        }
        expect(bucket().history.newerBoundary).toBe(left.at(-1)!.history_position)
        await rest([...right, ...tail], 'newer')
      }
      if (shape === 'prose-first') segment(A, 'First.\n\nSecond.', [A, T, B], [pos(1), pos(2), pos(3)], ['s11-call'], [6], [B])
      else segment(P, 'Answer.', [T, P], [pos(2), pos(3)], ['s11-call'], [0])
    })
  }
}

for (const withTool of [false, true]) {
  it.each(['REST', 'snapshot', 'refresh', 'witnessed'] as const)(`T74 B91: tool=${withTool} keeps its exact immutable seam on %s`, async carrier => {
    const sources = withTool ? textToolText() : [prose(1, A, 'First.'), prose(3, B, 'Second.')]
    if (carrier === 'witnessed') {
      token(A, 'First.')
      if (withTool) liveTool()
      token(B, 'Second.'); done()
      expect(assistantRows()[0].content, 'live instrument establishes the recorded seam').toBe(withTool ? 'First.\n\nSecond.' : 'First.Second.')
      native(sources)
    } else if (carrier === 'snapshot') native(sources)
    else {
      await rest(sources)
      if (carrier === 'refresh') { useChatStore.getState().enterHistoryReading(SID); await rest(sources, 'refresh') }
    }
    segment(A, withTool ? 'First.\n\nSecond.' : 'First.Second.', withTool ? [A, T, B] : [A, B], withTool ? [pos(1), pos(2), pos(3)] : [pos(1), pos(3)], withTool ? ['s11-call'] : [], withTool ? [6] : [], [B])
  })
}
it.each(['REST', 'snapshot'] as const)('T75 B92: %s normal-stop tool-only segment is one earliest-tool row', async carrier => {
  const sources = [empty(1, 's11-E1'), tool(2, 's11-T1', 's11-c1'), empty(3, 's11-E2'), tool(4, 's11-T2', 's11-c2')]
  if (carrier === 'REST') await rest(sources); else native(sources)
  segment('s11-T1', '', ['s11-T1', 's11-T2'], [pos(2), pos(4)], ['s11-c1', 's11-c2'], [0, 0])
})
