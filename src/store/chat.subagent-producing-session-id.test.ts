// ADR-091 D7/I-4 (formerly ADR-057 U12/W5c, FR-012/FR-013) — the chat
// store's `childSessionId` field on a subagent span, the open control's
// navigation target. `subagent_start`/`subagent_end` are the live, real-
// emitter delegation-span frames — the natural place to thread the child's
// own real session id through to the side panel's open control
// (FR-046, `/sessions/{childSessionId}`), since the store's per-session
// buckets are keyed by the ROUTING session_id (the root of the chat tree),
// never by the id of the child that actually produced a given span.
//
// Superseded (ADR-091 D7/I-4, CP-0 additive): `childSessionId` is now
// populated from `SubagentStartFrame.child_session_id` — the field this
// delivery adds, populated by both front doors (`delegate` and
// `create_task`) — not from the ADR-057 workaround `producing_session_id`,
// which this file's original name references. `producing_session_id`
// itself is still present on the wire (not yet deleted — the lead
// coordinates that once the Go readers are gone) but the SPA no longer
// reads it for this purpose. `SubagentEndFrame` carries no session id field
// of its own (only `subagent_start` does) — the terminal span always keeps
// whatever `subagent_start` already stamped.
//
// New file per ownership Rule 5 (every unit's new tests go in new files);
// mirrors chat.delegate-attribution.test.ts's store-reset and
// handleFrame-dispatch pattern.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'

const SESSION_ID = 'producing-session-id-test-root'
const CHILD_SESSION_ID = 'producing-session-id-test-child'

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

describe('chat store — subagent span carries the real child session id (ADR-091 D7/I-4)', () => {
  it('subagent_start stamps the span with child_session_id as childSessionId', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token', content: 'Delegating the audit...', agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'delegate_1', tool: 'delegate', params: {}, agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_1',
        parent_call_id: 'delegate_1',
        task_label: 'audit the payments module',
        agent_id: 'ava',
        session_id: SESSION_ID,
        child_session_id: CHILD_SESSION_ID,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => (m.spans?.length ?? 0) > 0)
    expect(msg, 'the message holding the subagent span must exist').toBeDefined()
    const span = msg!.spans![0]
    expect(span.status).toBe('running')
    expect(span.childSessionId).toBe(CHILD_SESSION_ID)
  })

  it('a pre-delivery transcript that omits child_session_id leaves childSessionId undefined, not a crash or a bogus value', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'delegate_2', tool: 'delegate', params: {}, agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_2',
        parent_call_id: 'delegate_2',
        task_label: 'legacy gateway task',
        agent_id: 'ava',
        session_id: SESSION_ID,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => (m.spans?.length ?? 0) > 0)
    const span = msg!.spans!.find((s) => s.spanId === 'span_2')
    expect(span!.childSessionId).toBeUndefined()
  })

  it('subagent_end (which carries no session id field of its own) always keeps the childSessionId subagent_start already stamped', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'delegate_3', tool: 'delegate', params: {}, agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_3',
        parent_call_id: 'delegate_3',
        task_label: 'audit the payments module',
        agent_id: 'ava',
        session_id: SESSION_ID,
        child_session_id: CHILD_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_3',
        status: 'success',
        duration_ms: 4200,
        final_result: 'Found 2 issues.',
        session_id: SESSION_ID,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => (m.spans?.length ?? 0) > 0)
    const span = msg!.spans!.find((s) => s.spanId === 'span_3')
    expect(span!.status).toBe('success')
    expect(span!.childSessionId).toBe(CHILD_SESSION_ID)
  })

  it('subagent_end on a span that never got a child_session_id stays undefined, not a crash or a bogus value', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start', call_id: 'delegate_4', tool: 'delegate', params: {}, agent_id: 'jim', session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_4',
        parent_call_id: 'delegate_4',
        task_label: 'audit the payments module',
        agent_id: 'ava',
        session_id: SESSION_ID,
        // child_session_id deliberately omitted.
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_4',
        status: 'success',
        session_id: SESSION_ID,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => (m.spans?.length ?? 0) > 0)
    const span = msg!.spans!.find((s) => s.spanId === 'span_4')
    expect(span!.childSessionId).toBeUndefined()
  })
})
