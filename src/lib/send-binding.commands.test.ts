// #1428 step 2 RED. Spec 4.7 BR-23, BR-23e, BR-23f, BR-23g.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useDraftStore } from '@/store/chat/drafts'
import { useSessionStore } from '@/store/session'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import {
  CONN_FAILED,
  NOT_STOPPED,
  UNCERTAIN_LINE,
  FakeSocket,
  apiMocks,
  deliverFrame,
  framesOf,
  flushInbound,
  installFakeWebSocket,
  mountComposer,
  resetBindingWorld,
  seedStreamingAnswer,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

let live: WsConnection | null = null

afterEach(() => {
  vi.useRealTimers()
  live?.disconnect()
  live = null
  vi.unstubAllGlobals()
})

const EMITTED = '2026-11-18T00:00:00Z'

function seedTurn(sessionId: string, turnId: string): void {
  seedStreamingAnswer(sessionId)
  act(() => {
    useChatStore.setState((s) => ({
      sessionsById: {
        ...(s.sessionsById ?? {}),
        [sessionId]: {
          ...((s.sessionsById ?? {})[sessionId] ?? {}),
          activeTurnId: turnId,
          isStreaming: true,
        },
      },
    }))
  })
}

/** Spec 4.7: an open, supported connection. The command is sent on this socket. */
function openLive(): FakeSocket {
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
  act(() => { socket.receive({ type: 'server_capabilities', send_origin: true }) })
  useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
  return socket
}

function sessionSnapshot(sessionId: string, turnId?: string, bootId?: string) {
  return {
    type: 'session_state' as const,
    user_id: 'ada',
    pending_approvals: [] as [],
    session_id: sessionId,
    emitted_at: EMITTED,
    auto_approve_modifier: true,
    ...(bootId ? { boot_id: bootId } : {}),
    ...(turnId ? { active_turn: { turn_id: turnId, agent_id: 'mia', started_at: EMITTED } } : {}),
  }
}

/** Spec 4.7: close before any receipt, then the next connection's snapshot.
 *  Today's socket holds a session_state until catch_up_complete when an
 *  attach is in flight, and delivers the batch on the next animation frame.
 *  Both are the transport, not the reconciliation under test. */
async function closeThenSnapshot(first: FakeSocket, frame: ReturnType<typeof sessionSnapshot>): Promise<FakeSocket> {
  const before = FakeSocket.instances.length
  first.close(4008, 'catch-up required')
  const next = FakeSocket.instances.at(-1)
  expect(next, 'spec 4.7: the close opens the next connection').toBeTruthy()
  expect(next).not.toBe(first)
  expect(FakeSocket.instances.length, 'spec 4.7: one reconnect').toBe(before + 1)
  act(() => { next!.open() })
  act(() => { next!.receive({ type: 'server_capabilities', send_origin: true }) })
  act(() => { next!.receive(frame) })
  act(() => {
    next!.receive({
      type: 'catch_up_complete',
      session_id: frame.session_id,
      seq: 1,
      mode: 'snapshot',
    })
  })
  await flushInbound()
  return next!
}

function expectSnapshotApplied(sessionId: string): void {
  expect(
    useChatStore.getState().sessionsById[sessionId]?.autoApproveEffective,
    'spec 4.7: the reattach snapshot reached the reducer',
  ).toBe(true)
}

async function typeRedirect(text: string): Promise<void> {
  await typeIntoComposer(text)
  const user = userEvent.setup()
  await user.click(screen.getByTestId('chat-input'))
  await user.keyboard('{Enter}')
}

/** One change plus Enter. Not one keystroke per character — sixty of those blow the file timeout. */
async function submitTypedCommand(text: string): Promise<void> {
  const box = screen.getByTestId('chat-input')
  await act(async () => {
    fireEvent.change(box, { target: { value: text } })
    fireEvent.keyDown(box, { key: 'Enter', code: 'Enter' })
  })
}

async function waitForCommandList(): Promise<void> {
  await waitFor(() => {
    expect(apiMocks.fetchCommands.mock.calls.length, 'the command list was requested').toBeGreaterThan(0)
  })
  await act(async () => {
    const pending = apiMocks.fetchCommands.mock.results[0]?.value as Promise<unknown> | undefined
    if (pending && typeof pending.then === 'function') await pending
  })
}

describe('command records (spec 4.7)', () => {
  registerCommandReceiptCases()
  registerCommandFailureCases()
  registerSignedOutCommandCase()
})

function registerCommandReceiptCases(): void {
  it('BR-23a command_received drops the record and leaves nothing unsent', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a', 'running')
    mountComposer()
    await typeRedirect('/stop-redirect continue')
    const redirect = framesOf('redirect', sent())[0] as { client_frame_id?: string } | undefined
    expect(redirect, 'BR-23a: the redirect carries the id the receipt will name').toEqual(
      expect.objectContaining({
        type: 'redirect',
        session_id: 'chat-a',
        instruction: 'continue',
        origin: { kind: 'chat', session_id: 'chat-a' },
        client_frame_id: expect.stringMatching(/^.{1,128}$/),
      }),
    )
    const id = redirect?.client_frame_id ?? 'r1'
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'command_received', command: 'redirect', client_frame_id: id, session_id: 'chat-a' })
    expect(screen.queryByText(/not sent/i), 'BR-23a: a receipt leaves nothing unsent').toBeNull()
    expect(screen.queryByText('May not have been delivered'), 'BR-23a: no uncertain item').toBeNull()
  })

  it('BR-23b send_refused puts the text back in A', async () => {
    resetBindingWorld()
    mountComposer()
    await typeRedirect('/stop-redirect private A')
    const id = (framesOf('redirect', sent())[0] as { client_frame_id?: string })?.client_frame_id ?? 'r1'
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'redirect',
      client_frame_id: id,
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-a' },
    })
    expect(screen.getByTestId('chat-input'), 'BR-23b: the instruction is back in A').toHaveValue('/stop-redirect private A')
  })

  it('BR-23c a close before receipt keeps an uncertain item and does not resend', async () => {
    resetBindingWorld()
    const socket = openLive()
    mountComposer()
    await typeRedirect('/stop-redirect y')
    expect(socket.sent.some((frame) => frame.type === 'redirect'), 'BR-23c: the redirect was handed over').toBe(true)
    const next = await closeThenSnapshot(socket, sessionSnapshot('chat-a'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('May not have been delivered'), 'BR-23c: the close is uncertain').toBeTruthy()
    expect(screen.getByText(UNCERTAIN_LINE), 'BR-23c: the may-already line').toBeTruthy()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await navigator.clipboard.readText(), 'BR-23c: Copy is the command').toBe('/stop-redirect y')
    expect(
      next.sent.filter((frame) => frame.type === 'redirect' || frame.type === 'cancel' || frame.type === 'message'),
      'BR-23c: the next connection does not resend it',
    ).toEqual([])
  })

  it('BR-23d sixty commands without a receipt stay sixty items', async () => {
    resetBindingWorld()
    const socket = openLive()
    mountComposer()
    await waitForCommandList()
    for (let i = 0; i < 60; i++) {
      await submitTypedCommand(`/stop-redirect n${i}`)
    }
    const handed = socket.sent.filter((frame) => frame.type === 'redirect') as { instruction?: string }[]
    expect(handed, 'BR-23d: all sixty were handed over').toHaveLength(60)
    expect(
      handed.map((frame) => frame.instruction),
      'BR-23d: the sixty commands are sixty different instructions',
    ).toEqual(Array.from({ length: 60 }, (_, i) => `n${i}`))
    await closeThenSnapshot(socket, sessionSnapshot('chat-a'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('60 not sent'), 'BR-23d: none of the sixty is dropped').toBeTruthy()
  })

  it('BR-23c1 the same turn still running shows the notice and the uncertain item', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    expect(socket.sent.some((frame) => frame.type === 'redirect'), 'BR-23c1: the redirect was handed over').toBe(true)
    const next = await closeThenSnapshot(socket, sessionSnapshot('chat-a', 'T'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
    expect(screen.getByText(NOT_STOPPED), 'BR-23c1: the same turn shows the notice').toBeTruthy()
    expect(screen.getByTestId('stop-btn'), 'BR-23c1: Stop stays available').toBeEnabled()
    vi.useFakeTimers()
    await act(async () => { vi.advanceTimersByTime(30_000) })
    expect(screen.getByText(NOT_STOPPED), 'BR-23c1: still there after 30s').toBeTruthy()
    expect(
      next.sent.filter((frame) => frame.type === 'redirect'),
      'BR-23c1: no automatic redirect',
    ).toEqual([])
  })

  it('BR-23c2 no active turn means the item and no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    await closeThenSnapshot(socket, sessionSnapshot('chat-a'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('May not have been delivered'), 'BR-23c2: the uncertain item stays').toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c2: a finished turn shows no notice').toBeNull()
  })

  it('BR-23c3 a different turn id shows the item and no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    await closeThenSnapshot(socket, sessionSnapshot('chat-a', 'T2'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('May not have been delivered'), 'BR-23c3: the uncertain item stays').toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c3: a new turn is not the captured one').toBeNull()
  })

  it('BR-23c4 a helper target follows the same three outcomes', async () => {
    resetBindingWorld()
    const socket = openLive()
    act(() => { useSessionStore.getState().setActiveSession('helper-1', 'mia', 'core') })
    seedTurn('helper-1', 'H')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    await closeThenSnapshot(socket, sessionSnapshot('helper-1', 'H'))
    expectSnapshotApplied('helper-1')
    expect(screen.getByText(NOT_STOPPED), 'BR-23c4: a helper uses the same notice').toBeTruthy()
    expect(screen.getByText('May not have been delivered'), 'BR-23c4: the uncertain item stays').toBeTruthy()
  })

  it('BR-23c5 a restarted gateway that reuses the turn id still shows the notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    const handed = socket.sent.filter((frame) => frame.type === 'redirect').length
    expect(handed, 'BR-23c5: the redirect was handed over').toBe(1)
    const before = FakeSocket.instances.length
    socket.close(4008, 'catch-up required')
    const next = FakeSocket.instances.at(-1)
    expect(next).not.toBe(socket)
    expect(FakeSocket.instances.length).toBe(before + 1)
    act(() => { next!.open() })
    act(() => { next!.receive({ type: 'server_capabilities', send_origin: true }) })
    act(() => { next!.receive(sessionSnapshot('chat-a', undefined, 'boot-2')) })
    act(() => { next!.receive(sessionSnapshot('chat-a', 'T', 'boot-2')) })
    act(() => {
      next!.receive({
        type: 'catch_up_complete',
        session_id: 'chat-a',
        seq: 1,
        boot_id: 'boot-2',
        mode: 'snapshot',
      })
    })
    await flushInbound()
    expectSnapshotApplied('chat-a')
    expect(screen.getByText(NOT_STOPPED), 'BR-23c5: a reused id still shows the notice').toBeTruthy()
    expect(screen.getByText('May not have been delivered'), 'BR-23c5: the uncertain item stays').toBeTruthy()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await navigator.clipboard.readText(), 'BR-23c5: Copy is the command').toBe('/stop-redirect y')
    expect(
      next!.sent.filter((frame) => frame.type === 'redirect' || frame.type === 'cancel' || frame.type === 'message'),
      'BR-23c5: nothing is sent automatically',
    ).toEqual([])
  })

}

