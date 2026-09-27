// chat.subagent-replay-gap.test.ts — ADR-091 WP-E cross-family review finding 19
// (src/store/chat/slices/frames.ts::createFrameSlice.handleFrame, the
// subagent_message/subagent_state case arms).
//
// A replay gap is a legitimate ordering, not a bug: subagent_message and
// subagent_state are both session-scoped (I-4) and can arrive interleaved
// with other sessions' frames on reconnect, so a child's FIRST status update
// can be replayed before its own subagent_start. Before this fix, both
// reducers scanned for the span (index, then O(N)), found nothing either
// way, logged a "received for unknown span_id" warning, and discarded the
// update outright — the later subagent_start had nothing to apply, so the
// side panel row never showed the child's actual first status/state until
// its NEXT update (which may never come, e.g. a single-message child that
// goes straight from queued to completed).
//
// Fix: a bounded per-session pending-update map (SessionChatState.
// pendingSpanUpdatesBySpanId, keyed by span_id) captures the update when the
// span can't be found; subagent_start consults and clears it when the span
// is finally created.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const TEST_SESSION_ID = 'replay-gap-test-session'

function resetStores() {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({
      activeSessionId: TEST_SESSION_ID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('ChatStore — subagent_message/subagent_state arriving before subagent_start (finding 19)', () => {
  it('a subagent_message before its span\'s subagent_start is applied once the span is created', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst-gap-msg',
        role: 'assistant',
        content: 'Working...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
    })

    // The message arrives FIRST — no span exists yet for 'span-gap-msg'.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_message',
        span_id: 'span-gap-msg',
        message_id: 'm-gap-1',
        kind: 'progress',
        text: 'auditing the checkout page',
        sender_identity: 'agent-child',
        untrusted_origin: false,
        created_at: '2026-01-01T00:00:01.000Z',
        session_id: TEST_SESSION_ID,
      })
    })

    // Before the fix: dropped here — nothing for subagent_start to apply.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span-gap-msg',
        parent_call_id: 'call-gap-msg',
        task_label: 'audit',
        agent_id: 'agent-child',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const asstMsg = state.messages.find((m) => m.id === 'asst-gap-msg')
    const span = asstMsg!.spans!.find((s) => s.spanId === 'span-gap-msg')
    expect(span?.statusLine).toBe('auditing the checkout page')
    expect(span?.lastUpdateAt).toBe('2026-01-01T00:00:01.000Z')

    // The pending entry must be cleared once consumed — not left to leak
    // onto some future, unrelated span_id reuse.
    expect(state.sessionsById[TEST_SESSION_ID]?.pendingSpanUpdatesBySpanId?.['span-gap-msg']).toBeUndefined()
  })

  it('a subagent_state before its span\'s subagent_start is applied once the span is created', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst-gap-state',
        role: 'assistant',
        content: 'Working...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
    })

    // The state arrives FIRST — no span exists yet for 'span-gap-state'.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_state',
        span_id: 'span-gap-state',
        state: 'queued',
        created_at: '2026-01-01T00:00:02.000Z',
        session_id: TEST_SESSION_ID,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span-gap-state',
        parent_call_id: 'call-gap-state',
        task_label: 'audit',
        agent_id: 'agent-child',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const asstMsg = state.messages.find((m) => m.id === 'asst-gap-state')
    const span = asstMsg!.spans!.find((s) => s.spanId === 'span-gap-state')
    expect(span?.lifecycleState).toBe('queued')
    expect(span?.lastUpdateAt).toBe('2026-01-01T00:00:02.000Z')
    expect(state.sessionsById[TEST_SESSION_ID]?.pendingSpanUpdatesBySpanId?.['span-gap-state']).toBeUndefined()
  })

  it('both a subagent_message and a subagent_state before subagent_start are both applied', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst-gap-both',
        role: 'assistant',
        content: 'Working...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_message',
        span_id: 'span-gap-both',
        message_id: 'm-gap-both',
        kind: 'progress',
        text: 'first pass',
        sender_identity: 'agent-child',
        untrusted_origin: false,
        created_at: '2026-01-01T00:00:03.000Z',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'subagent_state',
        span_id: 'span-gap-both',
        state: 'needs_input',
        created_at: '2026-01-01T00:00:04.000Z',
        session_id: TEST_SESSION_ID,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span-gap-both',
        parent_call_id: 'call-gap-both',
        task_label: 'audit',
        agent_id: 'agent-child',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const asstMsg = state.messages.find((m) => m.id === 'asst-gap-both')
    const span = asstMsg!.spans!.find((s) => s.spanId === 'span-gap-both')
    expect(span?.statusLine).toBe('first pass')
    expect(span?.lifecycleState).toBe('needs_input')
    // The later of the two pending updates (subagent_state) is the most
    // recent event applied to this span.
    expect(span?.lastUpdateAt).toBe('2026-01-01T00:00:04.000Z')
  })

  it('a subagent_message for a DIFFERENT span_id that later gets its own start is unaffected by another pending entry', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst-gap-multi',
        role: 'assistant',
        content: 'Working...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_message',
        span_id: 'span-gap-multi-A',
        message_id: 'm-multi-a',
        kind: 'progress',
        text: 'A working',
        sender_identity: 'agent-a',
        untrusted_origin: false,
        created_at: '2026-01-01T00:00:05.000Z',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'subagent_message',
        span_id: 'span-gap-multi-B',
        message_id: 'm-multi-b',
        kind: 'progress',
        text: 'B working',
        sender_identity: 'agent-b',
        untrusted_origin: false,
        created_at: '2026-01-01T00:00:06.000Z',
        session_id: TEST_SESSION_ID,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span-gap-multi-B',
        parent_call_id: 'call-multi-b',
        task_label: 'audit B',
        agent_id: 'agent-b',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const asstMsg = state.messages.find((m) => m.id === 'asst-gap-multi')
    const spanB = asstMsg!.spans!.find((s) => s.spanId === 'span-gap-multi-B')
    expect(spanB?.statusLine).toBe('B working')
    // A's pending entry must still be sitting there, untouched, waiting for
    // A's own (not-yet-arrived) subagent_start.
    expect(state.sessionsById[TEST_SESSION_ID]?.pendingSpanUpdatesBySpanId?.['span-gap-multi-A']?.statusLine).toBe('A working')
  })
})

