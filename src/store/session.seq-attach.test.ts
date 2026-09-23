/**
 * session.seq-attach.test.ts — #823 phase 2 attach site #2.
 *
 * `useSessionStore.attachToSession` is the user-driven attach (session list,
 * deep link, workspace re-entry). Phase 2 gives it the same two changes as the
 * reconnect path:
 *
 *   1. it sends the ATTACHED session's applied-frame cursor as `since_seq`,
 *      omitted when that session has no position — and it must read that
 *      cursor from `sessionsById[sessionId]`, never from the foreground
 *      mirror, because an attach can target a session that is not the active
 *      one yet (the active id is written AFTER the attach frame goes out), and
 *   2. it wipes the attached session's transcript only when there is no
 *      cursor.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { useUiStore } from './ui'

const WS_A = 'ws-alpha'
const SID_A = 'attach-seq-a'
const SID_B = 'attach-seq-b'

function resetStores() {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
    })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
      sessionByWorkspace: {},
    })
    useWorkspacesStore.setState({ activeWorkspaceId: WS_A })
    useUiStore.setState({ toasts: [] })
  })
}

beforeEach(resetStores)

/** Wires a fake WS connection and returns its `send` spy. */
function connectMock() {
  const mockSend = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
      isConnected: true,
    })
  })
  return mockSend
}

function attachFrames(mockSend: ReturnType<typeof connectMock>): Array<Record<string, unknown>> {
  return mockSend.mock.calls
    .map((call) => call[0] as Record<string, unknown>)
    .filter((frame) => frame?.type === 'attach_session')
}

/** Apply a real token frame so the bucket's cursor is populated by the store itself. */
function seedCursor(sid: string, seq: number, content: string) {
  act(() => {
    useChatStore.getState().handleFrame({ type: 'token', session_id: sid, content, seq } as never)
  })
}

function bucketMessages(sid: string): string[] {
  const bucket = useChatStore.getState().sessionsById[sid]
  return bucket ? Object.values(bucket.messagesById).map((m) => m.content) : []
}

describe('#823 phase 2 — attachToSession sends the sequence cursor', () => {
  it('sends since_seq for the ATTACHED session, read from its own bucket rather than the foreground one', () => {
    const mockSend = connectMock()
    // The user is sitting in A (foreground) and clicks session B.
    act(() => { useSessionStore.setState({ activeSessionId: SID_A }) })
    seedCursor(SID_A, 3, 'A state')
    seedCursor(SID_B, 9, 'B state')

    act(() => {
      useSessionStore.getState().attachToSession(SID_B, 'chat', undefined, 'mia')
    })

    const attaches = attachFrames(mockSend)
    expect(attaches).toHaveLength(1)
    expect(attaches[0]).toMatchObject({ type: 'attach_session', session_id: SID_B, since_seq: 9 })
  })

  it('omits since_seq for a session the SPA has no position for (first load)', () => {
    const mockSend = connectMock()

    act(() => {
      useSessionStore.getState().attachToSession(SID_B, 'chat', undefined, 'mia')
    })

    const attaches = attachFrames(mockSend)
    expect(attaches).toHaveLength(1)
    expect('since_seq' in attaches[0]).toBe(false)
  })

  it('omits since_seq when the stored position is 0 (the gateway treats 0 as no cursor)', () => {
    const mockSend = connectMock()
    act(() => {
      useChatStore.setState((state) => ({
        sessionsById: {
          ...state.sessionsById,
          [SID_B]: {
            ...(state.sessionsById[SID_B] ?? {
              messagesById: {}, messageOrder: [], trimmedCount: 0, toolCalls: {}, toolCallOrder: [],
              textAtToolCallStart: {}, toolCallOwnerMessageId: {}, isStreaming: false, isReplaying: false,
              replayCompletedForSession: null, sessionTokens: 0, sessionCost: 0, rateLimitEvent: null,
              lastUserMessageAt: null, cancelStage: null, lastReceivedEventTime: null,
              spanByParentCallId: {}, spanBySpanId: {}, mergedReplayMessageIds: {}, goalStatus: null,
              goalPills: {}, loopStatus: null, pendingAsk: null,
            }),
            lastAppliedSeq: 0,
          },
        },
      }))
    })

    act(() => {
      useSessionStore.getState().attachToSession(SID_B, 'chat', undefined, 'mia')
    })

    expect('since_seq' in attachFrames(mockSend)[0]).toBe(false)
  })
})

describe('#823 phase 2 — attachToSession resets only when there is no cursor', () => {
  it('keeps the attached session transcript when a cursor exists, and arms the catch-up window', () => {
    connectMock()
    seedCursor(SID_B, 4, 'kept across attach')

    act(() => {
      useSessionStore.getState().attachToSession(SID_B, 'chat', undefined, 'mia')
    })

    expect(bucketMessages(SID_B)).toEqual(['kept across attach'])
    expect(useChatStore.getState().sessionsById[SID_B]?.isReplaying).toBe(true)
  })

  it('wipes the attached session transcript when there is no cursor', () => {
    connectMock()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', session_id: SID_B, role: 'assistant', content: 'stale history', id: 'm-stale-b',
      } as never)
    })

    act(() => {
      useSessionStore.getState().attachToSession(SID_B, 'chat', undefined, 'mia')
    })

    expect(bucketMessages(SID_B)).toEqual([])
  })
})