function registerSignedOutCommandCase(): void {
  it('BR-18 sign-out forgets a pending command, and a late refusal restores nothing', async () => {
    // Spec BR-18 line 819: a command record in flight, then sign out, then it
    // completes. Spec 4.7: sign-out clears the record with everything else, so a
    // later send_refused matches nothing (4.8, no matching record: no restore).
    resetBindingWorld()
    const first = mountComposer({ sidebar: true })
    await typeRedirect('/stop-redirect continue')
    const records = Object.values(useDraftStore.getState().commands)
    expect(records, 'BR-18: the typed command left one pending record').toHaveLength(1)
    expect(records[0]?.text, 'BR-18: the record is the typed command').toBe('/stop-redirect continue')
    const id = records[0]?.clientFrameId ?? ''
    const handed = sent().length
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Open user menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out' }))
    expect(useDraftStore.getState().commands, 'BR-18: sign-out leaves no command record').toEqual({})
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'redirect',
      client_frame_id: id,
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-a' },
    })
    expect(useDraftStore.getState().commands, 'BR-18: the late refusal does not recreate the record').toEqual({})
    expect(useDraftStore.getState().drafts['chat-a']?.text ?? '', 'BR-18: the late refusal restores no text').toBe('')
    expect(screen.queryByText(NOT_STOPPED), 'BR-18: the late refusal shows no notice').toBeNull()
    expect(sent().length, 'BR-18: the late refusal sends no frame').toBe(handed)
    first.unmount()
    mountComposer()
    expect(screen.getByTestId('chat-input'), 'BR-18: nothing reappears').toHaveValue('')
    expect(screen.queryByText('1 not sent'), 'BR-18: the Unsent list is gone').toBeNull()
    expect(screen.queryByText(NOT_STOPPED), 'BR-18: no notice after the page returns').toBeNull()
  })
}

