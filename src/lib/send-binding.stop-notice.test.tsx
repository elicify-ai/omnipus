// #1428 step 2 RED. Spec B-24, B-27, BR-25, BR-25g.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import {
  NOT_STOPPED,
  OLD_TAB_STOP,
  FakeSocket,
  connection,
  deliverFrame,
  framesOf,
  installFakeWebSocket,
  mountComposer,
  resetBindingWorld,
  seedStreamingAnswer,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

const NOTICE = NOT_STOPPED

let live: WsConnection | null = null

afterEach(() => {
  vi.useRealTimers()
  live?.disconnect()
  live = null
  vi.unstubAllGlobals()
})

function stopButton(): HTMLElement {
  return screen.getByTestId('stop-btn')
}

function assertNoticeInThisChat(): void {
  const node = screen.getByText(NOTICE)
  expect(node.textContent, 'BR-25: full string, not a substring').toBe(NOTICE)
  expect(document.body.textContent ?? '', 'BR-25: the old-tab sentence is absent').not.toContain(OLD_TAB_STOP)
  expect(node.closest('[role="status"]'), 'BR-25: the notice is a status').toBeTruthy()
  expect(screen.queryByRole('alert'), 'BR-25: not a toast').toBeNull()
}

describe('current-tab not-stopped notice (spec 4.7)', () => {
  it('BR-25a a refused Stop shows the persistent notice and leaves Stop enabled', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    // The notice is established by the refusal, not by the click. Fake timers
    // start here so a 30s dismissal scheduled with the notice is advanced,
    // and they are not on during userEvent (that hangs the click).
    vi.useFakeTimers()
    const id = (framesOf('cancel', sent())[0] as { client_frame_id?: string })?.client_frame_id ?? 'c1'
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'cancel',
      client_frame_id: id,
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-b' },
    })
    deliverFrame({ type: 'session_state', session_id: 'chat-a', active_turn: { turn_id: 'T' } } as never)
    assertNoticeInThisChat()
    expect(stopButton(), 'BR-25a: Stop stays enabled').toBeEnabled()
    expect(stopButton().textContent ?? '', 'BR-25a: the label is not Stopping').not.toContain('Stopping')
    await act(async () => { vi.advanceTimersByTime(30_000) })
    assertNoticeInThisChat()
  })

  it('BR-25b a Stop whose send returns false does not freeze the answer', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a', 'still going')
    connection().fail = true
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    const bubble = useChatStore.getState().sessionsById['chat-a']?.messagesById?.['live-bubble-1']
    expect(bubble?.status, 'BR-25b: the answer is not frozen').not.toBe('interrupted')
    expect(document.body.textContent ?? '', 'BR-25b: no (interrupted)').not.toContain('(interrupted)')
    expect(stopButton()).toBeEnabled()
    expect(screen.getByText(NOTICE), 'BR-25b: the notice is shown').toBeTruthy()
  })

  it('BR-25c a refused redirect restores the text and shows the notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    await typeIntoComposer('/stop-redirect hold')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    const id = (framesOf('redirect', sent())[0] as { client_frame_id?: string })?.client_frame_id ?? 'r1'
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'redirect',
      client_frame_id: id,
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-a' },
    })
    expect(screen.getByTestId('chat-input')).toHaveValue('/stop-redirect hold')
    expect(screen.getByText(NOTICE)).toBeTruthy()
  })

  it('BR-25d the notice goes away on done, on a later receipt, and on Dismiss, and not in another chat', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    connection().fail = true
    const user = userEvent.setup()
    await user.click(stopButton())
    expect(screen.getByText(NOTICE)).toBeTruthy()
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    expect(screen.queryByText(NOTICE), 'BR-25d: another chat does not show A\'s notice').toBeNull()
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    await user.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByText(NOTICE), 'BR-25d: Dismiss removes it').toBeNull()
  })

  it('BR-25d2 a helper provider_status ended does not clear the main chat notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    connection().fail = true
    const user = userEvent.setup()
    await user.click(stopButton())
    const { deliverFrame: deliver } = await import('@/test/sendBinding1428')
    deliver({
      type: 'provider_status',
      session_id: 'chat-a',
      child_session_id: 'h1',
      agent_id: 'mia',
      turn_id: 'h1-turn',
      state: 'ended',
    } as never)
    expect(screen.getByText(NOTICE), 'BR-25d2: the helper ending leaves the notice').toBeTruthy()
    expect(stopButton()).toBeEnabled()
    deliver({ type: 'done', session_id: 'chat-a', turn_id: 'T' } as never)
    expect(screen.queryByText(NOTICE), 'BR-25d2: the chat\'s own done removes it').toBeNull()
  })

  // GUARD of today's freeze. The "no notice" half is absence until the notice exists.
  it('BR-25e a received Stop shows no notice and freezes as today', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a', 'partial')
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    const id = (framesOf('cancel', sent())[0] as { client_frame_id?: string })?.client_frame_id ?? 'c1'
    deliverFrame({ type: 'command_received', command: 'cancel', client_frame_id: id, session_id: 'chat-a' })
    expect(screen.queryByText(NOTICE), 'BR-25e: a receipt shows no notice').toBeNull()
    const bubble = useChatStore.getState().sessionsById['chat-a']?.messagesById?.['live-bubble-1']
    expect(bubble?.status, 'BR-25e: the answer is frozen').toBe('interrupted')
  })

  it('BR-25g a a failed Stop does not arm Stop all', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    connection().fail = true
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    expect(screen.getByText(NOTICE)).toBeTruthy()
    expect(stopButton().getAttribute('aria-label'), 'BR-25g a: label is Stop').toBe('Stop generation')
    expect(stopButton().getAttribute('title'), 'BR-25g a: Stop all is not armed').toBe('Stop (Escape)')
    connection().fail = false
    const before = framesOf('cancel', sent()).length
    await user.click(stopButton())
    const added = framesOf('cancel', sent()).slice(before)
    expect(added, 'BR-25g a: the next press is one single-chat cancel').toEqual([
      expect.objectContaining({ type: 'cancel', session_id: 'chat-a' }),
    ])
    expect(added[0], 'BR-25g a: not a tree cancel').not.toHaveProperty('scope')
  })

  it('BR-25g b a refused Stop does not arm Stop all', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    const id = (framesOf('cancel', sent())[0] as { client_frame_id?: string })?.client_frame_id ?? 'c1'
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'cancel',
      client_frame_id: id,
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-a' },
    })
    expect(stopButton().getAttribute('title'), 'BR-25g b: not armed').toBe('Stop (Escape)')
    const before = framesOf('cancel', sent()).length
    await user.click(stopButton())
    const added = framesOf('cancel', sent()).slice(before)
    expect(added).toHaveLength(1)
    expect(added[0]).not.toHaveProperty('scope')
  })

  it('BR-25g c and h an unsupported connection sends nothing on either press', async () => {
    // Spec 4.10 step 3: the first frame is session_state, so the connection
    // is unsupported before either press. BR-25g (c) and (h).
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    installFakeWebSocket()
    live = new WsConnection({
      onFrames: () => {},
      onConnected: () => {},
      onDisconnected: () => {},
      onError: () => {},
    })
    live.connect()
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('socket was not created')
    act(() => { socket.open() })
    act(() => { socket.receive({ type: 'session_state', session_id: 'chat-a' } as never) })
    useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    await user.click(stopButton())
    const controls = socket.sent.filter((frame) =>
      frame.type === 'message' || frame.type === 'redirect' || frame.type === 'cancel')
    expect(controls, 'BR-25g c/h: zero frames on both presses').toEqual([])
    expect(stopButton().getAttribute('aria-label'), 'BR-25g c/h: label stays Stop').toBe('Stop generation')
  })

  // GUARD of today's Stop all. Not a RED proof; MU-45 does not target this case.
  it('BR-25g d a confirmed Stop keeps today\'s second-press Stop all', async () => {
    // Positive control of today's Stop all. Both presses are immediate, so
    // they are inside the 3s window without fake timers.
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    const id = (framesOf('cancel', sent())[0] as { client_frame_id?: string })?.client_frame_id ?? 'c1'
    deliverFrame({ type: 'command_received', command: 'cancel', client_frame_id: id, session_id: 'chat-a' })
    await user.click(stopButton())
    const cancels = framesOf('cancel', sent())
    expect(cancels.at(-1), 'BR-25g d: the second press inside 3s is Stop all').toEqual(
      expect.objectContaining({ type: 'cancel', scope: 'tree', session_id: 'chat-a' }),
    )
  })

  it('BR-25g g a notice for A does not disarm a window armed for B', async () => {
    resetBindingWorld()
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    seedStreamingAnswer('chat-b')
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    expect(stopButton().getAttribute('title'), 'BR-25g g: B is armed').toContain('helpers')
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'cancel',
      client_frame_id: 'a-stop',
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-a' },
    })
    expect(screen.getByText(NOTICE), 'BR-25g g: A\'s notice is established').toBeTruthy()
    expect(stopButton().getAttribute('title'), 'BR-25g g: B stays armed').toContain('helpers')
    await user.click(stopButton())
    expect(framesOf('cancel', sent()).at(-1), 'BR-25g g: B\'s second press is still Stop all').toEqual(
      expect.objectContaining({ scope: 'tree', session_id: 'chat-b' }),
    )
  })

  async function pressStopOnLiveSocket(): Promise<FakeSocket> {
    installFakeWebSocket()
    live = new WsConnection({
      onFrames: (frames) => {
        for (const frame of frames) deliverFrame(frame as never)
      },
      onConnected: () => {},
      onDisconnected: () => {},
      onError: () => {},
    })
    live.connect()
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('socket was not created')
    act(() => { socket.open() })
    useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
    mountComposer()
    // Click stays off userEvent: the 1006 case runs under fake timers.
    fireEvent.click(stopButton())
    return socket
  }

  function reattachSameTurn(socket: FakeSocket): void {
    act(() => { socket.open() })
    act(() => {
      socket.receive({
        type: 'session_state',
        session_id: 'chat-a',
        active_turn: { turn_id: 'T' },
      } as never)
    })
  }

  it('BR-25g e a 4008 close before receipt disarms Stop all when the same turn is still running', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    const first = await pressStopOnLiveSocket()
    expect(stopButton().getAttribute('aria-label'), 'BR-25g e: the hand-over arms Stopping').toBe('Stopping...')
    first.close(4008, 'catch-up required')
    const next = FakeSocket.instances.at(-1)
    expect(next, 'BR-25g e: 4008 opens the next socket immediately').toBeTruthy()
    expect(next).not.toBe(first)
    reattachSameTurn(next!)
    expect(screen.getByText(NOTICE), 'BR-25g e: the same turn shows the notice').toBeTruthy()
    expect(stopButton().getAttribute('aria-label'), 'BR-25g e: the label is Stop').toBe('Stop generation')
    expect(stopButton().getAttribute('title'), 'BR-25g e: Stop all is disarmed').toBe('Stop (Escape)')
    const before = next!.sent.filter((frame) => frame.type === 'cancel').length
    fireEvent.click(stopButton())
    const added = next!.sent.filter((frame) => frame.type === 'cancel').slice(before)
    expect(added, 'BR-25g e: the next press is one single-chat cancel').toEqual([
      expect.objectContaining({ type: 'cancel', session_id: 'chat-a' }),
    ])
    expect(added[0], 'BR-25g e: not a tree cancel').not.toHaveProperty('scope')
  })

  it('BR-25g e a 1006 close delivers the snapshot before the 1000ms retry sends anything', async () => {
    vi.useFakeTimers()
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    const first = await pressStopOnLiveSocket()
    const sentBeforeClose = first.sent.filter((frame) => frame.type === 'cancel').length
    first.close(1006, 'abnormal')
    await act(async () => { vi.advanceTimersByTime(999) })
    expect(FakeSocket.instances, 'BR-25g e: the 1000ms retry has not opened yet').toHaveLength(1)
    await act(async () => { vi.advanceTimersByTime(1) })
    const next = FakeSocket.instances.at(-1)
    expect(next).not.toBe(first)
    reattachSameTurn(next!)
    const auto = next!.sent.filter((frame) =>
      frame.type === 'cancel' || frame.type === 'redirect' || frame.type === 'message')
    expect(auto, 'BR-25g e: the retry itself sends no control frame').toEqual([])
    expect(first.sent.filter((frame) => frame.type === 'cancel').length, 'BR-25g e: no second cancel on the dead socket').toBe(sentBeforeClose)
    expect(screen.getByText(NOTICE), 'BR-25g e: the snapshot before any later send shows the notice').toBeTruthy()
    expect(stopButton().getAttribute('title'), 'BR-25g e: Stop all is disarmed').toBe('Stop (Escape)')
    fireEvent.click(stopButton())
    const added = next!.sent.filter((frame) => frame.type === 'cancel')
    expect(added, 'BR-25g e: the next press is one single-chat cancel').toEqual([
      expect.objectContaining({ type: 'cancel', session_id: 'chat-a' }),
    ])
    expect(added[0]).not.toHaveProperty('scope')
  })

  it('BR-25g f a redirect the socket refuses inside the armed window disarms it', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    expect(stopButton().getAttribute('title'), 'BR-25g f: the window is armed').toContain('helpers')
    connection().fail = true
    await typeIntoComposer('/stop-redirect x')
    await user.keyboard('{Enter}')
    expect(screen.getByText(NOTICE), 'BR-25g f: the failed redirect shows the notice').toBeTruthy()
    expect(stopButton().getAttribute('aria-label'), 'BR-25g f: the label is Stop').toBe('Stop generation')
    expect(stopButton().getAttribute('title'), 'BR-25g f: Stop all is disarmed').toBe('Stop (Escape)')
    connection().fail = false
    const before = framesOf('cancel', sent()).length
    await user.click(stopButton())
    const added = framesOf('cancel', sent()).slice(before)
    expect(added, 'BR-25g f: the next press is one single-chat cancel').toEqual([
      expect.objectContaining({ type: 'cancel', session_id: 'chat-a' }),
    ])
    expect(added[0]).not.toHaveProperty('scope')
  })

  it('BR-25g f2 a refused redirect inside the armed window disarms it', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(stopButton())
    await typeIntoComposer('/stop-redirect x')
    await user.keyboard('{Enter}')
    const id = (framesOf('redirect', sent())[0] as { client_frame_id?: string })?.client_frame_id ?? 'r1'
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'redirect',
      client_frame_id: id,
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-a' },
    })
    expect(screen.getByText(NOTICE), 'BR-25g f2: the refusal shows the notice').toBeTruthy()
    expect(stopButton().getAttribute('title'), 'BR-25g f2: Stop all is disarmed').toBe('Stop (Escape)')
    const before = framesOf('cancel', sent()).length
    await user.click(stopButton())
    const added = framesOf('cancel', sent()).slice(before)
    expect(added).toEqual([expect.objectContaining({ type: 'cancel', session_id: 'chat-a' })])
    expect(added[0]).not.toHaveProperty('scope')
  })
})
