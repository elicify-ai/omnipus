// Issue #1428 RED-first pack — draft/queue send ownership, refused-send text
// retention, delivery-confirmation deadline, idle /cancel feedback.
//
// Base: feature/1266-chat-history-paging @ 7d2d03675 (the #1266 send-path
// rework). Oracles come from the issue's "Expected" section and the chief's
// dispatch — never from the implementation:
//
//   #1428 Expected: "A typed or queued message is only ever sent to the
//   conversation it was written in. Text is never cleared when a send is
//   refused. An unconfirmed message shows 'Delivery not confirmed · Retry'
//   after a short correlated deadline, without stopping the agent."
//   Chief brief: "an idle /cancel shows 'nothing to cancel'".
//
// Harness: real stores, real useOmnipusRuntime + installed AssistantUI
// composer, real frame reducer. Only the process edge is controlled: the
// WsConnection transport (a recorded vi.fn) and, where a deadline is
// asserted, the clock (vi.useFakeTimers).
//
// DELIVERY CONFIRMATION DEADLINE — FOUNDER DECISION Q67: 15 seconds.
// "Delivery not confirmed · Retry" appears 15 s after a send with no server
// receipt; the agent keeps working either way (chief, 2026-10-23, ruling on
// the uat-send triage §4 proposal this pack originally carried as an open
// question). The value below IS the ruled 15 s.

import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { createElement } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { useSlashMenu } from '@/hooks/useSlashMenu'
import { useCancelState } from '@/hooks/useCancelState'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import type { WsConnection } from '@/lib/ws'
import type { components } from '@/lib/api/generated/openapi-types'
import type { MessageFrame } from '@/lib/api/generated/asyncapi-types'

// The conversation the draft/queue/message was WRITTEN in.
const SID_A = 'session_01JCE1428ORIGINA1CHAT000000'
// The unrelated session that becomes attached/active (Activity-row helper).
const SID_B = 'session_01JCE1428HELPERBCHAT000000'
const AMBIENT_WORKSPACE = '01JCE1428WORKSPACEAMBIENT00000'
const OWNER = 'mia'
const TEXT = 'Issue 1428 ownership probe'
const STEER = 'Issue 1428 steer probe — keep running'

/** Founder decision Q67: the delivery-confirmation deadline is 15 s. */
const DELIVERY_CONFIRM_DEADLINE_MS = 15_000

let send: ReturnType<typeof vi.fn>
let seq = 0

function seedKnownSession(sessionId: string): void {
  const session: Pick<components['schemas']['Session'], 'id' | 'agent_id' | 'workspace_id'> = {
    id: sessionId,
    agent_id: OWNER,
    workspace_id: AMBIENT_WORKSPACE,
  }
  queryClient.setQueryData(['session-detail', sessionId], { session })
}

function sentMessageFrames(): MessageFrame[] {
  return send.mock.calls
    .map(([frame]) => frame)
    .filter((frame) => frame.type === 'message') as MessageFrame[]
}

function sentCancelFrames(): Array<Record<string, unknown>> {
  return send.mock.calls.map(([frame]) => frame).filter((frame) => frame.type === 'cancel')
}

function userContents(): string[] {
  return useChatStore.getState().messages.filter((m) => m.role === 'user').map((m) => m.content)
}

beforeEach(() => {
  seq = 0
  vi.useRealTimers()
  queryClient.clear()
  localStorage.clear()
  __resetFinishedTurnIdsForTests()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    seedKnownSession(SID_A)
    useSessionStore.getState().setActiveSession(SID_A, OWNER)
    useWorkspacesStore.setState({ activeWorkspaceId: AMBIENT_WORKSPACE })
    send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({
      connection: { send, isConnected: true, connect: vi.fn(), disconnect: vi.fn() } as unknown as WsConnection,
      isConnected: true,
    })
  })
})

afterEach(() => {
  vi.useRealTimers()
  queryClient.clear()
})

