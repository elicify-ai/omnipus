// #1428 founder requirements Q1/Q2, chief dispatch 2026-10-25.
// Independent oracle: issue #1428 Expected + founder Q67 (15 s).
// Chief froze the exact row copy as "Delivery not confirmed · Retry" for
// both deadline paths. Toast wording is unspecified: require a NEW visible
// notification at expiry, not the earlier queued-message notification.
//
// REAL: chat/session/connection/UI stores, production VirtualUserMessageRow,
// RowDeliveryStatus and ToastContainer. The fixture merely subscribes the
// production row to actual foreground messages; it does not invent a status.
// DOUBLES: transport sender and fake clock only. No real sleeps.
// Cases: 14,999/15,000 ms boundary; A→B before queued expiry; reconnect/drain
// in B and A after failure; mounted non-first message copy. No existing test
// is changed. RED versus post-fix coverage is reported from the captured head.
// GREEN/mutation proof and independent verdict remain with CHECK.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import { ToastContainer } from '@/components/ui/toast-container'
import { queryClient } from '@/lib/queryClient'
import type { MessageFrame } from '@/lib/api/generated/asyncapi-types'
import type { components } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'

const SID_A = 'session_01JCE1428DEADLINECHATA0001'
const SID_B = 'session_01JCE1428DEADLINECHATB0001'
const WORKSPACE = 'workspace_01JCE1428DEADLINE00001'
const OWNER = 'mia'
const DEADLINE_MS = 15_000 // Founder Q67, not an implementation-derived constant.
const COPY = 'Delivery not confirmed · Retry' // Issue #1428 + chief Q1 A copy ruling.
const QUEUED_ID = 'q1-offline-deadline-a'
const FOLLOWUP_ID = 'q2-followup-deadline-a'
const QUEUED_TEXT = 'Q1 keep this expired queued message in A'
const FOLLOWUP_TEXT = 'Q2 follow-up delivery confirmation probe'
let send: ReturnType<typeof vi.fn>

function seedSession(id: string, owner: string) {
  const session: Pick<components['schemas']['Session'], 'id' | 'agent_id' | 'workspace_id'> = {
    id, agent_id: owner, workspace_id: WORKSPACE,
  }
  queryClient.setQueryData(['session-detail', id], { session })
}

// Thin render fixture, not an alternative delivery implementation: all row
// content/status/retry behavior is the production component's own behavior.
function CurrentRows() {
  const messages = useChatStore((state) => state.messages)
  return <div data-testid="current-deadline-rows">{messages.filter((message) => message.role === 'user').map((message) => (
    <VirtualUserMessageRow key={message.id} message={message} skills={[]} commandLabels={[]} agentName="Mia" latest />
  ))}</div>
}

function mountRows() {
  render(<QueryClientProvider client={queryClient}>
    <CurrentRows />
    <div data-testid="deadline-toasts"><ToastContainer /></div>
  </QueryClientProvider>)
}

function messageIn(sessionId: string, messageId: string) {
  return useChatStore.getState().sessionsById[sessionId]?.messagesById[messageId]
}

function messageFrames(): MessageFrame[] {
  return send.mock.calls.map(([frame]) => frame).filter((frame) => frame.type === 'message') as MessageFrame[]
}

function assertRetryRow(messageId: string) {
  const row = document.querySelector(`[data-message-id="${messageId}"]`)
  const status = row?.querySelector('[data-testid="user-message-delivery-status"]')
  const retry = row?.querySelector('[data-testid="user-message-retry"]')
  // Soft checks keep RED while still driving the forbidden automatic drains.
  expect.soft(row, 'the failed message remains visibly retained in its own chat').toBeVisible()
  expect.soft(status?.textContent ?? null, 'the mounted row uses the ruled honest deadline copy').toBe(COPY)
  expect.soft(retry, 'the retained row offers a visible delivery Retry control').toBeVisible()
  expect.soft(retry, 'delivery Retry is usable once connected').toBeEnabled()
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-25T12:00:00Z')) // Deterministic fixture epoch, not a timing oracle.
  queryClient.clear()
  localStorage.clear()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    seedSession(SID_A, OWNER)
    seedSession(SID_B, 'jim')
    useSessionStore.getState().setActiveSession(SID_A, OWNER)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
    send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({
      connection: { send, isConnected: true, connect: vi.fn(), disconnect: vi.fn() } as unknown as WsConnection,
      isConnected: true,
    })
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  localStorage.clear()
  vi.clearAllTimers()
  vi.useRealTimers()
})