function registerCommandFailureCases(): void {
  it('BR-23e an OPEN socket that throws restores the text once and does not wait for a close', async () => {
    resetBindingWorld()
    installFakeWebSocket()
    const conn = new WsConnection({
      onFrames: () => {},
      onConnected: () => {},
      onDisconnected: () => {},
      onError: () => {},
    })
    conn.connect()
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('socket missing')
    act(() => { socket.open() })
    act(() => { socket.receive({ type: 'server_capabilities', send_origin: true }) })
    // Spec 4.7 / BR-23e: a throw is one attempt. A later timer or an unrelated
    // inbound frame must not call send again.
    let attempts = 0
    socket.send = () => {
      attempts += 1
      throw new Error('OPEN socket send threw')
    }
    useConnectionStore.setState({ connection: conn as never, isConnected: true })
    mountComposer()
    await typeIntoComposer('/stop-redirect private A')
    // The throw, and any 60s retry it schedules, must sit on fake timers.
    // Enter is a keydown, not userEvent, because userEvent hangs under them.
    vi.useFakeTimers()
    fireEvent.keyDown(screen.getByTestId('chat-input'), { key: 'Enter', code: 'Enter' })
    expect(attempts, 'BR-23e: the command is handed over once').toBe(1)
    expect(socket.readyState, 'BR-23e: the socket stays OPEN').toBe(FakeSocket.OPEN)
    expect(screen.getByTestId('chat-input'), 'BR-23e: the exact text is back once').toHaveValue('/stop-redirect private A')
    expect(screen.getByText(CONN_FAILED), 'BR-23e: the connection-failed line').toBeTruthy()
    expect(screen.queryByText('May not have been delivered'), 'BR-23e: no uncertain item without a close').toBeNull()
    expect(screen.getByText(NOT_STOPPED), 'BR-23e: the notice is shown').toBeTruthy()
    expect(useDraftStore.getState().commands, 'BR-23e: the failed command record is dropped').toEqual({})
    act(() => {
      socket.receive({
        type: 'session_state',
        user_id: 'ada',
        pending_approvals: [],
        session_id: 'chat-b',
        emitted_at: '2026-11-18T00:00:00Z',
        auto_approve_modifier: true,
      })
    })
    await act(async () => { vi.advanceTimersByTime(60_000) })
    expect(attempts, 'BR-23e: unrelated frames and 60s do not retry').toBe(1)
    expect(useDraftStore.getState().commands, 'BR-23e: no command record appears later').toEqual({})
    expect(screen.getAllByDisplayValue('/stop-redirect private A'), 'BR-23e: still one copy').toHaveLength(1)
    expect(screen.getByText(CONN_FAILED)).toBeTruthy()
  })

  it('BR-23e a CLOSING socket returns false and restores the text without a close', async () => {
    // Spec BR-23e variant: send() is false because readyState is CLOSING.
    // No close event is fired. The box, the connection-failed line and the
    // not-stopped notice match the OPEN-throw case, and nothing is retried.
    resetBindingWorld()
    installFakeWebSocket()
    const conn = new WsConnection({
      onFrames: () => {},
      onConnected: () => {},
      onDisconnected: () => {},
      onError: () => {},
    })
    conn.connect()
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('socket missing')
    act(() => { socket.open() })
    act(() => { socket.receive({ type: 'server_capabilities', send_origin: true }) })
    let writes = 0
    let closes = 0
    socket.send = () => {
      writes += 1
      return undefined as never
    }
    const previousClose = socket.onclose
    socket.onclose = (event) => {
      closes += 1
      previousClose?.call(socket, event)
    }
    socket.readyState = 2
    useConnectionStore.setState({ connection: conn as never, isConnected: true })
    mountComposer()
    await typeIntoComposer('/stop-redirect private A')
    vi.useFakeTimers()
    fireEvent.keyDown(screen.getByTestId('chat-input'), { key: 'Enter', code: 'Enter' })
    expect(writes, 'BR-23e: a CLOSING socket is not written').toBe(0)
    expect(closes, 'BR-23e: no close event').toBe(0)
    expect(socket.readyState, 'BR-23e: the socket stays CLOSING').toBe(2)
    expect(screen.getByTestId('chat-input'), 'BR-23e: the exact text is back once').toHaveValue('/stop-redirect private A')
    expect(screen.getByText(CONN_FAILED), 'BR-23e: the connection-failed line').toBeTruthy()
    expect(screen.queryByText('May not have been delivered'), 'BR-23e: no uncertain item without a close').toBeNull()
    expect(screen.getByText(NOT_STOPPED), 'BR-23e: the notice is shown').toBeTruthy()
    expect(useDraftStore.getState().commands, 'BR-23e: the failed command record is dropped').toEqual({})
    act(() => {
      socket.receive({
        type: 'session_state',
        user_id: 'ada',
        pending_approvals: [],
        session_id: 'chat-b',
        emitted_at: '2026-11-18T00:00:00Z',
        auto_approve_modifier: true,
      })
    })
    await act(async () => { vi.advanceTimersByTime(60_000) })
    expect(writes, 'BR-23e: 60s and an unrelated frame do not retry').toBe(0)
    expect(closes, 'BR-23e: still no close event').toBe(0)
    expect(screen.getAllByDisplayValue('/stop-redirect private A'), 'BR-23e: still one copy').toHaveLength(1)
  })

  it('BR-23f a Stop closed before receipt follows the turn id', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    expect(socket.sent.some((frame) => frame.type === 'cancel'), 'BR-23f a: Stop was handed over').toBe(true)
    await closeThenSnapshot(socket, sessionSnapshot('chat-a', 'T'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText(NOT_STOPPED), 'BR-23f a: the same turn shows the notice').toBeTruthy()
    vi.useFakeTimers()
    await act(async () => { vi.advanceTimersByTime(30_000) })
    expect(screen.getByText(NOT_STOPPED), 'BR-23f a: still present after 30s').toBeTruthy()
    expect(screen.getByTestId('stop-btn')).toBeEnabled()
  })

  // Absence with a live-positive control: the snapshot must land in the bucket
  // (auto_approve_modifier). A reconciliation that shows the notice for no
  // turn, or that never applies the snapshot, turns this red.
  it('BR-23f b a snapshot with no active turn shows no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    expect(socket.sent.some((frame) => frame.type === 'cancel'), 'BR-23f b: Stop was handed over').toBe(true)
    await closeThenSnapshot(socket, sessionSnapshot('chat-a'))
    expectSnapshotApplied('chat-a')
    expect(screen.queryByText(NOT_STOPPED), 'BR-23f b: a finished turn shows no notice').toBeNull()
    expect(screen.queryByText('May not have been delivered'), 'BR-23f b: the Stop record is gone').toBeNull()
  })

  // Same control as BR-23f b, for a different turn id (MU-41's second half).
  it('BR-23f c a different turn id shows no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    expect(socket.sent.some((frame) => frame.type === 'cancel'), 'BR-23f c: Stop was handed over').toBe(true)
    await closeThenSnapshot(socket, sessionSnapshot('chat-a', 'T2'))
    expectSnapshotApplied('chat-a')
    expect(screen.queryByText(NOT_STOPPED), 'BR-23f c: a new turn is not the captured one').toBeNull()
    expect(screen.queryByText('May not have been delivered'), 'BR-23f c: the Stop record is gone').toBeNull()
  })

  it('BR-23f d the settling turn\'s own done removes the notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    const next = await closeThenSnapshot(socket, sessionSnapshot('chat-a', 'T'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText(NOT_STOPPED), 'BR-23f d: the same turn shows the notice first').toBeTruthy()
    const before = next.sent.length
    act(() => {
      next.receive({ type: 'done', session_id: 'chat-a', turn_id: 'T' })
    })
    await flushInbound()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23f d: that turn\'s done removes the notice').toBeNull()
    expect(screen.queryByTestId('stop-btn'), 'BR-23f d: Stop is not rendered').toBeNull()
    const auto = next.sent.slice(before).filter((frame) => frame.type === 'cancel' || frame.type === 'redirect' || frame.type === 'message')
    expect(auto, 'BR-23f d: done sends nothing').toEqual([])
  })

  it('BR-23c cancel c1 the same turn shows the notice and the uncertain item', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    await typeIntoComposer('/cancel')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    expect(socket.sent.some((frame) => frame.type === 'cancel'), 'BR-23c cancel: the cancel was handed over').toBe(true)
    await closeThenSnapshot(socket, sessionSnapshot('chat-a', 'T'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('May not have been delivered'), 'BR-23c cancel: the item stays').toBeTruthy()
    expect(screen.getByText(NOT_STOPPED), 'BR-23c cancel: the same turn shows the notice').toBeTruthy()
  })

  it('BR-23c cancel c2 no active turn shows the item and no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    await typeIntoComposer('/cancel')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    await closeThenSnapshot(socket, sessionSnapshot('chat-a'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('May not have been delivered'), 'BR-23c cancel: the item stays').toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c cancel: a finished turn shows no notice').toBeNull()
  })

  it('BR-23c cancel c3 a different turn id shows the item and no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    await typeIntoComposer('/cancel')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    await closeThenSnapshot(socket, sessionSnapshot('chat-a', 'T2'))
    expectSnapshotApplied('chat-a')
    expect(screen.getByText('May not have been delivered'), 'BR-23c cancel: the item stays').toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c cancel: a new turn shows no notice').toBeNull()
  })

  it('BR-23f e a new boot id that reuses the turn id shows the notice and sends nothing', async () => {
    resetBindingWorld()
    const socket = openLive()
    seedTurn('chat-a', 'T')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    expect(socket.sent.some((frame) => frame.type === 'cancel'), 'BR-23f e: Stop was handed over').toBe(true)
    const before = FakeSocket.instances.length
    socket.close(4008, 'catch-up required')
    const next = FakeSocket.instances.at(-1)
    expect(next).not.toBe(socket)
    expect(FakeSocket.instances.length).toBe(before + 1)
    act(() => { next!.open() })
    act(() => { next!.receive({ type: 'server_capabilities', send_origin: true }) })
    act(() => { next!.receive(sessionSnapshot('chat-a', undefined, 'boot-new')) })
    act(() => { next!.receive(sessionSnapshot('chat-a', 'T', 'boot-new')) })
    act(() => {
      next!.receive({
        type: 'catch_up_complete',
        session_id: 'chat-a',
        seq: 1,
        boot_id: 'boot-new',
        mode: 'snapshot',
      })
    })
    await flushInbound()
    expectSnapshotApplied('chat-a')
    expect(screen.getByText(NOT_STOPPED), 'BR-23f e: the reused id shows the notice').toBeTruthy()
    expect(screen.getByTestId('stop-btn'), 'BR-23f e: Stop stays available').toBeEnabled()
    expect(
      next!.sent.filter((frame) => frame.type === 'cancel' || frame.type === 'redirect' || frame.type === 'message'),
      'BR-23f e: nothing is sent automatically',
    ).toEqual([])
  })
}


