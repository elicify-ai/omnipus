// ADR-057 U12 (W5c, FR-012/FR-013) — the chat store's first consumer of the
// genuinely-new `producing_session_id` field (verified 2026-08-03:
// `rg -c producing_session_id contracts/ src/ pkg/` was zero everywhere
// tree-wide before this change). `subagent_start`/`subagent_end` are the
// live, real-emitter delegation-span frames (distinct from the dead,
// zero-Go-emitter `subagent_message`/`subagent_state` pair the Explicit
// Non-Behaviors section forbids relying on) — the natural place to thread
// the child's own real session id through to a future drill-down link
// (FR-046, `/sessions/{childSessionId}`), since the store's per-session
// buckets are keyed by the ROUTING session_id (the root of the chat tree,
// FR-012), never by the id of the child that actually produced a given
// span.
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

describe('chat store — subagent span carries the real child session id (ADR-057 FR-013)', () => {
  it('subagent_start stamps the span with producing_session_id as childSessionId', () => {
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
        producing_session_id: CHILD_SESSION_ID,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => (m.spans?.length ?? 0) > 0)
    expect(msg, 'the message holding the subagent span must exist').toBeDefined()
    const span = msg!.spans![0]
    expect(span.status).toBe('running')
    expect(span.childSessionId).toBe(CHILD_SESSION_ID)
  })

  it('a pre-ADR-057 gateway that omits producing_session_id leaves childSessionId undefined, not a crash or a bogus value', () => {
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

  it('subagent_end carries its own producing_session_id through onto the terminal span', () => {
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
        producing_session_id: CHILD_SESSION_ID,
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
        producing_session_id: CHILD_SESSION_ID,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => (m.spans?.length ?? 0) > 0)
    const span = msg!.spans!.find((s) => s.spanId === 'span_3')
    expect(span!.status).toBe('success')
    expect(span!.childSessionId).toBe(CHILD_SESSION_ID)
  })

  it('subagent_end omitting producing_session_id keeps the value already stamped by subagent_start (fallback, mirrors the existing agentId fallback)', () => {
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
        producing_session_id: CHILD_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_4',
        status: 'success',
        session_id: SESSION_ID,
        // producing_session_id deliberately omitted.
      })
    })

    const msg = useChatStore.getState().messages.find((m) => (m.spans?.length ?? 0) > 0)
    const span = msg!.spans!.find((s) => s.spanId === 'span_4')
    expect(span!.childSessionId).toBe(CHILD_SESSION_ID)
  })
})
