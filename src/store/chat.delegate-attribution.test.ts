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
