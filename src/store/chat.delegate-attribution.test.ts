// Regression test for the delegator/delegate speaker-misattribution bug found in
// live UAT.
//
// Symptom (two manifestations of the same root cause):
//   1. Persisted: the delegator's (Jim's) own pre-delegate reasoning
//      text was consistently labeled with the DELEGATE's ("Worker") name/avatar in
//      the persisted chat bubble.
//   2. Live-only, self-healing: mid-stream, Jim's pre-delegate reasoning was
//      transiently labeled with the delegate's ("Researcher") name for ~2s (avatar
//      stayed Jim's, since AssistantMessageAvatar reads session-level activeAgentId,
//      not the per-message agentId) before self-correcting once Jim's own turn
//      resumed and re-stamped the bubble.
//
// Root cause: the `token` frame reducer in chat.ts decided whether to reuse the
// last assistant bubble based ONLY on that bubble's `isStreaming` flag, with no
// check that the incoming frame's `agent_id` still matches the bubble's own
// `agentId`. A background delegate's own streamed tokens correctly carry the
// DELEGATE's agent_id on the wire (Fix 5a), but they land on the delegator's
// bubble whenever the delegator's turn is still open (which it always is while
// waiting on the delegate) — and `msg.agentId = frame.agent_id` then
// unconditionally relabeled the WHOLE bubble, including the delegator's own
// already-rendered text, as the delegate's. If the delegator's turn later
// resumes (its own further tokens re-stamp the bubble back), the mislabeling
// self-heals before the turn finalizes (the live-only case); if the delegate's
// contribution is the last writer before the bubble closes, the mislabeling
// survives into the persisted transcript (the persisted case).
//
// Fix: treat a bubble/frame agent_id mismatch as a "new producer segment"
// boundary — exactly like the pre-existing closed-bubble segment boundary — so
// the delegate's own tokens open a NEW bubble instead of hijacking the
// delegator's.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'

const SESSION_ID = 'delegate-attribution-test'

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