// BR-23g (UAT-15 option A, turn_started): a turn the page has not yet heard a
// token from is named by the server's live turn_started frame, so a typed
// /stop-redirect captures that turn. (V) Team B's main run: the control
// redirect's turn ended (its done), the redirected instruction started turn-3
// with no token yet. (T) An ordinary message started mia-turn-2 with no token.
// Oracle: spec 4.7 target-running reconciliation and UAT-15 main run (F-tt).
describe('BR-23g turn_started names the running turn before its first token', () => {
  async function redirectAfterTurnStarted(variant: 'V' | 'T'): Promise<FakeSocket> {
    resetBindingWorld()
    const socket = openLive()
    mountComposer()
    if (variant === 'V') {
      seedStreamingAnswer('chat-a', '')
      act(() => { socket.receive({ type: 'token', session_id: 'chat-a', content: 'Working', turn_id: 'mia-turn-2', agent_id: 'mia' }) })
      act(() => { socket.receive({ type: 'done', session_id: 'chat-a', turn_id: 'mia-turn-2' }) })
    } else {
      act(() => {
        socket.receive({ type: 'user_message', session_id: 'chat-a', id: 'u-1', content: 'do the thing', timestamp: EMITTED, agent_id: 'mia' })
      })
    }
    const started = variant === 'V' ? 'turn-3' : 'mia-turn-2'
    act(() => {
      socket.receive({ type: 'turn_started', session_id: 'chat-a', active_turn: { turn_id: started, agent_id: 'mia', started_at: EMITTED } })
    })
    await flushInbound()
    await typeRedirect('/stop-redirect x-p4r9k')
    expect(socket.sent.some((frame) => frame.type === 'redirect'), 'BR-23g: the redirect was handed over').toBe(true)
    return socket
  }

  for (const [variant, started, next] of [['V', 'turn-3', 'turn-4'], ['T', 'mia-turn-2', 'mia-turn-3']] as const) {
    it(`(${variant}) an applied redirect's new turn ${next} shows the uncertain item and no notice`, async () => {
      const socket = await redirectAfterTurnStarted(variant)
      await closeThenSnapshot(socket, sessionSnapshot('chat-a', next))
      expectSnapshotApplied('chat-a')
      expect(screen.getByText('May not have been delivered'), 'BR-23g: the uncertain item stays').toBeTruthy()
      expect(screen.queryByText(NOT_STOPPED), `BR-23g: ${next} is not the captured ${started}`).toBeNull()
    })

    it(`(${variant}) the captured turn ${started} still running shows the notice`, async () => {
      const socket = await redirectAfterTurnStarted(variant)
      await closeThenSnapshot(socket, sessionSnapshot('chat-a', started))
      expectSnapshotApplied('chat-a')
      expect(screen.getByText('May not have been delivered'), 'BR-23g: the uncertain item stays').toBeTruthy()
      expect(screen.getByText(NOT_STOPPED), `BR-23g: ${started} is still running`).toBeTruthy()
    })
  }
})
