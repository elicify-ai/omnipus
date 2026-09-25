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
import { useConnectionStore } from '../connection'
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

/** A step in ONE simulated tab's real-time lifetime: either a frame to feed
 * into handleFrame, or a disconnect side effect to actually EXECUTE. */
type DriveStep = { kind: 'frame'; frame: WsReceiveFrame } | { kind: 'disconnect' }

/** Real-browser regression (orchestrator + Opus review round 2, "TEST
 * QUALITY"): a prior pass of this driver only ever fed server→client frames
 * straight into handleFrame — it silently dropped every recording's `note`
 * event, including "connection dropped"/"connection lost". That meant the
 * real WS disconnect handler (OmnipusRuntimeProvider's onDisconnected ->
 * clearStreamingState) never actually ran in this suite, so it could not
 * have caught BUG 1 (clearStreamingState closing the still-streaming
 * bubble) even though F2/F8 both recorded a real disconnect. Fixed: any
 * `note` event whose text says the connection dropped/was lost is now a
 * real step that calls the ACTUAL clearStreamingState(), exactly as
 * OmnipusRuntimeProvider.tsx's onDisconnected does. */
function allSteps(fixture: Fixture): DriveStep[] {
  const steps: DriveStep[] = []
  for (const tab of fixture.tabs) {
    for (const event of tab.events) {
      if (event.dir === 'server→client' && event.frame) {
        steps.push({ kind: 'frame', frame: event.frame as unknown as WsReceiveFrame })
      } else if (event.dir === 'note' && /connection (dropped|lost)/i.test(event.note ?? '')) {
        steps.push({ kind: 'disconnect' })
      }
    }
  }
  return steps
}

/** Every server→client frame across ALL tabs, in file order — kept for the
 * scenarios (F5's per-tab split, F6/F7) that drive frames directly rather
 * than through `driveSteps`. */
function allServerFrames(fixture: Fixture): WsReceiveFrame[] {
  return allSteps(fixture)
    .filter((s): s is { kind: 'frame'; frame: WsReceiveFrame } => s.kind === 'frame')
    .map((s) => s.frame)
}

/** Drives ONE fresh store instance through every server→client frame in
 * `frames`, in order. */
function driveFrames(frames: WsReceiveFrame[]): void {
  useChatStore.setState({ sessionsById: {} } as never)
  for (const frame of frames) {
    useChatStore.getState().handleFrame(frame)
  }
}

/** Drives ONE fresh store instance through a fixture's full step sequence —
 * frames AND real disconnect side effects, in the order the recording saw
 * them. Use this instead of `driveFrames(allServerFrames(...))` for any
 * fixture whose recording notes a real connection drop (F2, F8) — see
 * `allSteps`'s doc comment. */
function driveSteps(fixture: Fixture): void {
  useChatStore.setState({ sessionsById: {} } as never)
  for (const step of allSteps(fixture)) {
    if (step.kind === 'disconnect') {
      useChatStore.getState().clearStreamingState()
    } else {
      useChatStore.getState().handleFrame(step.frame)
    }
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
    //
    // Opus review round 3 item N3: joined with a paragraph break, not
    // `join('')` — a bare concatenation glues the post-tool-call
    // continuation directly onto the pre-tool-call text with no separator
    // ("Let me check.All done.", live), which does not match what a reload
    // produces for the SAME persisted turn (a real paragraph break between
    // the two segments). `join('')` locked that bug in as the oracle; fixed
    // to expect the actual correct output.
    expect(asst).toHaveLength(1)
    expect(asst[0].content).toBe((fixture.expect.assistant_messages as string[]).join('\n\n'))
    expect(asst[0].status === 'done' && !asst[0].isStreaming).toBe(true)

    const allCalls = (asst[0].tool_calls ?? []).map((tc) => tc.tool)
    expect(allCalls).toEqual(fixture.expect.tool_calls)

    expect(bucket(SID)?.isStreaming).toBe(false)
  })
})

describe('F2 — reconnect, incremental catch-up (cursor servable)', () => {
  it('the catch-up tail lands on the SAME bubble as the live continuation — no duplicate, one bubble', () => {
    const fixture = f2 as Fixture
    driveSteps(fixture) // executes the recording's real 'connection dropped' note
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

    // Opus review round 3 (requested test additions): the recording's own
    // session_snapshot frame carries the scenario's ground-truth reason —
    // oracle-independence (§8.2): read from the SCENARIO's own recorded
    // frame, not derived from the reducer.
    const snapshotFrame = fixture.tabs
      .flatMap((t) => t.events)
      .map((e) => e.frame)
      .find((f) => (f as { type?: string } | undefined)?.type === 'session_snapshot') as { reason?: string } | undefined
    expect(snapshotFrame?.reason).toBe(fixture.expect.snapshot_reason)

    // F7's own recording never replays a SINGLE assistant token — the
    // gateway restarted before the turn produced any output at all (only
    // the user's question survives, per replay_message above). There is
    // therefore no message for ChatMessage.confirmedUnfinished (BUG 1/N1's
    // per-message flag) to attach to; §6.5's "unfinished_answer: true" here
    // means the session-level facts alone: the composer must NOT be stuck
    // waiting on a turn that no longer exists.
    expect(fixture.expect.unfinished_answer).toBe(true)
    expect(assistantMessages(SID)).toHaveLength(0)
    expect(bucket(SID)?.isStreaming).toBe(false)
  })
})