describe('#1428 delivery deadline — founder Q1/Q2', () => {
  it('Q1: offline queue expires at 15 s in its own chat, shows Retry and a new toast, and never auto-sends after failure', () => {
    mountRows()
    act(() => {
      useConnectionStore.setState({ isConnected: false })
      useChatStore.getState().sendMessage(QUEUED_TEXT, { clientMessageId: QUEUED_ID })
    })
    expect(useChatStore.getState().outboundQueue, 'instrument: the real offline path retained one queued message').toHaveLength(1)
    expect(messageFrames(), 'an offline queue is not a successful send').toEqual([])
    const earlierToastIds = new Set(useUiStore.getState().toasts.map((toast) => toast.id))
    act(() => useSessionStore.getState().setActiveSession(SID_B, 'jim'))
    act(() => vi.advanceTimersByTime(DEADLINE_MS - 1))
    expect(messageIn(SID_A, QUEUED_ID)?.deliveryStatus, 'the deadline must not fire a millisecond early').not.toBe('failed')
    expect(useUiStore.getState().toasts.filter((toast) => !earlierToastIds.has(toast.id))).toEqual([])

    act(() => vi.advanceTimersByTime(1))
    expect.soft(messageIn(SID_A, QUEUED_ID), 'expiry must retain the original text as failed in A, even while B is active').toMatchObject({
      id: QUEUED_ID, role: 'user', content: QUEUED_TEXT, deliveryStatus: 'failed',
    })
    expect.soft(messageIn(SID_B, QUEUED_ID), 'the expired A message must never be placed in B').toBeUndefined()
    expect.soft(messageFrames(), 'expiry itself never sends the message').toEqual([])
    const newToasts = useUiStore.getState().toasts.filter((toast) => !earlierToastIds.has(toast.id))
    expect.soft(newToasts, 'one expiry creates a NEW notification, not the earlier queue toast').toHaveLength(1)
    act(() => {
      useSessionStore.getState().setActiveSession(SID_A, OWNER)
      useConnectionStore.getState().setConnected(true)
    })
    assertRetryRow(QUEUED_ID)
    // Toast sentence is not prescribed. Assert the state→DOM rendering
    // invariant for the new notification; never copy it into a text oracle.
    for (const toast of newToasts) {
      expect.soft(toast.message, 'the deadline notification identifies delivery rather than an unrelated event').toMatch(/deliver|confirm|send|sent/i)
      expect.soft(toast.message, 'the deadline notification honestly identifies failure or lack of confirmation').toMatch(/not|unconfirmed|fail|couldn.t|unable/i)
      expect.soft(within(screen.getByTestId('deadline-toasts')).queryByText(toast.message, { exact: true }),
        'the new deadline notification is rendered when its own chat is shown').toBeVisible()
    }

    act(() => useSessionStore.getState().setActiveSession(SID_B, 'jim'))
    expect.soft(useChatStore.getState().isStreaming, 'instrument: B is idle before the forbidden drain').toBe(false)
    expect.soft(useChatStore.getState().isReplaying, 'instrument: replay cannot mask the forbidden drain').toBe(false)
    act(() => useChatStore.getState().drainOutboundQueue())
    expect.soft(useChatStore.getState().isStreaming, 'an expired A message must not start a turn in B').toBe(false)
    expect.soft(useChatStore.getState().isReplaying, 'the forbidden drain must not start replay').toBe(false)
    expect.soft(messageFrames(), 'reconnect/drain in B must not auto-deliver the expired A message').toEqual([])
    act(() => {
      vi.advanceTimersByTime(4 * DEADLINE_MS) // Four further deadline windows, entirely on the fake clock.
      useChatStore.getState().drainOutboundQueue()
      useSessionStore.getState().setActiveSession(SID_A, OWNER)
      useChatStore.getState().drainOutboundQueue()
    })
    expect.soft(messageFrames(), 'neither later drains nor returning to A grants automatic Retry').toEqual([])
    expect.soft(messageIn(SID_A, QUEUED_ID), 'automatic drains must not discard or un-fail A retained message').toMatchObject({
      id: QUEUED_ID, role: 'user', content: QUEUED_TEXT, deliveryStatus: 'failed',
    })
    expect.soft(messageIn(SID_B, QUEUED_ID)).toBeUndefined()
    assertRetryRow(QUEUED_ID)
    // An explicit activation is different from every automatic drain above:
    // the production row's real Retry handler may now send the retained A text.
    act(() => screen.getByTestId('user-message-retry').click())
    expect(messageFrames(), 'only the user Retry sends the expired message').toHaveLength(1)
    expect(messageFrames()[0]).toMatchObject({
      type: 'message', content: QUEUED_TEXT, session_id: SID_A, client_message_id: QUEUED_ID,
    })
    expect(messageFrames().filter((frame) => frame.session_id === SID_B), 'manual Retry also stays out of B').toEqual([])
  })

  it('Q2: a non-first message timeout shows Delivery not confirmed · Retry in the real mounted user row', () => {
    act(() => useChatStore.getState().appendMessage({
      id: 'q2-prior-confirmed-user', session_id: SID_A, role: 'user', content: 'Earlier confirmed message in A',
      timestamp: '2026-10-25T11:59:00Z', status: 'done', deliveryStatus: 'received',
    }))
    mountRows()
    act(() => useChatStore.getState().sendMessage(FOLLOWUP_TEXT, { clientMessageId: FOLLOWUP_ID }))
    expect(messageFrames()).toHaveLength(1)
    expect(messageFrames()[0]).toMatchObject({ type: 'message', content: FOLLOWUP_TEXT, session_id: SID_A })
    expect(useChatStore.getState().pendingFirstSend, 'instrument: this is the existing-chat follow-up path, not first-send recovery').toBeNull()
    expect(screen.getByText(FOLLOWUP_TEXT, { exact: true }), 'instrument: the real production user row is mounted before the deadline').toBeVisible()
    expect(messageIn(SID_A, FOLLOWUP_ID)?.deliveryStatus).toBe('sending')
    act(() => vi.advanceTimersByTime(DEADLINE_MS - 1))
    expect(messageIn(SID_A, FOLLOWUP_ID)?.deliveryStatus, 'follow-up still waits immediately before Q67').toBe('sending')
    expect(screen.queryByTestId('user-message-retry')).toBeNull()

    act(() => vi.advanceTimersByTime(1))
    expect.soft(messageIn(SID_A, FOLLOWUP_ID)?.deliveryStatus, 'the known-chat message becomes retryable at exactly 15 s').toBe('failed')
    assertRetryRow(FOLLOWUP_ID)
    expect.soft(useChatStore.getState().pendingFirstSend).toBeNull()
    expect.soft(messageFrames(), 'a timeout does not automatically retry or retarget the follow-up').toHaveLength(1)
    expect.soft(send.mock.calls.filter(([frame]) => frame.type === 'cancel'), 'delivery timeout does not stop the agent').toEqual([])
  })
})
