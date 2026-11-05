// 6c UAT regression oracles: chat-history-paging-spec AC-D1-6, S11b/c,
// B88/T71 and B93/T77; chief's 2026-11-02 08:23Z requirements (c)/(d).
// No production reducer is mocked. Rendered row/part expectations are literal.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { splitMessageParts } from '@/lib/messageParts'
import { cleanup, reset } from '../../tests/chat-history-paging/final-gate-harness'
import { entry } from '../../tests/chat-history-paging/fixtures'
import {
  A, B, SID, TURN, assistantRows, begin, bucket, done, native, prose,
  rest, snapshotStart, socket, token,
} from '../../tests/chat-history-paging/s11-fold-harness'

beforeEach(() => { reset(); begin() })
afterEach(cleanup)

// The same part splitter is consumed by the real chat renderers. Only project
// its observable text/card values; no private junction/ownership flags decide
// whether this assertion passes. All these fixtures contain drawn rows only.
function renderedTranscriptRows() {
  const current = bucket()
  return current.messageOrder.map(id => current.messagesById[id])
    .filter(row => row.role === 'user' || (row.role === 'assistant' && (row.content || row.tool_calls?.length)))
    .map(row => ({
      id: row.id,
      role: row.role,
      parts: splitMessageParts(row.content, row.tool_calls ?? []).map(part => part.type === 'text'
        ? { type: 'text', text: part.text }
        : { type: 'tool', id: part.call.id, tool: part.call.tool, status: part.call.status, textOffset: part.call.textOffset }),
    }))
}

it.each(['before-catch-up-complete', 'after-catch-up-complete'] as const)(
  'U-D1-4 T71 B88: %s untagged tool start with a [user,A] snapshot stays one A-keyed row',
  arrival => {
    // Real mia-turn-14 shape: no durable tool source exists while bash runs.
    // The independent B88/S11b oracle is A, bash at A.length, then "\n\n"+B.
    const sources = [entry(1, { id: 'uat6c-user', content: 'Write text, run bash, then continue.' }), prose(2, A, 'First.')]
    const deliveredFrom = socket().frames.length
    const commit = snapshotStart(sources, true)
    const start = () => socket().deliver({
      type: 'tool_call_start', session_id: SID, call_id: 'uat6c-live-bash',
      tool: 'bash', agent_id: 'mia', params: { command: 'sleep 30' },
      // Deliberately no turn_id/history_origin: this is the real live shape.
    })
    if (arrival === 'before-catch-up-complete') { start(); commit() }
    else { commit(); start() }

    const delivered = socket().frames.slice(deliveredFrom)
    const startIndex = delivered.findIndex(frame => frame.type === 'tool_call_start' && frame.call_id === 'uat6c-live-bash')
    const completeIndex = delivered.findIndex(frame => frame.type === 'catch_up_complete')
    expect(startIndex, 'instrument: actual untagged tool start reached the real reducer').toBeGreaterThanOrEqual(0)
    expect(completeIndex, 'instrument: the native snapshot boundary was actually delivered').toBeGreaterThanOrEqual(0)
    expect(startIndex < completeIndex, 'instrument: exercised the named tool-start/commit arrival order').toBe(arrival === 'before-catch-up-complete')
    expect(delivered.find(frame => frame.type === 'session_state' && frame.active_turn?.turn_id === TURN),
      'instrument: current state identifies this exact active turn').toBeDefined()
    expect.soft(assistantRows().map(row => row.id), 'B88: one A-keyed row even while the untagged tool is running').toEqual([A])

    socket().deliver({ type: 'tool_call_result', session_id: SID, call_id: 'uat6c-live-bash', tool: 'bash', status: 'success', result: { text: 'slept' } })
    token(B, 'Second.')
    expect.soft(assistantRows().map(row => row.id), 'B88: B extends A rather than an unstamped second row').toEqual([A])
    done()
    expect(renderedTranscriptRows(), 'S11: exactly one assistant row, with A then bash then B, keyed to A').toEqual([
      { id: 'uat6c-user', role: 'user', parts: [{ type: 'text', text: 'Write text, run bash, then continue.' }] },
      { id: A, role: 'assistant', parts: [
        { type: 'text', text: 'First.' },
        { type: 'tool', id: 'uat6c-live-bash', tool: 'bash', status: 'success', textOffset: 6 },
        { type: 'text', text: '\n\nSecond.' },
      ] },
    ])
  },
)

