// F2 (final-gate architect): a REJECTED cross-boot snapshot must not silently
// drop the new boot's live frames. Spec D7: a failed replacement keeps the
// prior cursor (it never claims the rejected cut) AND the reader recovers.
// Either recovery is acceptable: the new boot's live text is applied exactly
// once, or ONE real attach_session is sent after the rejection. Silence is not.
// Real useChatStore.handleFrame and generated-valid frames; only the socket
// send is a stand-in.
import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { gapReattachRetryAttempts, gapReattachRetryTimers, inFlightReattachSids, replayErrorRetryAttempts, replayErrorRetryTimers } from './runtime-state'
import type { ServerFrame } from '@/lib/ws'
import { entry, expectValidFrame, expectValidPage, page, pos, replay, snapshot } from '../../../tests/chat-history-paging/fixtures'

const SID = 'session-crossboot-rejected'
let sent: { type?: string }[] = []

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  sent = []
  useConnectionStore.setState({ connection: { send: (frame: { type?: string }) => { sent.push(frame); return true } } } as never)
  inFlightReattachSids.clear()
  for (const sid of Object.keys(replayErrorRetryAttempts)) delete replayErrorRetryAttempts[sid]
  for (const sid of Object.keys(replayErrorRetryTimers)) { clearTimeout(replayErrorRetryTimers[sid]); delete replayErrorRetryTimers[sid] }
  for (const sid of Object.keys(gapReattachRetryAttempts)) delete gapReattachRetryAttempts[sid]
  for (const sid of Object.keys(gapReattachRetryTimers)) { clearTimeout(gapReattachRetryTimers[sid]); delete gapReattachRetryTimers[sid] }
})

const bucket = () => useChatStore.getState().sessionsById[SID]
const feed = (frames: ServerFrame[]) => {
  frames.forEach(expectValidFrame)
  frames.forEach(frame => useChatStore.getState().handleFrame(frame))
}
const current: ServerFrame = { type: 'session_state', session_id: SID, user_id: 'f2-reader', pending_approvals: [],
  emitted_at: '2026-10-26T09:00:00Z', history_state: { status: 'available' } }

/** A semantically contradictory page: one narration source draws one row,
 * but its metadata promises two, so the real replay owner rejects it. */
function rejectedSnapshot(seq: number, id: string): ServerFrame[] {
  const replacement = entry(2, { id, role: 'assistant', content: 'Must never publish.', agent_id: 'agent-1' })
  const contradictory = page([replacement], { sessionId: SID, rows: 2 })
  return [
    { ...snapshot(contradictory), seq, boot_id: 'boot-new' }, current, replay(replacement, SID),
    { type: 'done', session_id: SID, stats: { frames_emitted: 1 } },
    { type: 'catch_up_complete', session_id: SID, seq, boot_id: 'boot-new', mode: 'snapshot' },
  ]
}
function burst(seqs: number[], messageId: string, text: string): ServerFrame[] {
  return seqs.map((seq, index) => ({ type: 'token', session_id: SID, content: text[index], turn_id: `turn-${messageId}`, message_id: messageId, seq }) as ServerFrame)
}
const attaches = () => sent.filter(frame => frame.type === 'attach_session')
/** Applied exactly once: one live row carries the whole burst text, once. */
const appliedOnce = (text: string) => Object.values(bucket().messagesById).filter(row => row.content?.includes(text)).length === 1
  && Object.values(bucket().messagesById).every(row => !row.content?.includes(text + text))

describe('F2: rejected cross-boot snapshot, then live frames of the new boot', () => {
  it('recovers every new-boot burst exactly once, keeps the failure visible and never loops without traffic', () => {
    const old = entry(1, { id: 'f2-old-reply', role: 'assistant', content: 'Previously committed reply.', agent_id: 'agent-1' })
    const initial = page([old], { sessionId: SID })
    expectValidPage(initial, [pos(1)], 1)
    feed([
      { ...snapshot(initial), seq: 100, boot_id: 'boot-prior' }, current, replay(old, SID),
      { type: 'done', session_id: SID, stats: { frames_emitted: 1 } },
      { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'boot-prior', mode: 'snapshot' },
    ])
    expect(bucket().cursor, 'instrument: the prior boot cut is committed').toStrictEqual({ bootId: 'boot-prior', seq: 100 })

    // The gateway restarts; its first snapshot is rejected by real validation.
    feed(rejectedSnapshot(3, 'f2-replacement-1'))
    expect(bucket().history.requests.latest.status, 'positive control: the rejection is a visible history failure').toBe('error')
    expect(bucket().cursor, 'D7: the rejected cut is never claimed').toMatchObject({ bootId: 'boot-prior', seq: 100 })
    expect(bucket().awaitingCatchUp, 'current controls completed').toBe(false)
    sent = []
    expect(attaches(), 'no traffic, no attach: the rejection itself starts no loop').toHaveLength(0)

    // First live burst of the NEW boot: seq 4..6 continue the rejected cut.
    feed(burst([4, 5, 6], 'live-new-1', 'ABC'))
    const firstRecovered = appliedOnce('ABC') ? 'applied' : attaches().length === 1 ? 'reattached' : 'dropped'
    expect(firstRecovered, 'the new boot burst is applied once or answered by ONE real re-attach, never silently dropped').not.toBe('dropped')
    expect(attaches().length, 'one attach per burst, not one per frame').toBeLessThanOrEqual(1)

    // The server answers with another snapshot that is rejected again.
    sent = []
    feed(rejectedSnapshot(6, 'f2-replacement-2'))
    expect(bucket().history.requests.latest.status, 'repeated rejection stays visible').toBe('error')
    expect(bucket().history.historyLoaded, 'no empty success after a repeated rejection').toBe(false)
    expect(bucket().messagesById['f2-old-reply'], 'D7: the prior published reader survives both rejections').toBeDefined()
    expect(attaches(), 'the repeated rejection itself starts no attach').toHaveLength(0)

    // Second burst: again recovered, again at most one attach for the burst.
    feed(burst([7, 8], 'live-new-2', 'DE'))
    const secondRecovered = appliedOnce('DE') ? 'applied' : attaches().length === 1 ? 'reattached' : 'dropped'
    expect(secondRecovered, 'the next burst after a repeated rejection is recovered too').not.toBe('dropped')
    expect(attaches().length, 'one attach per burst after a repeated rejection').toBeLessThanOrEqual(1)
  })
})