describe('issue #1428 — a draft is sent only to the conversation it was written in', () => {
  it('draft typed in A is delivered to A, never to the helper attached before Send', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    act(() => result.current.thread.composer.setText(TEXT))
    expect(result.current.thread.composer.getState().text).toBe(TEXT)
    // The Activity-row Enter case: opening the helper attaches its session
    // while the app-wide composer still holds A's draft.
    act(() => {
      expect(useSessionStore.getState().attachToSession(SID_B, 'delegate', 'Helper B', 'worker')).toBe(true)
    })
    expect(useSessionStore.getState().activeSessionId).toBe(SID_B)
    await act(async () => { await result.current.thread.composer.send() })

    const frames = sentMessageFrames()
    expect(frames, 'exactly one message goes on the wire').toHaveLength(1)
    expect(frames[0]).toMatchObject({ type: 'message', content: TEXT })
    expect(frames[0].session_id, '#1428 Expected: sent to the conversation it was written in').toBe(SID_A)
    expect(frames.filter((f) => f.session_id === SID_B), 'nothing may reach the helper').toEqual([])
    // The optimistic user bubble lands in A's bucket, not B's.
    const bucketA = useChatStore.getState().sessionsById[SID_A]
    const bucketB = useChatStore.getState().sessionsById[SID_B]
    expect(Object.values(bucketA?.messagesById ?? {}).filter((m) => m.role === 'user').map((m) => m.content)).toEqual([TEXT])
    expect(Object.values(bucketB?.messagesById ?? {}).filter((m) => m.role === 'user')).toEqual([])
  })

  it('a session switch during the acceptance-to-dispatch handoff cannot retarget the accepted message', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    act(() => result.current.thread.composer.setText(TEXT))
    await act(async () => {
      // Real send accepts (clears) the text and yields before the adapter's
      // onNew dispatches — attach the helper exactly inside that gap.
      const sending = result.current.thread.composer.send()
      useSessionStore.getState().attachToSession(SID_B, 'delegate', 'Helper B', 'worker')
      await sending
    })
    const frames = sentMessageFrames()
    expect(frames, 'exactly one message goes on the wire').toHaveLength(1)
    expect(frames[0]).toMatchObject({ type: 'message', content: TEXT })
    expect(frames[0].session_id, '#1428 Expected: destination frozen at acceptance').toBe(SID_A)
    expect(frames.filter((f) => f.session_id === SID_B)).toEqual([])
  })

  it('guard: with no session switch a settled send reaches its own conversation and clears the composer', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    act(() => result.current.thread.composer.setText(TEXT))
    await act(async () => { await result.current.thread.composer.send() })
    const frames = sentMessageFrames()
    expect(frames).toHaveLength(1)
    expect(frames[0]).toMatchObject({ type: 'message', content: TEXT, session_id: SID_A })
    expect(result.current.thread.composer.getState().text).toBe('')
    expect(userContents()).toEqual([TEXT])
  })
})

