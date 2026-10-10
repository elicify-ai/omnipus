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
import type { Session } from '@/lib/api'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import { noteForegroundAttach } from '@/store/session/foregroundAck'

const SID = 'helper-session-1'
const MAIN_SID = 'main-approval-check'

let sent: unknown[]

function connectionThat(): WsConnection {
  return {
    send: (frame: unknown) => {
      sent.push(frame)
      return true
    },
  } as unknown as WsConnection
}

function mainSession(id: string): Session {
  return {
    id,
    agent_id: 'mia',
    title: 'Mia main',
    type: 'main',
    needs_attention: true,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-10T00:00:00Z',
    message_count: 1,
    workspace_id: 'ws-1',
  }
}

function stateFrame(sessionId: string, attentionBound: number): ServerFrame {
  return {
    type: 'session_state',
    user_id: '',
    pending_approvals: [],
    emitted_at: '2026-10-10T00:00:00Z',
    session_id: sessionId,
    attention_bound: attentionBound,
  } as unknown as ServerFrame
}

function catchUpComplete(sessionId: string): ServerFrame {
  return {
    type: 'catch_up_complete',
    session_id: sessionId,
    seq: 1,
    boot_id: 'boot-a',
    mode: 'incremental',
  } as unknown as ServerFrame
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

  it('approval frames never trigger the shown main’s acknowledgement early; the shown completion still acks with the frozen bound', () => {
    // The claimed scenario, actually built: a real LOADED MAIN is the active
    // shown foreground, its bound (7) has arrived, catch-up has NOT completed.
    // Approval frames for a helper must produce no acknowledgement keyed off
    // any of it; only the shown completion may ack, with the frozen bound.
    useSessionStore.setState({ activeSessionId: MAIN_SID })
    queryClient.setQueryData(['sessions'], [mainSession(MAIN_SID)])
    noteForegroundAttach(MAIN_SID)
    useChatStore.getState().handleFrame(stateFrame(MAIN_SID, 7))

    useChatStore.getState().handleFrame(requiredFrame(SID))
    useChatStore.getState().handleFrame(resolvedFrame(SID))
    expect(sent).toEqual([]) // no premature acknowledgement

    // Positive control: the shown completion acknowledges with the frozen 7.
    useChatStore.getState().handleFrame(catchUpComplete(MAIN_SID))
    expect(sent).toEqual([
      { type: 'attach_session', session_id: MAIN_SID, ack_attention: true, attention_bound: 7 },
    ])
  })

  it('approval frames naming the loaded MAIN itself still refresh the roster and send nothing — the frame id is never matched against mains', () => {
    useSessionStore.setState({ activeSessionId: MAIN_SID })
    queryClient.setQueryData(['sessions'], [mainSession(MAIN_SID)])
    noteForegroundAttach(MAIN_SID)
    useChatStore.getState().handleFrame(stateFrame(MAIN_SID, 7))

    // The approval frames name the MAIN's own session id here.
    useChatStore.getState().handleFrame(requiredFrame(MAIN_SID))
    expect(queryClient.getQueryState(['sessions'])?.isInvalidated).toBe(true) // the list still refreshes
    useChatStore.getState().handleFrame(resolvedFrame(MAIN_SID))
    expect(sent).toEqual([]) // and nothing is sent because of that id

    // Positive control: only the shown completion acknowledges, with the frozen bound.
    useChatStore.getState().handleFrame(catchUpComplete(MAIN_SID))
    expect(sent).toEqual([
      { type: 'attach_session', session_id: MAIN_SID, ack_attention: true, attention_bound: 7 },
    ])
  })
})
