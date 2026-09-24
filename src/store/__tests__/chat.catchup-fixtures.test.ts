// chat.catchup-fixtures.test.ts: BE-DESIGN.md §8.2 — feeds each catch-up
// fixture (src/store/__fixtures__/catchup/F1..F8.json) through the REAL
// useChatStore.handleFrame, in order, and asserts the final visible
// transcript against the fixture's own `expect` block — which is written
// from the SCENARIO DEFINITION, not derived from the reducer's own output.
// This is the oracle-independence rule §8.2 requires.
//
// Fixture provenance: F1.json..F8.json are Lane A's REAL gateway-recorded
// fixtures (pkg/gateway/catchup_fixtures_test.go, committed in Lane A's
// `1889e1aa5`, SQUAD-REPORT-BEA.md's "Opus pass") — not hand-derived. Each
// file's `tabs[]` holds every frame (both directions) one simulated
// connection saw, in order, plus `note` events marking what the test did
// between frames (drop, reload, chat switch). This driver only feeds the
// `server→client` frames into handleFrame — the `client→server` frames
// (attach_session, message) describe what the SPA itself sends, which the
// dedicated session.attach-cursor.test.ts / ws.reattach.test.ts already
// cover directly.

import { describe, it, expect, beforeEach } from 'vitest'
import { useChatStore } from '../chat/store'
import { useSessionStore } from '../session'
import type { WsReceiveFrame } from '@/lib/ws'
import f1 from '../__fixtures__/catchup/F1.json'
import f2 from '../__fixtures__/catchup/F2.json'
import f3 from '../__fixtures__/catchup/F3.json'
import f4 from '../__fixtures__/catchup/F4.json'
import f5 from '../__fixtures__/catchup/F5.json'
import f6 from '../__fixtures__/catchup/F6.json'
import f7 from '../__fixtures__/catchup/F7.json'
import f8 from '../__fixtures__/catchup/F8.json'

interface FixtureEvent {
  dir: 'client→server' | 'server→client' | 'note'
  frame?: Record<string, unknown>
  note?: string
}
interface FixtureTab {
  name: string
  events: FixtureEvent[]
}
interface Fixture {
  scenario: string
  title: string
  description: string
  expect: Record<string, unknown>
  tabs: FixtureTab[]
}

/** Every server→client frame across ALL tabs, in file order — this is what
 * ONE simulated browser tab receives across its whole lifetime, including
 * any reconnect segments (fixtures split a reconnect into a second `tabs[]`
 * entry, e.g. "A" then "A (reconnected)" — concatenating them in file order
 * reproduces exactly what a real, persistent Zustand store would see across
 * that reconnect, since reconnecting does not reset the store). */
function allServerFrames(fixture: Fixture): WsReceiveFrame[] {
  const frames: WsReceiveFrame[] = []
  for (const tab of fixture.tabs) {
    for (const event of tab.events) {
      if (event.dir === 'server→client' && event.frame) {
        frames.push(event.frame as unknown as WsReceiveFrame)
      }
    }
  }
  return frames
}

/** Drives ONE fresh store instance through every server→client frame in
 * `frames`, in order. */
function driveFrames(frames: WsReceiveFrame[]): void {
  useChatStore.setState({ sessionsById: {} } as never)
  for (const frame of frames) {
    useChatStore.getState().handleFrame(frame)
  }
}

function bucket(sessionId: string) {
  return useChatStore.getState().sessionsById[sessionId]
}

function assistantMessages(sessionId: string) {
  const b = bucket(sessionId)
  if (!b) return []
  return b.messageOrder.map((id) => b.messagesById[id]).filter((m) => m.role === 'assistant')
}

function userMessages(sessionId: string) {
  const b = bucket(sessionId)
  if (!b) return []
  return b.messageOrder.map((id) => b.messagesById[id]).filter((m) => m.role === 'user')
}

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: null })
  useChatStore.setState({ sessionsById: {} } as never)
})

describe('F1 — live turn, one tab, no reconnect (baseline shape)', () => {
  it('produces exactly the transcript the scenario defines', () => {
    const fixture = f1 as Fixture
    driveFrames(allServerFrames(fixture))
    const SID = 'sess-1'

    const users = userMessages(SID)
    expect(users).toHaveLength(1)
    expect(users[0].content).toBe(fixture.expect.user_message)

    const asst = assistantMessages(SID)
    // BE-DESIGN.md §6.3/§8.3 (corrected — a prior pass of this test wrongly
    // asserted two separate bubbles, adapting the oracle to that pass's own
    // bug rather than the design; caught by the orchestrator's Opus review):
    // "exactly one bubble per turn". F1's two message_ids (msg-1 "Let me
    // check." + tool call, msg-2 "All done.") merge onto the SAME bubble via
    // turn_id — the fixture's own `assistant_messages` array is the turn's
    // successive text SEGMENTS, concatenated into that one bubble's content,
    // not two separate bubbles' contents.
    expect(asst).toHaveLength(1)
    expect(asst[0].content).toBe((fixture.expect.assistant_messages as string[]).join(''))
    expect(asst[0].status === 'done' && !asst[0].isStreaming).toBe(true)

    const allCalls = (asst[0].tool_calls ?? []).map((tc) => tc.tool)
    expect(allCalls).toEqual(fixture.expect.tool_calls)

    expect(bucket(SID)?.isStreaming).toBe(false)
  })
})