describe('issue #1428 — a queued message drains only into its own conversation', () => {
  it('an offline-queued message drains into its own conversation after navigation, not the active one', () => {
    act(() => useConnectionStore.setState({ isConnected: false }))
    act(() => useChatStore.getState().sendMessage(TEXT, { clientMessageId: 'queued-1428-a' }))
    expect(send.mock.calls).toEqual([])
    expect(useChatStore.getState().outboundQueue).toHaveLength(1)
    // Navigation away, then the reconnect drain.
    act(() => {
      useSessionStore.getState().setActiveSession(SID_B, 'worker')
      useConnectionStore.setState({ isConnected: true })
      useChatStore.getState().drainOutboundQueue()
    })
    const frames = sentMessageFrames()
    expect(frames, 'the drain sends the queued message once').toHaveLength(1)
    expect(frames[0]).toMatchObject({ type: 'message', content: TEXT })
    expect(frames[0].session_id, '#1428 Expected: queued message drains into its own conversation').toBe(SID_A)
    expect(frames.filter((f) => f.session_id === SID_B)).toEqual([])
  })

  it('a queued message stays bound to its conversation even when the drain is freed by another chat finishing its turn', () => {
    act(() => useConnectionStore.setState({ isConnected: false }))
    act(() => useChatStore.getState().sendMessage(TEXT, { clientMessageId: 'queued-1428-b' }))
    act(() => useSessionStore.getState().setActiveSession(SID_B, 'worker'))
    act(() => useConnectionStore.setState({ isConnected: true }))
    // B is mid-turn when the reconnect happens: the drain must hold…
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_state', session_id: SID_B, user_id: 'u-1428', pending_approvals: [],
        active_turn: { turn_id: 'turn-b-1428', agent_id: 'worker', started_at: '2026-10-23T08:00:00Z' },
        emitted_at: '2026-10-23T08:00:00Z',
      })
      useChatStore.getState().handleFrame({
        type: 'token', session_id: SID_B, agent_id: 'worker', turn_id: 'turn-b-1428',
        message_id: 'msg-b-1428', content: 'working…', seq: ++seq,
      })
    })
    expect(useChatStore.getState().isStreaming).toBe(true)
    act(() => useChatStore.getState().drainOutboundQueue())
    expect(sentMessageFrames(), 'held while the active chat streams').toEqual([])
    // …and fire when B's turn completes.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SID_B, turn_id: 'turn-b-1428', message_id: 'msg-b-1428',
        stats: { tokens: 1, cost: 0 }, seq: ++seq,
      })
    })
    const frames = sentMessageFrames()
    expect(frames).toHaveLength(1)
    expect(frames[0]).toMatchObject({ type: 'message', content: TEXT })
    expect(frames[0].session_id, '#1428 Expected: the drain fires into the conversation the message was queued in').toBe(SID_A)
    expect(frames.filter((f) => f.session_id === SID_B)).toEqual([])
  })
})

describe('issue #1428 — a refused send never clears the composer text', () => {
  it('a send refused by the workspace-entry guard leaves the composer text untouched', async () => {
    // Workspace entry is resolving: blockSendForWorkspaceEntry refuses the
    // send (the exact #1428 "workspace-entry guard returns after the composer
    // is cleared" path).
    act(() => useSessionStore.setState({ resolvingSessionForWorkspace: { [AMBIENT_WORKSPACE]: true } }))
    const { result } = renderHook(() => useOmnipusRuntime())
    act(() => result.current.thread.composer.setText(TEXT))
    await act(async () => { await result.current.thread.composer.send() })

    expect(send.mock.calls, 'a refused send puts nothing on the wire').toEqual([])
    expect(result.current.thread.composer.getState().text,
      '#1428 Expected: text is never cleared when a send is refused').toBe(TEXT)
  })

  it('guard: an accepted send still clears the composer', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    act(() => result.current.thread.composer.setText(TEXT))
    await act(async () => { await result.current.thread.composer.send() })
    expect(sentMessageFrames()).toHaveLength(1)
    expect(result.current.thread.composer.getState().text).toBe('')
  })
})