// #823/ADR-091 merge review (Opus, F4 — low, plausible race): the Go side's
// deliverSubagentStart persists the span to the session transcript BEFORE
// emitting the live subagent_start frame (not one atomic step). If a SECOND
// tab's attach_session binds in that exact window, its snapshot replay emits
// the SAME span (reconstructed from the transcript, unsequenced — no `seq`
// field at all) that the live emit ALSO delivers moments later (sequenced,
// `seq` above whatever the bucket's cursor already had) — one delegation,
// two subagent_start frames for the SAME span_id, racing each other. Before
// this fix, `case 'subagent_start'` (slices/frames.ts) had no span_id dedupe
// at all: the second frame pushed a SECOND SubagentSpanRunning onto the same
// message's `spans` array and overwrote `spanBySpanId[span_id]` to point at
// the new (duplicate) index — two cards in the UI for one real delegation,
// and any update racing in between the two starts (a subagent_message/
// subagent_state) would have been silently orphaned on the FIRST span's
// index once the second start clobbered the map.
describe('ChatStore — subagent_start dedup by span_id (#823/ADR-091 merge review F4)', () => {
  it('a live subagent_start for a span_id the bucket ALREADY has (from an earlier replayed start) is ignored — no second span, no state reset', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst-dup-start',
        role: 'assistant',
        content: 'Working...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
    })

    // 1. The REPLAYED copy — a session_snapshot's reconstruction of the
    // transcript, unsequenced (no `seq` field), exactly as replay.go emits it.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span-dup-race',
        parent_call_id: 'call-dup-race',
        task_label: 'audit',
        agent_id: 'agent-child',
        session_id: TEST_SESSION_ID,
      })
    })

    let state = useChatStore.getState()
    let asstMsg = state.messages.find((m) => m.id === 'asst-dup-start')
    expect(asstMsg!.spans!.filter((s) => s.spanId === 'span-dup-race')).toHaveLength(1)

    // 2. A live update lands on the (single, real) span in between the two
    // starts — this is what proves the eventual duplicate doesn't just fail
    // to ADD a second card, but also doesn't RESET the real one's state.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_message',
        span_id: 'span-dup-race',
        message_id: 'm-dup-race',
        kind: 'progress',
        text: 'auditing the checkout page',
        sender_identity: 'agent-child',
        untrusted_origin: false,
        created_at: '2026-01-01T00:00:07.000Z',
        session_id: TEST_SESSION_ID,
      })
    })

    // 3. The LIVE copy of the SAME start arrives after — sequenced, per
    // BE-DESIGN.md §1.2 (subagent_start carries an optional `seq`).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span-dup-race',
        parent_call_id: 'call-dup-race',
        task_label: 'audit',
        agent_id: 'agent-child',
        session_id: TEST_SESSION_ID,
        seq: 5,
      })
    })

    state = useChatStore.getState()
    asstMsg = state.messages.find((m) => m.id === 'asst-dup-start')
    const matches = asstMsg!.spans!.filter((s) => s.spanId === 'span-dup-race')
    // Exactly one span — the live duplicate must not have appended a second.
    expect(matches).toHaveLength(1)
    // The pending subagent_message's update survives the duplicate start —
    // the duplicate must not have reset the real span's already-live state.
    expect(matches[0].statusLine).toBe('auditing the checkout page')
    expect(matches[0].lastUpdateAt).toBe('2026-01-01T00:00:07.000Z')
    // The O(1) index still points at the ONE real span, at its original
    // index — a naive "push a second, then re-point the index" bug would
    // otherwise leave this pointing at the duplicate instead.
    const indexEntry = state.sessionsById[TEST_SESSION_ID]?.spanBySpanId?.['span-dup-race']
    expect(indexEntry?.messageId).toBe('asst-dup-start')
    expect(asstMsg!.spans![indexEntry!.spanIdx]?.spanId).toBe('span-dup-race')
  })
})