describe('F2 — reconnect, incremental catch-up (cursor servable)', () => {
  it('the catch-up tail lands on the SAME bubble as the live continuation — no duplicate, one bubble', () => {
    const fixture = f2 as Fixture
    driveFrames(allServerFrames(fixture))
    const SID = 'sess-1'

    const asst = assistantMessages(SID)
    expect(asst.map((m) => m.content)).toEqual(fixture.expect.assistant_messages)
    expect(asst[0].status).toBe('done')
    expect(bucket(SID)?.isStreaming).toBe(false)
  })
})

describe('F3 — reload mid-message: snapshot with the active-turn projection', () => {
  it('the in-flight answer survives the snapshot via the projection, with no duplication', () => {
    const fixture = f3 as Fixture
    driveFrames(allServerFrames(fixture))
    const SID = 'sess-1'

    const asst = assistantMessages(SID)
    expect(asst.map((m) => m.content)).toEqual(fixture.expect.assistant_messages)
    expect(asst[0].status).toBe('done')
    expect(bucket(SID)?.isStreaming).toBe(false)
    expect(bucket(SID)?.awaitingCatchUp).toBe(false)
  })
})

describe('F4 — snapshot where the answer is persisted between the bind and the transcript read', () => {
  it('no duplicate text regardless of the persist/read race (§4.2 overlap rule)', () => {
    const fixture = f4 as Fixture
    driveFrames(allServerFrames(fixture))
    const SID = 'sess-1'

    const asst = assistantMessages(SID)
    expect(asst.map((m) => m.content)).toEqual(fixture.expect.assistant_messages)
    expect(bucket(SID)?.isStreaming).toBe(false)
  })
})

describe('F5 — two tabs on one chat', () => {
  it('both tabs converge on byte-identical messages from the SAME frame stream', () => {
    const fixture = f5 as Fixture
    const [tabA, tabB] = fixture.tabs
    expect(tabA.name).toBe('A')
    expect(tabB.name).toBe('B')
    const SID = 'sess-1'

    const framesFor = (tab: FixtureTab) =>
      tab.events.filter((e) => e.dir === 'server→client' && e.frame).map((e) => e.frame as unknown as WsReceiveFrame)

    driveFrames(framesFor(tabA))
    const tabAContent = assistantMessages(SID).map((m) => m.content)
    const tabACursor = bucket(SID)?.cursor

    driveFrames(framesFor(tabB))
    const tabBContent = assistantMessages(SID).map((m) => m.content)
    const tabBCursor = bucket(SID)?.cursor

    expect(tabAContent).toEqual(fixture.expect.assistant_messages)
    expect(tabBContent).toEqual(tabAContent)
    expect(tabBCursor).toEqual(tabACursor)
  })
})

describe('F6 — switch to another chat while the answer finishes, then switch back', () => {
  it('the original chat reassembles into ONE bubble across the switch-away, and the other chat is never touched by it', () => {
    const fixture = f6 as Fixture
    driveFrames(allServerFrames(fixture))
    const SID_A = 'sess-1'
    const SID_B = 'sess-2'

    const asstA = assistantMessages(SID_A)
    expect(asstA.map((m) => m.content)).toEqual(fixture.expect.assistant_messages)
    expect(asstA).toHaveLength(1) // never split by the switch-away/back
    expect(bucket(SID_A)?.isStreaming).toBe(false)

    // The other chat's own snapshot/replay never touched session A's bucket.
    const bB = bucket(SID_B)!
    expect(bB.messageOrder.length).toBeGreaterThan(0)
    expect(assistantMessages(SID_A).map((m) => m.content)).toEqual(fixture.expect.assistant_messages)
  })
})

describe('F7 — gateway restart: the cursor\'s boot id no longer matches', () => {
  it('boot_mismatch forces a snapshot; no turn is left announced as active', () => {
    const fixture = f7 as Fixture
    driveFrames(allServerFrames(fixture))
    const SID = 'sess-1'

    expect(bucket(SID)?.activeTurnId).toBeNull()
    expect(bucket(SID)?.awaitingCatchUp).toBe(false)
    const users = userMessages(SID)
    expect(users.some((m) => m.content === fixture.expect.user_message)).toBe(true)
  })
})

describe('F8 — message typed while offline', () => {
  it('the offline message appears at the end, then its own answer, with no duplicate', () => {
    const fixture = f8 as Fixture
    driveFrames(allServerFrames(fixture))
    const SID = 'sess-1'

    const users = userMessages(SID)
    expect(users.map((m) => m.content)).toEqual(fixture.expect.user_messages)

    const asst = assistantMessages(SID)
    expect(asst.map((m) => m.content)).toEqual(fixture.expect.assistant_messages)
    expect(bucket(SID)?.isStreaming).toBe(false)
  })
})