describe('issue #1428 — an unconfirmed message becomes recoverable after a correlated deadline, without stopping the agent', () => {
  it('first send with an open socket and no receipt shows the unconfirmed Retry state after the deadline', () => {
    vi.useFakeTimers()
    act(() => useSessionStore.getState().setActiveSession(null, OWNER))
    act(() => useChatStore.getState().sendMessage(TEXT, { clientMessageId: 'first-1428' }))
    expect(send).toHaveBeenCalledTimes(1)
    expect(userContents()).toEqual([TEXT])

    act(() => vi.advanceTimersByTime(DELIVERY_CONFIRM_DEADLINE_MS))

    const state = useChatStore.getState()
    expect(state.pendingFirstSend?.status,
      '#1428 Expected: unconfirmed after the correlated deadline (drives "Delivery not confirmed · Retry")').toBe('unconfirmed')
    const first = state.messages.find((m) => m.role === 'user')
    expect(first?.firstSendStatus).toBe('unconfirmed')
    expect(state.isStreaming).toBe(false)
    expect(userContents()).toEqual([TEXT])
    // "without stopping the agent": the deadline may only surface the state —
    // it must not put anything further on the wire (no cancel, no resend).
    expect(send).toHaveBeenCalledTimes(1)
    expect(sentCancelFrames()).toEqual([])
  })

  it('a known-session follow-up with an open socket and no receipt becomes recoverable after the deadline', () => {
    vi.useFakeTimers()
    act(() => useChatStore.getState().sendMessage(TEXT, { clientMessageId: 'followup-1428' }))
    expect(send).toHaveBeenCalledTimes(1)
    expect(useChatStore.getState().messages.find((m) => m.role === 'user')?.deliveryStatus).toBe('sending')

    act(() => vi.advanceTimersByTime(DELIVERY_CONFIRM_DEADLINE_MS))

    const state = useChatStore.getState()
    const followUp = state.messages.find((m) => m.role === 'user')
    // UserDeliveryState has no 'unconfirmed' member (lib/userDeliveryState.ts):
    // 'failed' is the one recoverable terminal state, and it is what the
    // existing false-transport path already maps to the Retry affordance.
    expect(followUp?.deliveryStatus,
      '#1428 Expected: recoverable Retry state after the correlated deadline').toBe('failed')
    expect(userContents()).toEqual([TEXT])
    expect(state.isStreaming).toBe(false)
    expect(send).toHaveBeenCalledTimes(1)
    expect(sentCancelFrames()).toEqual([])
  })

  it('an unconfirmed mid-turn steer past the deadline never stops the running turn', () => {
    vi.useFakeTimers()
    // An announced, running turn in the same conversation.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_state', session_id: SID_A, user_id: 'u-1428', pending_approvals: [],
        active_turn: { turn_id: 'turn-a-running', agent_id: OWNER, started_at: '2026-10-23T08:00:00Z' },
        emitted_at: '2026-10-23T08:00:00Z',
      })
      useChatStore.getState().handleFrame({
        type: 'token', session_id: SID_A, agent_id: OWNER, turn_id: 'turn-a-running',
        message_id: 'msg-a-running', content: 'still working', seq: ++seq,
      })
    })
    expect(useChatStore.getState().isStreaming).toBe(true)
    act(() => useChatStore.getState().sendMessage(STEER, { clientMessageId: 'steer-1428' }))
    // The announce/token above are inbound frames; the steer is the only
    // outbound call so far.
    expect(send).toHaveBeenCalledTimes(1)
    expect(sentMessageFrames()[0]).toMatchObject({ type: 'message', content: STEER, session_id: SID_A })
    const steer = useChatStore.getState().messages.filter((m) => m.role === 'user').at(-1)
    expect(steer?.content).toBe(STEER)
    expect(steer?.deliveryStatus).toBe('sending')

    act(() => vi.advanceTimersByTime(DELIVERY_CONFIRM_DEADLINE_MS))

    const state = useChatStore.getState()
    const steerAfter = state.messages.filter((m) => m.role === 'user').at(-1)
    expect(steerAfter?.deliveryStatus, '#1428 Expected: the steer becomes recoverable').toBe('failed')
    // "without stopping the agent": the running turn keeps running.
    expect(state.isStreaming, '#1428 Expected: the agent is not stopped').toBe(true)
    expect(state.sessionsById[SID_A]?.activeTurnId).toBe('turn-a-running')
    expect(send).toHaveBeenCalledTimes(1)
    expect(sentCancelFrames()).toEqual([])
  })

  it('guard: a receipt arriving just before the deadline keeps the message confirmed', () => {
    vi.useFakeTimers()
    act(() => useSessionStore.getState().setActiveSession(null, OWNER))
    act(() => useChatStore.getState().sendMessage(TEXT, { clientMessageId: 'first-receipt-1428' }))
    expect(useChatStore.getState().pendingFirstSend).not.toBeNull()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'message_status', session_id: SID_A, client_message_id: 'first-receipt-1428',
        state: 'received', seq: ++seq,
      })
    })
    act(() => vi.advanceTimersByTime(DELIVERY_CONFIRM_DEADLINE_MS))
    expect(useChatStore.getState().pendingFirstSend).toBeNull()
    const confirmed = useChatStore.getState().sessionsById[SID_A]?.messagesById['first-receipt-1428']
    expect(confirmed?.deliveryStatus).toBe('received')
    expect(sentCancelFrames()).toEqual([])
  })
})

