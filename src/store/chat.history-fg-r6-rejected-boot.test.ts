// D7: rejected replay retains prior cursor/view and uses existing replay-error
// handling. Spec does not select reattach vs accepting the new boot here;
// direct user ruling requires the actual next frame NOT be silently dropped.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { replay } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup, open, socket, AVAILABLE, SID, BOOT, entry, page, snapshotAt, complete, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R6: rejecting a lower-sequence new-boot snapshot cannot silently discard the new turn live frame', () => {
  const prior = entry(1, { id: 'r6-prior', role: 'assistant', content: 'Old boot saved answer' })
  const initial = page([prior])
  // Real transport establishes old boot/100, not a directly seeded cursor.
  const prepared = open([prior], [replay(prior)])
  expect(bucket().history.historyLoaded).toBe(true)
  // The common real initial boot is boot-A for this scenario's comparison.
  const oldBoot = BOOT // explicitly supplied by the real initial snapshot
  expect(bucket().cursor).toStrictEqual({ bootId: oldBoot, seq: 100 })
  expect(prepared.entries).toStrictEqual(initial.entries)
  socket().attach()
  const attachCount = socket().socket.sent.filter(frame => frame.type === 'attach_session').length
  const bad = entry(2, { id: 'r6-invalid', role: 'assistant', content: 'Rejected history, not the live token' })
  const contradictory = page([bad], { rows: 2 })
  socket().deliver(
    { ...snapshotAt(contradictory), boot_id: 'r6-new-boot', seq: 3, reason: 'boot_mismatch' },
    { ...AVAILABLE, active_turn: { turn_id: 'r6-live-turn', agent_id: 'mia', started_at: '2026-10-21T10:00:00Z' } },
    replay(bad), { type: 'done', session_id: SID },
    { ...complete(), boot_id: 'r6-new-boot', seq: 3 },
  )
  expect(bucket().history.requests.latest.status, 'instrument: semantic contradiction rejected the page').toBe('error')
  expect(bucket().cursor, 'D7 old committed cut remains protected despite failure').toStrictEqual({ bootId: oldBoot, seq: 100 })
  expect(bucket().awaitingCatchUp).toBe(false)
  expect(bucket().history.liveControlsReady).toBe(true)
  socket().deliver(
    { type: 'token', session_id: SID, turn_id: 'r6-live-turn', agent_id: 'mia', message_id: 'r6-new-live', content: 'Unique new-boot live output', seq: 4 },
    { type: 'done', session_id: SID, turn_id: 'r6-live-turn', seq: 5 },
  )
  const actualApplied = bucket().messagesById['r6-new-live']?.content === 'Unique new-boot live output'
  const recoveryInitiated = socket().socket.sent.filter(frame => frame.type === 'attach_session').length > attachCount
  expect(actualApplied || recoveryInitiated, 'D7: accept the actual new-boot live frame OR initiate real reattach; never silently drop it against old-boot seq100').toBe(true)
})
