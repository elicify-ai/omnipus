// Regression test for the delegator-narration/delegate-content text-glue bug
// found in live UAT, reproduced verbatim as:
//
//   "...Let me load the delegate tool and send the task now.Now delegating
//   the "ping" task to Worker synchronously so we get the result inline:ping"
//
// Note the missing space/break after "now." before "Now", and the missing
// space/break before the glued-on delegate reply "ping" after "inline:".
//
// Root cause: the `token` frame reducer in chat.ts (case 'token') reuses the
// last assistant bubble whenever it is still streaming AND the frame's
// agent_id doesn't disagree with the bubble's own agentId (see the sibling
// regression file chat.delegate-attribution.test.ts for that agent_id-change
// guard, which already opens a NEW bubble in that case). Neither guard fires
// when a tool call (e.g. `ToolSearch`, or a synchronous/"await" `delegate`
// call whose own reply rides back on the SAME agent_id / an omitted one)
// starts and completes WITHOUT the bubble ever closing or changing producer
// — `startToolCall`/the tool_call_start handler never touches isStreaming,
// so the bubble stays open across the tool call and the next token's content
// is glued directly onto the trailing text via plain string concatenation
// with no separator.
//
// Fix: a `pendingTextBoundary` flag, set on the reused bubble whenever a
// top-level (non-spanned) tool call starts on it, consumed by the next
// `token` append — which inserts a paragraph break (`\n\n`, matching the
// existing separator convention already used elsewhere in chat.ts for the
// media-attachment-failure note) before appending.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'

const SESSION_ID = 'delegate-text-boundary-test'

function resetStore() {
  act(() => {
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
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({ activeSessionId: SESSION_ID, activeAgentId: 'jim', activeAgentType: null })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStore)

describe('chat store — delegator-narration/delegate-content text join (regression)', () => {
  it('reproduces the exact live-UAT repro text with the join fixed (paragraph breaks, not glue)', () => {
    // Jim's own pre-tool-call narration streams in.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        content: 'Let me load the delegate tool and send the task now.',
        agent_id: 'jim',
        session_id: SESSION_ID,
      })
    })

    // A hidden-by-default infra tool call starts and completes WITHOUT the
    // bubble ever closing (tool_call_start never touches isStreaming) and
    // without changing the producer (same agent_id: 'jim') — exactly the
    // seam the old code glued over.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'ToolSearch_1',
        tool: 'ToolSearch',
        params: { tool: 'delegate' },
        agent_id: 'jim',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'ToolSearch_1',
        tool: 'ToolSearch',
        result: {},
        status: 'success',
        session_id: SESSION_ID,
      })
    })

    // Jim's narration resumes on the SAME bubble (still open, same agent_id).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        content: 'Now delegating the "ping" task to Worker synchronously so we get the result inline:',
        agent_id: 'jim',
        session_id: SESSION_ID,
      })
    })

    // The synchronous ("await") delegate call itself starts — another
    // tool_call_start on the still-open bubble.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'delegate_1',
        tool: 'delegate',
        params: { action: 'run', async: false },
        agent_id: 'jim',
        session_id: SESSION_ID,
      })
    })

    // The delegate's own reply rides back on the same bubble (same agent_id
    // 'jim' on the wire for this synchronous call — the exact scenario that
    // produced the glued-on "ping" in the live repro).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        content: 'ping',
        agent_id: 'jim',
        session_id: SESSION_ID,
      })
    })

    const msgs = useChatStore.getState().messages
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe(
      'Let me load the delegate tool and send the task now.\n\n' +
        'Now delegating the "ping" task to Worker synchronously so we get the result inline:\n\n' +
        'ping',
    )
    // The old, buggy behavior asserted here for documentation purposes only
    // (this must NOT be the rendered content):
    expect(msgs[0].content).not.toContain('now.Now delegating')
    expect(msgs[0].content).not.toContain('inline:ping')
  })

  it('does NOT insert a separator between plain consecutive tokens with no intervening tool call (no regression)', () => {
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Hello', agent_id: 'jim', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: ' world', agent_id: 'jim', session_id: SESSION_ID })
    })
    const msgs = useChatStore.getState().messages
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Hello world')
  })

  it('does not insert a leading separator for the FIRST token of a brand-new bubble', () => {
    // A tool call with no prior text (e.g. glm-5v-turbo calling a tool with
    // no lead-in narration) must not leave a stray leading "\n\n".
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'write_1', tool: 'write_file', params: {}, agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Done.', agent_id: 'jim', session_id: SESSION_ID })
    })
    const msgs = useChatStore.getState().messages
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Done.')
  })

  it('clears pendingTextBoundary when a turn ends with a tool call as its last event (no trailing token before done)', () => {
    // Narration streams in, then a tool call starts — flags the seam so the
    // NEXT token (if any) gets a paragraph break before it.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Running the command now.', agent_id: 'jim', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'shell_1', tool: 'shell', params: { cmd: 'echo hi' }, agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result', call_id: 'shell_1', tool: 'shell', result: { stdout: 'hi\n' }, status: 'success', session_id: SESSION_ID,
      })
    })

    // Sanity: the seam marker IS set right after tool_call_start, before done.
    const midMsgs = useChatStore.getState().messages
    expect(midMsgs).toHaveLength(1)
    expect(midMsgs[0].pendingTextBoundary).toBe(true)

    // The turn ends here — the agent's final action was the tool call, with
    // NO further narration token arriving before `done` (e.g. the agent's
    // last action in the turn was a tool call with no trailing text).
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: SESSION_ID })
    })

    const finalMsgs = useChatStore.getState().messages
    expect(finalMsgs).toHaveLength(1)
    expect(finalMsgs[0].content).toBe('Running the command now.')
    expect(finalMsgs[0].status).toBe('done')
    expect(finalMsgs[0].isStreaming).toBe(false)
    // THE FIX: pendingTextBoundary must be cleared on finalization. Left
    // uncleared, it would be a representable-but-meaningless `true` on a
    // finalized message that has no next token coming — a trap for future
    // code that reads this field and assumes `true` means "still mid-stream".
    expect(finalMsgs[0].pendingTextBoundary).toBe(false)
  })
})

