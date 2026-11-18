// #1537 order 5a. Oracle: FR-014 and BDD Missed end during a disconnect.
// Real attach buffering + store + screen; no synthetic internal helper-map field.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup } from '@testing-library/react'
import { AttachReplayBuffer, isSupersededAttachFrame } from '@/lib/attach-replay'
import type { ServerFrame } from '@/lib/ws'
import { entry, page, snapshot, replay } from '../../../tests/chat-history-paging/fixtures'
import { reset, feed, retry, status, active, mount, named, bucket, SID } from '../../../tests/helper-model-notices/harness'

beforeEach(() => {
  reset()
  // Animation/tick scheduling is a browser-clock edge, not the attach reducer.
  // Own that clock so real component cleanup runs before its restoration.
  vi.useFakeTimers()
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.useRealTimers() })

const saved = entry(1, { id: 'saved-normal-answer', role: 'assistant', content: 'The normal saved answer.', agent_id: 'main' })
const history = page([saved], { sessionId: SID })
const complete = (mode = 'snapshot', boot = 'boot-old') => ({ type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: boot, mode })

function seed() {
  feed(snapshot(history), active(), replay(saved, SID), { type: 'done', session_id: SID, stats: { frames_emitted: 1 } }, complete())
  feed(active(), retry('', 'own-turn'), retry(), status('waiting', 'writer-chat', 'writer-turn', { agent_id: 'writer' }))
  expect(bucket().messagesById[saved.id]?.content, 'instrument: real saved history is loaded').toBe(saved.content)
}
function through(buffer: AttachReplayBuffer, frames: object[]) {
  for (const frame of frames) for (const ready of buffer.accept(frame as unknown as ServerFrame)) feed(ready as unknown as Record<string, unknown>)
}
function replacement(kind: string): object[] {
  const boot = kind === 'restart' ? 'boot-new' : 'boot-old'
  const state = kind === 'live_only' ? { ...active(), history_state: { status: 'unavailable', reason: 'index_missing', message: 'History unavailable.' } } : active()
  return [
    ...(kind === 'live_only' ? [] : [{ ...snapshot(history, kind === 'restart' ? 'boot_mismatch' : 'retention_exceeded'), boot_id: boot }]),
    state,
    ...(kind === 'live_only' ? [] : [replay(saved, SID), { type: 'done', session_id: SID, stats: { frames_emitted: 1 } }]),
    status('waiting', 'writer-chat', 'writer-turn', { agent_id: 'writer' }),
    complete(kind === 'live_only' ? 'live_only' : 'snapshot', boot),
  ]
}

it.each(['retention_exceeded', 'restart', 'live_only'])('successful %s attach replaces only named rows with the delivered projection', kind => {
  seed()
  const own = structuredClone(bucket().providerRetryEvent)
  const buffer = new AttachReplayBuffer()
  buffer.begin(SID)
  through(buffer, replacement(kind))
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying automatically'), 'FR-014: ended while disconnected, absent from projection').toHaveLength(0)
  expect(named(container, 'Writer', "hasn't responded"), 'FR-014: still-active row is retained').toHaveLength(1)
  expect(bucket().providerRetryEvent, 'helper reconciliation cannot wipe the own notice').toEqual(own)
  expect(bucket().messagesById[saved.id]?.content, 'normal history survives helper reconciliation').toBe(saved.content)
})

it('a held live helper frame after COMPLETE survives replacement', () => {
  seed()
  const buffer = new AttachReplayBuffer()
  buffer.begin(SID)
  through(buffer, [...replacement('retention_exceeded'), retry('new-child', 'new-turn')])
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying automatically'), 'only the new, post-completion helper is visible').toHaveLength(1)
  feed(status('ended', 'new-child', 'new-turn'))
  expect(named(container, 'Researcher', 'Retrying automatically'), 'the old Researcher row was not retained underneath it').toHaveLength(0)
  expect(named(container, 'Writer', "hasn't responded")).toHaveLength(1)
})

it('incremental attach removes only the replayed ended turn', () => {
  seed()
  const buffer = new AttachReplayBuffer()
  buffer.begin(SID)
  through(buffer, [active(), status('ended'), complete('incremental')])
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(0)
  expect(named(container, 'Writer', "hasn't responded")).toHaveLength(1)
})

it('a failed attach cannot clear or replace named rows', () => {
  seed()
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying automatically'), 'instrument: seed must render before testing non-change').toHaveLength(1)
  const own = structuredClone(bucket().providerRetryEvent)
  const buffer = new AttachReplayBuffer()
  buffer.begin(SID)
  through(buffer, [snapshot(history), active(), { type: 'done', session_id: SID, stats: { replay_error: true } }])
  expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(1)
  expect(named(container, 'Writer', "hasn't responded")).toHaveLength(1)
  expect(bucket().providerRetryEvent).toEqual(own)
})

it('superseded attach completion cannot clear current named rows', () => {
  seed()
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying automatically'), 'instrument: retained row exists').toHaveLength(1)
  const buffer = new AttachReplayBuffer()
  buffer.begin(SID)
  const older = replacement('retention_exceeded').flatMap(frame => buffer.accept(frame as unknown as ServerFrame))
  // A1 completed but its batch still awaits paint when A2 begins. The REAL
  // transport marks queued controls superseded; no fabricated wire flag.
  buffer.begin(SID, older)
  expect(isSupersededAttachFrame(older.at(-1))).toBe(true)
  feed(...older.map(frame => frame as unknown as Record<string, unknown>))
  expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(1)
  expect(named(container, 'Writer', "hasn't responded")).toHaveLength(1)
})