describe('chat store — delegator/delegate token attribution (regression)', () => {
  it('the delegate own token stream must never relabel the delegator bubble mid-turn', () => {
    // Jim's own pre-delegate reasoning streams in across two token frames —
    // exactly what a live LLM stream looks like right before it emits a tool call.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token', content: 'I misread my roster, ', agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token', content: 'let me load the delegate tool...', agent_id: 'jim', session_id: SESSION_ID,
      })
    })

    const afterReasoning = useChatStore.getState().messages
    expect(afterReasoning).toHaveLength(1)
    expect(afterReasoning[0].agentId).toBe('jim')
    expect(afterReasoning[0].content).toBe('I misread my roster, let me load the delegate tool...')

    // Jim dispatches the delegate tool call — his own top-level tool call
    // (no parent_call_id: this call IS what creates the subagent span).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'delegate_1', tool: 'delegate', params: {}, agent_id: 'jim', session_id: SESSION_ID,
      })
    })

    // The subagent span opens for the delegate ("worker").
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start', span_id: 'span_1', parent_call_id: 'delegate_1', task_label: 'research', agent_id: 'worker', session_id: SESSION_ID,
      })
    })

    // Jim's bubble must still be his own at this point — opening the span must
    // not touch the parent message's own agentId.
    const afterSpanStart = useChatStore.getState().messages
    expect(afterSpanStart).toHaveLength(1)
    expect(afterSpanStart[0].agentId).toBe('jim')

    // The delegate's OWN reasoning-text token stream arrives on the SAME
    // chat/session (delegate turns share chatID with the parent — see
    // pkg/agent/subturn.go), correctly carrying the delegate's agent_id on the
    // wire per Fix 5a. Jim's turn is still open (isStreaming) while it waits on
    // the delegate — this is the exact race window the bug lived in.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token', content: 'Researching now...', agent_id: 'worker', session_id: SESSION_ID,
      })
    })

    const afterDelegateToken = useChatStore.getState().messages
    const jimBubble = afterDelegateToken.find((m) => m.role === 'assistant' && m.content.includes('I misread my roster'))
    expect(jimBubble, 'Jim\'s own reasoning bubble must still exist, unmodified').toBeDefined()
    // THE core regression assertion: Jim's own reasoning text must ALWAYS stay
    // attributed to Jim, never to the delegate — including through the moment
    // the delegate starts producing its own tokens.
    expect(jimBubble!.agentId).toBe('jim')
    expect(jimBubble!.content).toBe('I misread my roster, let me load the delegate tool...')

    // The delegate's own token content must land in its OWN bubble, correctly
    // attributed to the delegate — not silently dropped, and not merged into
    // Jim's bubble.
    const delegateBubble = afterDelegateToken.find((m) => m.role === 'assistant' && m.content.includes('Researching now'))
    expect(delegateBubble, 'the delegate\'s own token content must be rendered somewhere').toBeDefined()
    expect(delegateBubble!.agentId).toBe('worker')
    expect(delegateBubble!.id).not.toBe(jimBubble!.id)

    // Jim's turn resumes after the delegate. The delegate's bubble is now the
    // most recent segment, so — by the same new-producer-segment rule — Jim's
    // resumed tokens open their OWN fresh bubble rather than reclaiming either
    // prior bubble. This is the key structural improvement over the old
    // behavior: attribution is correct for every segment as it is produced,
    // with no window where a bubble is mislabeled and no reliance on a later
    // frame "healing" it back.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token', content: 'The delegate is on it.', agent_id: 'jim', session_id: SESSION_ID,
      })
    })

    const final = useChatStore.getState().messages
    expect(final).toHaveLength(3)

    // Jim's original reasoning bubble is untouched by anything that followed.
    const finalJimBubble = final.find((m) => m.id === jimBubble!.id)
    expect(finalJimBubble!.agentId).toBe('jim')
    expect(finalJimBubble!.content).toBe('I misread my roster, let me load the delegate tool...')

    // The delegate's bubble is untouched by Jim's resumed reasoning.
    const finalDelegateBubble = final.find((m) => m.id === delegateBubble!.id)
    expect(finalDelegateBubble!.agentId).toBe('worker')
    expect(finalDelegateBubble!.content).toBe('Researching now...')

    // Jim's resumed reasoning lands in its own new, correctly-attributed bubble.
    const resumedJimBubble = final.find((m) => m.id !== jimBubble!.id && m.id !== delegateBubble!.id)
    expect(resumedJimBubble, 'jim\'s resumed reasoning must render somewhere').toBeDefined()
    expect(resumedJimBubble!.agentId).toBe('jim')
    expect(resumedJimBubble!.content).toBe('The delegate is on it.')
  })

  it('same-agent token frames still coalesce into a single bubble (no regression)', () => {
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Hello', agent_id: 'jim', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: ' world', agent_id: 'jim', session_id: SESSION_ID })
    })
    const msgs = useChatStore.getState().messages
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Hello world')
    expect(msgs[0].agentId).toBe('jim')
  })

  it('a legacy token frame with no agent_id keeps appending to the open bubble (no regression)', () => {
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Hello', agent_id: 'jim', session_id: SESSION_ID })
    })
    // Legacy/older frame omitting agent_id must not be treated as a new producer.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: ' world', session_id: SESSION_ID })
    })
    const msgs = useChatStore.getState().messages
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Hello world')
    expect(msgs[0].agentId).toBe('jim')
  })

  it('the optimistic send-time placeholder (no agentId yet) is stamped normally, not split', () => {
    // sendMessage() creates a placeholder with no agentId before the first
    // token frame is known to arrive — simulate that directly.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'optimistic-1',
        session_id: SESSION_ID,
        role: 'assistant',
        content: '',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Hi there', agent_id: 'jim', session_id: SESSION_ID })
    })
    const msgs = useChatStore.getState().messages
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Hi there')
    expect(msgs[0].agentId).toBe('jim')
  })
})