describe('issue #1428 — an idle /cancel tells the user there is nothing to cancel', () => {
  const cancelCommand = [{
    name: 'cancel', label: '/cancel', delivery: 'client' as const,
    description: 'Stop this session and every helper under it', available_while_streaming: true,
  }]

  function wireSlashHooks() {
    return renderHook(() => {
      const runtime = useOmnipusRuntime()
      const streaming = useChatStore.getState().isStreaming
      const cancel = useCancelState(streaming, useChatStore.getState().cancelStream)
      const menu = useSlashMenu({
        isStreaming: streaming, isReplaying: false, inputEnabled: true,
        composerRuntime: runtime.thread.composer,
        appendMessage: useChatStore.getState().appendMessage,
        activateStop: cancel.cancelUnconditional,
        cancelIfStreaming: cancel.cancelAllTreeScoped,
        sendRedirectFrame: () => { throw new Error('Unexpected redirect during idle-/cancel probe') },
      })
      return { runtime, menu }
    }, { wrapper: ({ children }) => createElement(QueryClientProvider, { client: queryClient }, children) })
  }

  function visibleFeedbackTexts(): string[] {
    const toasts = useUiStore.getState().toasts.map((t) => t.message)
    const systemMessages = useChatStore.getState().messages
      .filter((m) => m.role === 'system')
      .map((m) => m.content)
    return [...toasts, ...systemMessages]
  }

  it('idle /cancel shows the "nothing to cancel" feedback, consumes the command, and keeps the D9 tree cancel', () => {
    queryClient.setQueryData(['commands', 'web'], cancelCommand)
    queryClient.setQueryData(['skills'], [])
    queryClient.setQueryData(['agents'], [])
    const { result } = wireSlashHooks()
    expect(useChatStore.getState().isStreaming).toBe(false)
    act(() => result.current.runtime.thread.composer.setText('/cancel'))
    act(() => { expect(result.current.menu.interceptClientCommand()).toBe(true) })

    // Chief brief / #1428: an idle /cancel must give visible feedback.
    expect(visibleFeedbackTexts().some((text) => /nothing to cancel/i.test(text)),
      `expected visible feedback mentioning "nothing to cancel", got: ${JSON.stringify(visibleFeedbackTexts())}`).toBe(true)
    // The command is consumed either way (guard against "fixed" by re-inserting text).
    expect(result.current.runtime.thread.composer.getState().text).toBe('')
    // ADR-20260928 D9: /cancel stays the self-confirming tree cancel — the
    // feedback must not come from suppressing the stop.
    expect(sentCancelFrames()).toEqual([{ type: 'cancel', session_id: SID_A, scope: 'tree' }])
  })

  it('guard: a streaming session must NOT be told there is nothing to cancel', () => {
    queryClient.setQueryData(['commands', 'web'], cancelCommand)
    queryClient.setQueryData(['skills'], [])
    queryClient.setQueryData(['agents'], [])
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_state', session_id: SID_A, user_id: 'u-1428', pending_approvals: [],
        active_turn: { turn_id: 'turn-streaming', agent_id: OWNER, started_at: '2026-10-23T08:00:00Z' },
        emitted_at: '2026-10-23T08:00:00Z',
      })
      useChatStore.getState().handleFrame({
        type: 'token', session_id: SID_A, agent_id: OWNER, turn_id: 'turn-streaming',
        message_id: 'msg-streaming', content: 'reply', seq: ++seq,
      })
    })
    expect(useChatStore.getState().isStreaming).toBe(true)
    const { result } = wireSlashHooks()
    act(() => result.current.runtime.thread.composer.setText('/cancel'))
    act(() => { expect(result.current.menu.interceptClientCommand()).toBe(true) })
    expect(visibleFeedbackTexts().some((text) => /nothing to cancel/i.test(text))).toBe(false)
  })
})