// Captured mia-turn-16 entries 67–70, from the saved triage receipt. That
// receipt contains ID/text PREFIXES only; these labels preserve exactly those
// known values, not invented UUID or prose suffixes. Legacy order is A,U,T,B.
const SAVED_A = 'b7745508-d', STEER = '23b864e5-8', CALL = 'call_ffb6d', SAVED_B = '6746c6aa-0'
const A_TEXT = 'First sentence:\n\nA river begins as a rumour of water high in'
const B_TEXT = 'The bash step is verified: `sleep 30 && echo "slept 30s; exi'
function savedMiaTurn16(correctedNewHistory: boolean) {
  const a = prose(67, SAVED_A, A_TEXT, { turn_id: 'mia-turn-16', timestamp: '2026-11-02T04:36:48.369498154Z' })
  const steer = entry(correctedNewHistory ? 69 : 68, { id: STEER, content: 'also add a third sentence', timestamp: '2026-11-02T04:36:52.508030335Z' })
  const tool = prose(correctedNewHistory ? 68 : 69, CALL, '', {
    type: 'tool_call', turn_id: 'mia-turn-16', timestamp: '2026-11-02T04:37:19.773894247Z',
    tool_calls: [{ id: CALL, tool: 'bash', status: 'success', parameters: { command: 'sleep 30 && echo "slept 30s; exit code 0"' }, content_state: 'full' }],
  })
  const b = prose(70, SAVED_B, B_TEXT, { turn_id: 'mia-turn-16', timestamp: '2026-11-02T04:37:22.429815648Z' })
  // New dispatch-time records precede the steer by acceptance position. Do
  // not repair old archives by sorting on timestamps or borrowing a turn.
  return correctedNewHistory ? [a, tool, steer, b] : [a, steer, tool, b]
}

it.each(['REST', 'snapshot'] as const)('U-D1-6 (c) legacy history loads unchanged through %s: pin mia-turn-16 A,steer,tool,B rendering', async carrier => {
  // Explicit compatibility/characterization oracle authorized by chief (c):
  // old completed histories keep their recorded order, even though new writes
  // must prevent that order. Never change this to the corrected (d) fixture.
  const sources = savedMiaTurn16(false)
  if (carrier === 'REST') await rest(sources)
  else native(sources)
  expect(renderedTranscriptRows(), 'legacy archive is neither reordered nor rejected').toEqual([
    { id: SAVED_A, role: 'assistant', parts: [{ type: 'text', text: A_TEXT }] },
    { id: STEER, role: 'user', parts: [{ type: 'text', text: 'also add a third sentence' }] },
    { id: SAVED_B, role: 'assistant', parts: [
      { type: 'tool', id: CALL, tool: 'bash', status: 'success', textOffset: 0 },
      { type: 'text', text: B_TEXT },
    ] },
  ])
})

it.each(['REST', 'snapshot'] as const)('U-D1-6 (d) corrected new history through %s renders [A+bash], steer, B after reload', async carrier => {
  const sources = savedMiaTurn16(true)
  if (carrier === 'REST') await rest(sources)
  else native(sources)
  expect(renderedTranscriptRows(), 'B93: bash stays above the steer and B stays separate').toEqual([
    { id: SAVED_A, role: 'assistant', parts: [
      { type: 'text', text: A_TEXT },
      { type: 'tool', id: CALL, tool: 'bash', status: 'success', textOffset: A_TEXT.length },
    ] },
    { id: STEER, role: 'user', parts: [{ type: 'text', text: 'also add a third sentence' }] },
    { id: SAVED_B, role: 'assistant', parts: [{ type: 'text', text: B_TEXT }] },
  ])
})