// Bug 2 regression (live UAT, persona Dana, DOM evidence via
// data-testid="agent-label"): in SYNCHRONOUS/AWAIT-mode delegation
// specifically, the delegator's OWN "Delegate task" tool-call card rendered
// inside a bubble labeled with the DELEGATE's agent-label instead of the
// delegator's — distinct from (and not fixed by) the token-attribution fix
// above, which only stamps agentId from 'token' frames.
//
// Root cause: the top-level (non-spanned) `tool_call_start` handler in
// chat.ts never stamped `agentId` on an EXISTING bubble it reused — only on
// a brand-new placeholder it created. frame.agent_id on a tool_call_start is
// always the CALLER of the tool (pkg/agent/turn.go's
// appendToolCallTranscript / resolveActiveAgentID; replay.go's buildStart
// sources it from the owning TranscriptEntry.AgentID) — for the delegator's
// own "delegate" call, that's the delegator, never the delegate. When the
// delegator emits NO leading narration before calling `delegate` (the fast,
// ~1s synchronous round-trip described in the bug report), the optimistic
// placeholder sendMessage() creates (no agentId at creation) is STILL
// unattributed when tool_call_start fires — and stays that way, since the
// old code never stamped it there either. The delegate's own reply tokens
// then arrive (its sub-turn shares the parent's transcriptSessionID/
// transcriptStore per pkg/agent/subturn.go regardless of Async), and because
// the `token` case's agent_id-boundary rule (Fix 5a) only opens a NEW bubble
// when BOTH sides are known and mismatched, an unset bubble agentId is
// permissive — the delegate's tokens silently claim the WHOLE bubble,
// including the delegator's own tool-call card.
//
// In background/async-mode delegation this window rarely bites because the
// delegator's own turn continues immediately and typically stamps the
// bubble with its own id before the (genuinely concurrent) delegate's
// tokens arrive — matching the bug report's confirmation that async mode
// was already correct.
//
// Fix: stamp/lock `agentId` from `frame.agent_id` when tool_call_start
// reuses an existing bubble, exactly mirroring what the 'token' case
// already does — so the bubble's identity is locked to its rightful owner
// (the tool's caller) the instant the call starts, before any later frame
// from a different producer can land on it.
describe('chat store — synchronous/await-mode delegate tool-call attribution (Bug 2 regression)', () => {
  it('the delegator\'s own "Delegate task" call is attributed to the delegator, not the delegate, in a fast synchronous round-trip', () => {
    // sendMessage() creates the optimistic placeholder with NO agentId yet —
    // this is the exact race window the bug lived in.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'optimistic-sync-1',
        session_id: SESSION_ID,
        role: 'assistant',
        content: '',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
    })

    // Jim (the delegator) calls `delegate` with ZERO leading narration — no
    // prior token frame has stamped the placeholder's agentId yet.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'delegate_sync_1', tool: 'delegate', params: { task: 'Ping' }, agent_id: 'jim', session_id: SESSION_ID,
      })
    })

    // THE core Bug 2 regression assertion: the bubble holding the
    // delegator's own tool-call card must already be attributed to the
    // delegator as soon as the call starts — not left unattributed for a
    // later frame to claim.
    const afterStart = useChatStore.getState().messages
    expect(afterStart).toHaveLength(1)
    expect(afterStart[0].agentId).toBe('jim')

    // The delegate's synchronous sub-turn runs with zero nested tool calls
    // (matching the bug report's DOM evidence: "0 steps") and its own reply
    // streams back on the SAME session, correctly carrying the delegate's
    // own agent_id on the wire.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'pong', agent_id: 'explorer', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result', call_id: 'delegate_sync_1', tool: 'delegate', result: { steps: 0 }, status: 'success', duration_ms: 1000, session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: SESSION_ID })
    })

    const final = useChatStore.getState().messages
    // The delegator's own tool-call card and the delegate's reply must
    // render in SEPARATE, correctly-labeled bubbles — never a single bubble
    // mislabeled with the delegate's name.
    expect(final).toHaveLength(2)
    const delegatorBubble = final.find((m) => m.agentId === 'jim')
    const delegateBubble = final.find((m) => m.agentId === 'explorer')
    expect(delegatorBubble, "the delegator's own bubble (holding the Delegate task card) must exist").toBeDefined()
    expect(delegateBubble, "the delegate's own reply bubble must exist separately").toBeDefined()
    expect(delegateBubble!.content).toBe('pong')
    expect(delegatorBubble!.id).not.toBe(delegateBubble!.id)

    // The tool call must be baked onto the DELEGATOR's bubble (the one that
    // actually issued it), not lost, and not baked onto the delegate's
    // bubble just because the delegate's reply happened to be the last
    // message when the turn ended. Without this, ChatScreen.tsx's
    // VirtualizedMessageListInner — which renders every non-last message via
    // the historical VirtualAssistantMessageRow, reading message.tool_calls
    // only — would show the "Delegate task" pill on the wrong bubble (or not
    // at all) the instant the delegate's reply superseded the delegator's
    // bubble as "last", well before this `done` frame even arrives.
    const delegatorToolCalls = delegatorBubble!.tool_calls ?? []
    const delegateToolCalls = delegateBubble!.tool_calls ?? []
    expect(delegatorToolCalls).toHaveLength(1)
    expect(delegatorToolCalls[0].id).toBe('delegate_sync_1')
    expect(delegatorToolCalls[0].tool).toBe('delegate')
    expect(delegatorToolCalls[0].status).toBe('success')
    expect(delegateToolCalls).toHaveLength(0)
  })

  it('a top-level tool call started by the SAME agent already holding the bubble is a harmless re-stamp (no regression)', () => {
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Running a command.', agent_id: 'jim', session_id: SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'shell_1', tool: 'shell', params: { cmd: 'echo hi' }, agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    const msgs = useChatStore.getState().messages
    expect(msgs).toHaveLength(1)
    expect(msgs[0].agentId).toBe('jim')
  })
})
