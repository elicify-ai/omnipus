import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  reattachActiveSession,
  type ReattachSender,
} from '@/components/chat/OmnipusRuntimeProvider'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

// Issue #1090, founder-approved T3/T4. The investigation report's "Proposed
// fix and risks — not implemented" requires preserving an ordinary unconfirmed
// first message after reconnect, and keeping it out of a later /new chat.
// No session_started is delivered in these cases. No retry/dedup wire shape or
// unfinished "not delivered" UI is assumed: assert the real store/foreground.

function reset1090Stores() {
  act(() => {
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: null,
      activeAgentId: 'mia',
      attachedSessionType: null,
      attachedTaskTitle: null,
      sessionByWorkspace: {},
    }, true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    localStorage.clear()
    sessionStorage.clear()
  })
}

beforeEach(() => {
  // The real connection store schedules a reconnect-status expiry. Own that
  // external clock so it cannot escape this file's jsdom lifetime.
  vi.useFakeTimers()
  reset1090Stores()
})

afterEach(() => {
  reset1090Stores()
  vi.clearAllTimers()
  vi.useRealTimers()
})

function connect1090Sender() {
  // Only the network edge is replaced. sendMessage, disconnect cleanup,
  // reattachActiveSession, queue draining and session actions remain real.
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
  return sender
}

function reconnect1090Sender(sender: ReattachSender) {
  act(() => {
    // Same actions and order as WsLifecycle::onDisconnected/onConnected.
    const state = useChatStore.getState()
    useConnectionStore.getState().recordDisconnect(
      state.isStreaming ? state.lastAssistantMessageId : null,
    )
    useChatStore.getState().clearStreamingState()
    useConnectionStore.getState().setConnected(true)
    useConnectionStore.getState().setConnectionError(null)
    reattachActiveSession(sender, useConnectionStore.getState().setConnectionError)
    useChatStore.getState().drainOutboundQueue()
  })
}

function foreground1090Users() {
  return useChatStore.getState().messages
    .filter((message) => message.role === 'user')
    .map(({ id, content }) => ({ id, content }))
}

describe('#1090 — an ordinary unconfirmed first message after a crash', () => {
  it('T3: reconnect retains the first user message in the store AND the active foreground', () => {
    const content = 'T3 keep this first message visible after reconnect'
    const id = '1090-t3-first'
    const sender = connect1090Sender()

    act(() => useChatStore.getState().sendMessage(content, { clientMessageId: id }))
    expect(sender.send).toHaveBeenCalledTimes(1)
    expect(sender.send).toHaveBeenNthCalledWith(1, expect.objectContaining({
      type: 'message', content, client_message_id: id, agent_id: 'mia',
    }))
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(useChatStore.getState().pendingKickoff,
      'T3 fixture is an ordinary first message, NOT a workspace-setup kickoff',
    ).toBeNull()
    expect(foreground1090Users(), 'T3 fixture must first show the submitted message')
      .toEqual([{ id, content }])

    reconnect1090Sender(sender)

    const retainedUsers = Object.values(useChatStore.getState().sessionsById)
      .flatMap(getMessages)
      .filter((message) => message.role === 'user')
      .map(({ id: messageId, content: messageContent }) => ({ id: messageId, content: messageContent }))
    expect.soft(retainedUsers, 'T3 must not delete the user message from the store')
      .toEqual([{ id, content }])
    expect.soft(foreground1090Users(),
      'T3: reconnect silently replaced the unconfirmed first message with an empty new chat',
    ).toEqual([{ id, content }])
    expect.soft(useSessionStore.getState().activeSessionId,
      'T3: an unconfirmed ordinary chat must not be reset to an empty new chat',
    ).not.toBeNull()
  })

  it('T4: confirmed + New chat and its first send do not inherit the failed chat\'s stale pending message', () => {
    const oldContent = 'T4 old unconfirmed message belongs only to the failed chat'
    const oldId = '1090-t4-old'
    const newContent = 'T4 fresh first message belongs only to the new chat'
    const newId = '1090-t4-new'
    const sender = connect1090Sender()

    act(() => useChatStore.getState().sendMessage(oldContent, { clientMessageId: oldId }))
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(foreground1090Users(), 'T4 fixture must first show the old pending message')
      .toEqual([{ id: oldId, content: oldContent }])
    reconnect1090Sender(sender)

    // Wave-2 FR-005: + New chat requires confirmation before abandoning an
    // unconfirmed first send. Typed /new is server-owned, not this action.
    // Keep T4's original guard: the next send must not reuse the stale bucket.
    act(() => useSessionStore.getState().startNewSession({ choice: 'confirm', clientMessageId: oldId }))
    expect(foreground1090Users(), 'T4 confirmed + New chat must start with an empty foreground').toEqual([])
    act(() => useChatStore.getState().sendMessage(newContent, { clientMessageId: newId }))
    expect(sender.send).toHaveBeenCalledTimes(2)
    expect(sender.send).toHaveBeenNthCalledWith(2, expect.objectContaining({
      type: 'message', content: newContent, client_message_id: newId, agent_id: 'mia',
    }))

    expect.soft(foreground1090Users(),
      'T4: confirmed + New chat reused the stale pending bucket and leaked the old user message',
    ).toEqual([{ id: newId, content: newContent }])
    const activeId = useSessionStore.getState().activeSessionId
    const activeBucket = activeId ? useChatStore.getState().sessionsById[activeId] : undefined
    const activeUsers = (activeBucket ? getMessages(activeBucket) : [])
      .filter((message) => message.role === 'user')
      .map(({ id, content }) => ({ id, content }))
    expect.soft(activeUsers, 'T4 the active chat bucket must also contain only the fresh user message')
      .toEqual([{ id: newId, content: newContent }])
  })
})