// Bug 1 / Finding A regression (A-I4 round 4 — live vs reload rendering
// parity for multi-round AWAIT-mode delegation): a multi-step
// narration -> delegate call -> narration -> delegate call -> narration
// turn must render as the SAME single bubble on reload as it does live.
//
// IMPORTANT — why this test was rewritten (round 4): the PREVIOUS version of
// this test simulated its "live sequence" using plain `web_search`/
// `web_fetch` tool calls instead of real `delegate` calls, and never
// exercised subagent_start/subagent_end at all. That premise was never
// checked against a real browser. Live UAT re-verification this round (a
// real 3-round synchronous delegation driven through an actual gateway +
// headless browser) found that a REAL delegate call sequence rendered as
// FOUR separate bubbles live, not one — directly contradicting what the old
// test asserted "live" does. Root cause (traced via a real WS frame capture,
// not guesswork): pkg/agent/subturn.go's spawnSubTurn runs a delegated child
// through the EXACT SAME pkg/agent/loop.go runTurn/finalizeStreamer path as
// a root turn — so the CHILD's own wsStreamer.Finalize (pkg/gateway/
// websocket.go) fired its own live "done" WS frame the instant the child's
// (synchronous, blocking) sub-turn finished, mid-parent-turn. DoneFrame
// carries no turn/parent discriminator, so the client's `done` case
// (unconditionally closes whichever bubble is currently open) prematurely
// finalized the delegator's still-open bubble after EVERY delegate call,
// producing N+1 bubbles for N delegate calls. Fixed by gating Finalize's
// live-facing signals (the done frame, session-peer fan-out, markStreamed)
// on the SAME shadow-stream-ownership check Update() already used — a
// delegated child's streamer is never the live-stream owner, so it now never
// sends a live "done" either. With that backend fix in place, live's ACTUAL,
// verified behavior for a real delegate call sequence — reproduced below —
// IS one continuous bubble: this test's assertions were always correct in
// their OUTCOME, but the fix belongs in the BACKEND (making live's real
// behavior match the design), never in reload's merge logic (which already
// correctly merged same-turn/same-agent entries and was not the bug).
//
// This rewrite exercises the REAL frame shapes a delegate call actually
// produces — tool_call_start{tool:"delegate"} bracketed by subagent_start/
// subagent_end — for both the live and reload sequences, verified against
// the real WS frame capture from a live gateway (3 sequential AWAIT-mode
// delegate calls, glm-5.2, 2026-07-12): live produces exactly one "done"
// frame (at the true end of the whole turn, not one per delegate round), one
// continuous bubble with all narration + all 3 tool-call chips + all 3
// nested subagent spans, and reload of that exact persisted session
// reproduces the identical bubble/tool-call/span structure.
//
// See the merge-boundary fix in the `replay_message` case of chat.ts (turn_id
// + agent_id as the replay-side equivalent of the live 'token' case's
// agent_id-boundary rule) and the corresponding
// ChatStore_ReplaySequence_InterleavedTurn_TwoFrames tests in chat.test.ts.
describe('chat store — live vs reload rendering parity for multi-round delegate calls (Bug 1 / Finding A regression, A-I4 round 4)', () => {
  it('a 2-round await-mode delegation renders as ONE bubble both live and on reload — same bubble count, same content, same tool-call count, same span count, one model tag not one per split segment', () => {
    // ── Live sequence: Ray narrates, calls delegate (bracketed by
    // subagent_start/end, matching a real await-mode delegation), narrates
    // again, calls delegate a second time, then narrates a final synthesis —
    // all as ONE continuous live token stream sharing Ray's own agent_id
    // throughout. Exactly ONE 'done' frame at the very end (the backend fix
    // under test: a delegate child's own Finalize must never send a live
    // "done" mid-turn).
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Step 1: researching topic alpha.', agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'delegate_1', tool: 'delegate', params: { task: 'alpha', async: false }, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_start', span_id: 'span_delegate_1', parent_call_id: 'delegate_1', task_label: 'topic alpha', agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_end', span_id: 'span_delegate_1', parent_call_id: 'delegate_1', status: 'success', duration_ms: 900, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'delegate_1', tool: 'delegate', result: 'Subagent task completed:\nLabel: topic alpha\nResult: alpha-result', status: 'success', duration_ms: 900, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Step 2: researching topic beta.', agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'delegate_2', tool: 'delegate', params: { task: 'beta', async: false }, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_start', span_id: 'span_delegate_2', parent_call_id: 'delegate_2', task_label: 'topic beta', agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_end', span_id: 'span_delegate_2', parent_call_id: 'delegate_2', status: 'success', duration_ms: 1100, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'delegate_2', tool: 'delegate', result: 'Subagent task completed:\nLabel: topic beta\nResult: beta-result', status: 'success', duration_ms: 1100, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Both topics are done.', agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: SESSION_ID })
    })

    const liveState = useChatStore.getState()
    const liveMsgs = liveState.messages.filter((m) => m.role === 'assistant')
    expect(liveMsgs).toHaveLength(1)
    expect(liveMsgs[0].agentId).toBe('ray')
    expect(liveMsgs[0].content).toBe(
      'Step 1: researching topic alpha.\n\n' +
        'Step 2: researching topic beta.\n\n' +
        'Both topics are done.',
    )
    // Live 'token' streaming never sets .model — only replay_message does.
    expect(liveMsgs[0].model).toBeUndefined()
    expect(liveMsgs[0].tool_calls ?? []).toHaveLength(2)
    expect(liveMsgs[0].spans ?? []).toHaveLength(2)

    // ── Reload sequence: the SAME turn, replayed from a transcript that
    // persisted the Bug #416-split entries — three separate replay_message
    // frames sharing ONE turn_id (ts.turnID) and Ray's agent_id, interleaved
    // with the same two delegate calls, each bracketed by subagent_start/end
    // exactly as pkg/gateway/replay.go emits them for a spawn/delegate call
    // with recorded children (FR-I-003).
    act(() => { useChatStore.getState().resetSession() })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'assistant', content: 'Step 1: researching topic alpha.',
        id: 'r-entry-1', agent_id: 'ray', turn_id: 'ray-subturn-1', model: 'z-ai/glm-5.2', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'delegate_1', tool: 'delegate', params: { task: 'alpha', async: false }, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_start', span_id: 'span_delegate_1', parent_call_id: 'delegate_1', task_label: 'topic alpha', agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_end', span_id: 'span_delegate_1', parent_call_id: 'delegate_1', status: 'success', duration_ms: 900, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'delegate_1', tool: 'delegate', result: 'Subagent task completed:\nLabel: topic alpha\nResult: alpha-result', status: 'success', duration_ms: 900, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'assistant', content: 'Step 2: researching topic beta.',
        id: 'r-entry-2', agent_id: 'ray', turn_id: 'ray-subturn-1', model: 'z-ai/glm-5.2', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'delegate_2', tool: 'delegate', params: { task: 'beta', async: false }, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_start', span_id: 'span_delegate_2', parent_call_id: 'delegate_2', task_label: 'topic beta', agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'subagent_end', span_id: 'span_delegate_2', parent_call_id: 'delegate_2', status: 'success', duration_ms: 1100, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'delegate_2', tool: 'delegate', result: 'Subagent task completed:\nLabel: topic beta\nResult: beta-result', status: 'success', duration_ms: 1100, agent_id: 'ray', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'assistant', content: 'Both topics are done.',
        id: 'r-entry-3', agent_id: 'ray', turn_id: 'ray-subturn-1', model: 'z-ai/glm-5.2', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: SESSION_ID })
    })

    const reloadState = useChatStore.getState()
    const reloadMsgs = reloadState.messages.filter((m) => m.role === 'assistant')

    // Bubble count and header must match live exactly — not one bubble per
    // underlying transcript entry.
    expect(reloadMsgs).toHaveLength(liveMsgs.length)
    expect(reloadMsgs[0].agentId).toBe(liveMsgs[0].agentId)
    // Content matches live byte-for-byte.
    expect(reloadMsgs[0].content).toBe(liveMsgs[0].content)
    // Both delegate calls land on the SAME merged bubble, matching live.
    expect((reloadMsgs[0].tool_calls ?? []).length).toBe((liveMsgs[0].tool_calls ?? []).length)
    // Finding C: both nested subagent spans land on the SAME merged bubble
    // too, matching live — not silently dropped.
    expect((reloadMsgs[0].spans ?? []).length).toBe((liveMsgs[0].spans ?? []).length)
    // Exactly one model tag on the merged bubble — not one per split segment.
    expect(reloadMsgs[0].model).toBe('z-ai/glm-5.2')
  })
})
