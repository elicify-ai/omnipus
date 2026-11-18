// #1428 step 2 RED. Spec 4.10 BR-20 a-f, B-16, B-25.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import {
  NOT_STOPPED,
  UNSUPPORTED_BANNER,
  FakeSocket,
  apiMocks,
  installFakeWebSocket,
  mountComposer,
  resetBindingWorld,
  seedStreamingAnswer,
  typeIntoComposer,
} from '@/test/sendBinding1428'

const logoutCalls = vi.hoisted(() => ({ n: 0 }))
vi.mock('@/lib/authLogout', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/authLogout')>()
  return {
    ...actual,
    forceLogout: (reason?: Parameters<typeof actual.forceLogout>[0]) => {
      logoutCalls.n += 1
      actual.forceLogout(reason)
    },
  }
})

let live: WsConnection | null = null

function openSocket(): FakeSocket {
  installFakeWebSocket()
  logoutCalls.n = 0
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
  useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
  return socket
}

function controlSent(socket: FakeSocket) {
  return socket.sent.filter((frame) => frame.type === 'message' || frame.type === 'redirect' || frame.type === 'cancel')
}

afterEach(() => {
  live?.disconnect()
  live = null
  vi.unstubAllGlobals()
})

describe('capability state (spec 4.10)', () => {
  it('BR-20a sends nothing until server_capabilities, then drains once', async () => {
    resetBindingWorld()
    const socket = openSocket()
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('queued hello')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(controlSent(socket), 'BR-20a: unconfirmed sends no message').toEqual([])
    expect(apiMocks.createSession, 'BR-20a: no keyed create').not.toHaveBeenCalled()
    expect(apiMocks.uploadFiles, 'BR-20a: no session upload').not.toHaveBeenCalled()
    act(() => {
      socket.receive({ type: 'server_capabilities', send_origin: true })
    })
    expect(controlSent(socket), 'BR-20a: the queued send drains once after the capability').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'queued hello',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
  })

  it('BR-20b a session_state first frame is unsupported and lists the draft', async () => {
    resetBindingWorld()
    const socket = openSocket()
    mountComposer()
    await typeIntoComposer('kept')
    act(() => { socket.receive({ type: 'session_state', session_id: 'chat-a' } as never) })
    expect(screen.getByText(UNSUPPORTED_BANNER), 'BR-20b: the reload banner').toBeTruthy()
    expect(screen.getByRole('button', { name: 'Copy' }), 'BR-20b: the draft is listed with Copy').toBeTruthy()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(controlSent(socket), 'BR-20b: unsupported sends nothing').toEqual([])
  })

  it('BR-20c a later socket whose first frame is not the capability is unsupported', () => {
    resetBindingWorld()
    const first = openSocket()
    act(() => { first.receive({ type: 'server_capabilities', send_origin: true }) })
    const second = openSocket()
    act(() => { second.receive({ type: 'session_state', session_id: 'chat-a' } as never) })
    mountComposer()
    expect(screen.getByText(UNSUPPORTED_BANNER), 'BR-20c: the new socket does not inherit supported').toBeTruthy()
  })

  it('BR-20d a late frame from the retired socket is ignored', () => {
    resetBindingWorld()
    const retired = openSocket()
    const next = openSocket()
    act(() => {
      retired.receive({ type: 'server_capabilities', send_origin: true })
      next.receive({ type: 'session_state', session_id: 'chat-a' } as never)
    })
    mountComposer()
    expect(screen.getByText(UNSUPPORTED_BANNER), 'BR-20d: the retired capability does not override').toBeTruthy()
  })

  // GUARD of today's 1008 logout. The missing banner is absence until the banner exists.
  it('BR-20e an auth error then close 1008 logs out and is not a version mismatch', () => {
    resetBindingWorld()
    const socket = openSocket()
    mountComposer()
    act(() => {
      socket.receive({ type: 'error', message: 'unauthorized' } as never)
      socket.close(1008, 'policy')
    })
    expect(logoutCalls.n, 'BR-20e: forceLogout once').toBe(1)
    expect(screen.queryByText(UNSUPPORTED_BANNER), 'BR-20e: auth is not a version mismatch').toBeNull()
    expect(controlSent(socket), 'BR-20e: nothing was sent').toEqual([])
  })

  it('BR-20e variant: an error then the capability, with no close, becomes supported', async () => {
    resetBindingWorld()
    const socket = openSocket()
    mountComposer()
    await typeIntoComposer('after auth')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    act(() => { socket.receive({ type: 'error', message: 'transient' } as never) })
    expect(controlSent(socket), 'BR-20e: nothing is sent before the capability').toEqual([])
    act(() => { socket.receive({ type: 'server_capabilities', send_origin: true }) })
    expect(screen.queryByText(UNSUPPORTED_BANNER), 'BR-20e: the capability wins when the socket stays open').toBeNull()
    expect(logoutCalls.n, 'BR-20e: no logout without 1008').toBe(0)
  })

  it('BR-20f an old engine shows both notices and a second Stop still sends nothing', async () => {
    resetBindingWorld()
    const socket = openSocket()
    seedStreamingAnswer('chat-a')
    mountComposer()
    act(() => { socket.receive({ type: 'session_state', session_id: 'chat-a' } as never) })
    const user = userEvent.setup()
    await typeIntoComposer('/stop-redirect x')
    await user.keyboard('{Enter}')
    await user.click(screen.getByTestId('stop-btn'))
    expect(controlSent(socket), 'BR-20f: no redirect or cancel').toEqual([])
    expect(screen.getByTestId('chat-input'), 'BR-20f: the typed command stays').toHaveValue('/stop-redirect x')
    expect(screen.getByText(NOT_STOPPED), 'BR-20f: the current-tab notice').toBeTruthy()
    expect(screen.getByText(UNSUPPORTED_BANNER), 'BR-20f: the banner is shown with the notice').toBeTruthy()
    expect(screen.queryByText(/\(interrupted\)/), 'BR-20f: the answer is not frozen').toBeNull()
    await user.click(screen.getByTestId('stop-btn'))
    expect(controlSent(socket), 'BR-20f: the second Stop still sends nothing').toEqual([])
    expect(screen.getByText(NOT_STOPPED)).toBeTruthy()
    expect(screen.getByText(UNSUPPORTED_BANNER)).toBeTruthy()
    expect(useSessionStore.getState().activeSessionId).toBe('chat-a')
  })
})
