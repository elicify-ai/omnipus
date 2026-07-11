// Regression test for the delegator-narration/delegate-content text-glue bug
// found in live UAT (persona "Dana"), reproduced verbatim as:
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
// when a tool call (e.g. `load_tool`, or a synchronous/"await" `delegate`
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
  it('reproduces the exact "Dana" repro text with the join fixed (paragraph breaks, not glue)', () => {
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
        call_id: 'load_tool_1',
        tool: 'load_tool',
        params: { tool: 'delegate' },
        agent_id: 'jim',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'load_tool_1',
        tool: 'load_tool',
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
})
