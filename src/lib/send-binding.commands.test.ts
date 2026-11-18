// #1428 step 2 RED. Spec 4.7 BR-23, BR-23e, BR-23f.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useSessionStore } from '@/store/session'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import {
  CONN_FAILED,
  NOT_STOPPED,
  UNCERTAIN_LINE,
  FakeSocket,
  connection,
  framesOf,
  installFakeWebSocket,
  mountComposer,
  resetBindingWorld,
  seedStreamingAnswer,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

afterEach(() => {
  vi.useRealTimers()
})

async function typeRedirect(text: string): Promise<void> {
  await typeIntoComposer(text)
  const user = userEvent.setup()
  await user.click(screen.getByTestId('chat-input'))
  await user.keyboard('{Enter}')
}

describe('command records (spec 4.7)', () => {
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
    mountComposer()
    await typeRedirect('/stop-redirect y')
    connection().fail = true
    act(() => { useConnectionStore.setState({ isConnected: false }) })
    expect(screen.getByText('May not have been delivered'), 'BR-23c: the close is uncertain').toBeTruthy()
    expect(screen.getByText(UNCERTAIN_LINE), 'BR-23c: the may-already line').toBeTruthy()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await navigator.clipboard.readText(), 'BR-23c: Copy is the command').toBe('/stop-redirect y')
    connection().fail = false
    act(() => { useConnectionStore.setState({ isConnected: true }) })
    expect(framesOf('redirect', sent()), 'BR-23c: the next connection does not resend it').toEqual([])
  })

  it('BR-23d sixty commands without a receipt stay sixty items', async () => {
    resetBindingWorld()
    mountComposer()
    for (let i = 0; i < 60; i++) {
      await typeRedirect(`/stop-redirect n${i}`)
    }
    connection().fail = true
    act(() => { useConnectionStore.setState({ isConnected: false }) })
    expect(screen.getByText('60 not sent'), 'BR-23d: none of the sixty is dropped').toBeTruthy()
  })

  it('BR-23c1 the same turn still running shows the notice and the uncertain item', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    // Notice is established by the snapshot below. Fake timers cover that
    // step only; they hang userEvent if they are on during typing.
    vi.useFakeTimers()
    connection().fail = true
    deliverFrame({
      type: 'session_state',
      session_id: 'chat-a',
      active_turn: { turn_id: 'T' },
    } as never)
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
    expect(screen.getByText(NOT_STOPPED), 'BR-23c1: the same turn shows the notice').toBeTruthy()
    expect(screen.getByTestId('stop-btn'), 'BR-23c1: Stop stays available').toBeEnabled()
    await act(async () => { vi.advanceTimersByTime(30_000) })
    expect(screen.getByText(NOT_STOPPED), 'BR-23c1: still there after 30s').toBeTruthy()
    expect(framesOf('redirect', sent()).filter((f) => (f as { instruction?: string }).instruction === 'y'), 'BR-23c1: no automatic redirect').toEqual([])
  })

  it('BR-23c2 no active turn means the item and no notice', async () => {
    resetBindingWorld()
    mountComposer()
    await typeRedirect('/stop-redirect y')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a' } as never)
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c2: a finished turn shows no notice').toBeNull()
  })

  it('BR-23c3 a different turn id shows the item and no notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a', active_turn: { turn_id: 'T2' } } as never)
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c3: a new turn is not the captured one').toBeNull()
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
  })

  it('BR-23c4 a helper target follows the same three outcomes', async () => {
    resetBindingWorld()
    act(() => { useSessionStore.getState().setActiveSession('helper-1', 'mia', 'core') })
    seedStreamingAnswer('helper-1')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'helper-1', active_turn: { turn_id: 'H' } } as never)
    expect(screen.getByText(NOT_STOPPED), 'BR-23c4: a helper uses the same notice').toBeTruthy()
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
  })

  it('BR-23c5 a restarted gateway that reuses the turn id still shows the notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    await typeRedirect('/stop-redirect y')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'server_capabilities', send_origin: true })
    deliverFrame({ type: 'session_state', boot_id: 'boot-2', session_id: 'chat-a', active_turn: { turn_id: 'T' } } as never)
    expect(screen.getByText(NOT_STOPPED), 'BR-23c5: a reused id still shows the notice').toBeTruthy()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await navigator.clipboard.readText()).toBe('/stop-redirect y')
    expect(framesOf('redirect', sent()).length, 'BR-23c5: nothing is sent automatically').toBe(1)
  })

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
    socket.send = () => { throw new Error('OPEN socket send threw') }
    useConnectionStore.setState({ connection: conn as never, isConnected: true })
    mountComposer()
    await typeIntoComposer('/stop-redirect private A')
    // The throw, and any 60s retry it schedules, must sit on fake timers.
    // Enter is a keydown, not userEvent, because userEvent hangs under them.
    vi.useFakeTimers()
    fireEvent.keyDown(screen.getByTestId('chat-input'), { key: 'Enter', code: 'Enter' })
    expect(socket.readyState, 'BR-23e: the socket stays OPEN').toBe(FakeSocket.OPEN)
    expect(screen.getByTestId('chat-input'), 'BR-23e: the exact text is back once').toHaveValue('/stop-redirect private A')
    expect(screen.getByText(CONN_FAILED), 'BR-23e: the connection-failed line').toBeTruthy()
    expect(screen.queryByText('May not have been delivered'), 'BR-23e: no uncertain item without a close').toBeNull()
    expect(screen.getByText(NOT_STOPPED), 'BR-23e: the notice is shown').toBeTruthy()
    await act(async () => { vi.advanceTimersByTime(60_000) })
    expect(screen.getAllByDisplayValue('/stop-redirect private A'), 'BR-23e: still one copy').toHaveLength(1)
    expect(screen.getByText(CONN_FAILED)).toBeTruthy()
  })

  it('BR-23f a Stop closed before receipt follows the turn id', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    vi.useFakeTimers()
    connection().fail = true
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a', active_turn: { turn_id: 'T' } } as never)
    expect(screen.getByText(NOT_STOPPED), 'BR-23f a: the same turn shows the notice').toBeTruthy()
    await act(async () => { vi.advanceTimersByTime(30_000) })
    expect(screen.getByText(NOT_STOPPED), 'BR-23f a: still present after 30s').toBeTruthy()
    expect(screen.getByTestId('stop-btn')).toBeEnabled()
  })

  // GUARD, not RED proof. Both assertions are absence. They pass until the
  // notice and the Unsent list exist, and they fail if a finished turn shows either.
  it('BR-23f b a snapshot with no active turn shows no notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    connection().fail = true
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a' } as never)
    expect(screen.queryByText(NOT_STOPPED), 'BR-23f b: a finished turn shows no notice').toBeNull()
    expect(screen.queryByText('May not have been delivered'), 'BR-23f b: the Stop record is gone').toBeNull()
  })

  // GUARD, not RED proof. Same absence shape as BR-23f b, for a different turn id.
  it('BR-23f c a different turn id shows no notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    connection().fail = true
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a', active_turn: { turn_id: 'T2' } } as never)
    expect(screen.queryByText(NOT_STOPPED), 'BR-23f c: a new turn is not the captured one').toBeNull()
    expect(screen.queryByText('May not have been delivered'), 'BR-23f c: the Stop record is gone').toBeNull()
  })

  it('BR-23f d the settling turn\'s own done removes the notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    connection().fail = true
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a', active_turn: { turn_id: 'T' } } as never)
    expect(screen.getByText(NOT_STOPPED), 'BR-23f d: the same turn shows the notice first').toBeTruthy()
    const before = sent().length
    deliverFrame({ type: 'done', session_id: 'chat-a', turn_id: 'T' } as never)
    expect(screen.queryByText(NOT_STOPPED), 'BR-23f d: that turn\'s done removes the notice').toBeNull()
    expect(screen.queryByTestId('stop-btn'), 'BR-23f d: Stop is not rendered').toBeNull()
    const auto = sent().slice(before).filter((frame) => frame.type === 'cancel' || frame.type === 'redirect' || frame.type === 'message')
    expect(auto, 'BR-23f d: done sends nothing').toEqual([])
  })

  it('BR-23c cancel c1 the same turn shows the notice and the uncertain item', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    await typeIntoComposer('/cancel')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    connection().fail = true
    deliverFrame({ type: 'session_state', session_id: 'chat-a', active_turn: { turn_id: 'T' } } as never)
    expect(screen.getByText('May not have been delivered'), 'BR-23c cancel: the item stays').toBeTruthy()
    expect(screen.getByText(NOT_STOPPED), 'BR-23c cancel: the same turn shows the notice').toBeTruthy()
  })

  it('BR-23c cancel c2 no active turn shows the item and no notice', async () => {
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('/cancel')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a' } as never)
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c cancel: a finished turn shows no notice').toBeNull()
  })

  it('BR-23c cancel c3 a different turn id shows the item and no notice', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    await typeIntoComposer('/cancel')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({ type: 'session_state', session_id: 'chat-a', active_turn: { turn_id: 'T2' } } as never)
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'BR-23c cancel: a new turn shows no notice').toBeNull()
  })

  it('BR-23f e a new boot id that reuses the turn id shows the notice and sends nothing', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    const { deliverFrame } = await import('@/test/sendBinding1428')
    const before = sent().length
    deliverFrame({ type: 'server_capabilities', send_origin: true })
    deliverFrame({ type: 'session_state', boot_id: 'boot-new' } as never)
    deliverFrame({
      type: 'session_state',
      session_id: 'chat-a',
      boot_id: 'boot-new',
      active_turn: { turn_id: 'T' },
    } as never)
    expect(screen.getByText(NOT_STOPPED), 'BR-23f e: the reused id shows the notice').toBeTruthy()
    expect(screen.getByTestId('stop-btn')).toBeEnabled()
    const auto = sent().slice(before).filter((frame) => frame.type === 'cancel' || frame.type === 'redirect' || frame.type === 'message')
    expect(auto, 'BR-23f e: nothing is sent automatically').toEqual([])
  })
})