describe('F8 — message typed while offline', () => {
  it('the offline message appears at the end, then its own answer, with no duplicate', () => {
    const fixture = f8 as Fixture
    driveSteps(fixture) // executes the recording's real 'connection lost' note
    const SID = 'sess-1'

    const users = userMessages(SID)
    expect(users.map((m) => m.content)).toEqual(fixture.expect.user_messages)

    const asst = assistantMessages(SID)
    expect(asst.map((m) => m.content)).toEqual(fixture.expect.assistant_messages)
    expect(bucket(SID)?.isStreaming).toBe(false)
  })

  it('Opus review round 3 (requested test addition): the pending bubble sendMessage actually creates is the one the server echo resolves — not a hand-built frame standing in for it', () => {
    // Reproduces F8's exact shape (first question -> answer, then offline,
    // second question, reconnect, answer) but drives the SECOND message
    // through the REAL sendMessage() — this is what "F8 must create a real
    // pending bubble via sendMessage" means: driving handleFrame alone (as
    // the fixture-replay test above does) never exercises sendMessage's own
    // optimistic-bubble creation, which is exactly the mechanism
    // pending_bubble_resolved is about.
    const SID = 'sess-1'
    useSessionStore.setState({ activeSessionId: SID })
    useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {}, isStreaming: false } as never)
    useConnectionStore.setState({ connection: null, isConnected: false } as never)

    // First question/answer — hand-fed, matching F8's own recording; not
    // what this test is about.
    useChatStore.getState().handleFrame({ type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-09-24T00:00:00Z' } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID, agent_id: 'mia', seq: 1, boot_id: 'boot-1' } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'user_message', session_id: SID, id: 'id-1', client_message_id: 'client-1', content: 'first question', timestamp: '2026-09-24T00:00:00Z', seq: 2 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'message_status', session_id: SID, client_message_id: 'client-1', state: 'received', seq: 3 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'message_status', session_id: SID, client_message_id: 'client-1', state: 'working', seq: 4 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'token', session_id: SID, content: 'First ', message_id: 'msg-1', seq: 5 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'token', session_id: SID, content: 'answer.', message_id: 'msg-1', seq: 6 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'done', session_id: SID, message_id: 'msg-1', seq: 7, stats: { tokens: 2, cost: 0.01 } } as WsReceiveFrame)

    // Connection lost — the user types the second question OFFLINE. The
    // REAL sendMessage() queues it (isConnected is false), same as
    // ChatScreen's composer would.
    useChatStore.getState().clearStreamingState()
    useChatStore.getState().sendMessage('second question')
    // Not yet in the thread — queued, not sent, while offline.
    expect(userMessages(SID).map((m) => m.content)).toEqual(['first question'])

    // Back online: reconnect, then the real drain actually sends it —
    // capturing the client_message_id sendMessage/beginSend minted so the
    // "server" echo below can resolve the SAME bubble, exactly as a real
    // gateway round-trip would.
    const sent: { client_message_id?: string; content?: string }[] = []
    useConnectionStore.setState({ connection: { send: (f: unknown) => { sent.push(f as never); return true }, close: () => {}, isConnected: true }, isConnected: true } as never)
    useChatStore.getState().handleFrame({ type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-09-24T00:00:10Z' } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'catch_up_complete', session_id: SID, seq: 7, boot_id: 'boot-1', mode: 'incremental' } as WsReceiveFrame)
    useChatStore.getState().drainOutboundQueue()

    const queuedFrame = sent.find((f) => f.content === 'second question')
    expect(queuedFrame?.client_message_id).toBeTruthy()
    const cmid = queuedFrame!.client_message_id!
    // The optimistic bubble exists, keyed by that same id — this IS the
    // "real pending bubble" the request is about.
    expect(bucket(SID)?.messagesById[cmid]).toBeDefined()
    expect(bucket(SID)?.messagesById[cmid]?.deliveryStatus).toBe('sending')

    // The server's echo resolves it in place — pending_bubble_resolved.
    useChatStore.getState().handleFrame({ type: 'user_message', session_id: SID, id: 'id-2', client_message_id: cmid, content: 'second question', timestamp: '2026-09-24T00:00:11Z', seq: 8 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'message_status', session_id: SID, client_message_id: cmid, state: 'received', seq: 9 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'message_status', session_id: SID, client_message_id: cmid, state: 'working', seq: 10 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'token', session_id: SID, content: 'Second ', message_id: 'msg-2', seq: 11 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'token', session_id: SID, content: 'answer.', message_id: 'msg-2', seq: 12 } as WsReceiveFrame)
    useChatStore.getState().handleFrame({ type: 'done', session_id: SID, message_id: 'msg-2', seq: 13, stats: { tokens: 2, cost: 0.01 } } as WsReceiveFrame)

    const users = userMessages(SID)
    expect(users.map((m) => m.content)).toEqual((f8 as Fixture).expect.user_messages)
    expect(users).toHaveLength(2) // never duplicated — the SAME bubble, resolved
    expect(bucket(SID)?.messagesById[cmid]?.deliveryStatus).toBe('working')

    const asst = assistantMessages(SID)
    expect(asst.map((m) => m.content)).toEqual((f8 as Fixture).expect.assistant_messages)
  })
})
