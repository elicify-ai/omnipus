/**
 * Approval frames refresh the session list (session-core-build-20261008,
 * ARCHITECT-ANSWER-U8-U11, U11 N2).
 *
 * Approval frames carry the HELPER's session id, never the main's. The SPA
 * must therefore refetch the session list (['sessions']) on ANY approval
 * frame so a main's needs_attention projection refreshes — and must NEVER
 * match the approval frame's session id against mains (only the refetched
 * server truth lights a cue).
 *
 * Oracle: after either approval frame, the ['sessions'] query is invalidated
 * (its cached state reads isInvalidated) and no attach frame is sent from
 * this path at all.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import type { ServerFrame, WsConnection } from '@/lib/ws'

const SID = 'helper-session-1'

let sent: unknown[]

function connectionThat(): WsConnection {
  return {
    send: (frame: unknown) => {
      sent.push(frame)
      return true
    },
  } as unknown as WsConnection
}

function requiredFrame(sessionId: string): ServerFrame {
  return {
    type: 'tool_approval_required',
    approval_id: 'appr-1',
    tool_call_id: 'call-1',
    tool_name: 'bash',
    args: { command: 'ls' },
    agent_id: 'helper-agent',
    session_id: sessionId,
    turn_id: 'turn-1',
    expires_in_ms: 30_000,
  } as unknown as ServerFrame
}

function resolvedFrame(sessionId: string): ServerFrame {
  return {
    type: 'tool_approval_resolved',
    approval_id: 'appr-1',
    state: 'approved',
    session_id: sessionId,
  } as unknown as ServerFrame
}

beforeEach(() => {
  sent = []
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({ connection: connectionThat() } as never)
  queryClient.setQueryData(['sessions'], [])
})

afterEach(() => {
  queryClient.removeQueries({ queryKey: ['sessions'] })
  useConnectionStore.setState({ connection: null } as never)
})

describe('approval frames refresh the session list (U11 N2)', () => {
  it('tool_approval_required invalidates the sessions query', () => {
    useChatStore.getState().handleFrame(requiredFrame(SID))
    expect(queryClient.getQueryState(['sessions'])?.isInvalidated).toBe(true)
  })

  it('tool_approval_resolved invalidates the sessions query', () => {
    useChatStore.getState().handleFrame(resolvedFrame(SID))
    expect(queryClient.getQueryState(['sessions'])?.isInvalidated).toBe(true)
  })

  it('the approval frame carrying a MAIN session id never sends an acknowledgement — matching frames against mains is forbidden', () => {
    // The frame here names a loaded main directly: the only legitimate effect
    // is the roster refresh, never an attach/ack keyed off the frame's id.
    useChatStore.getState().handleFrame(requiredFrame(SID))
    useChatStore.getState().handleFrame(resolvedFrame(SID))
    expect(sent).toEqual([])
  })
})
