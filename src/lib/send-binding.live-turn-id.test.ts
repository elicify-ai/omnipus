// #1428 step 2, UAT-15 (Team B, df26ab6418): a redirect typed during a turn
// that started LIVE in this tab, applied by the server but its
// command_received lost with the socket. Spec C r9 4.7 "Target-running
// reconciliation (r5)": the notice "Not stopped. Press Stop again." only when
// the next session_state's active_turn is present AND (record.turnId is null
// OR equals it); F-tt: an applied redirect continues under a NEW turn id.
//
// Why BR-23c1..c5 in send-binding.commands.test.ts miss it: their seedTurn
// writes activeTurnId into the store directly. On the live path the gateway
// announces active_turn only in the attach session_state; a turn started
// after the attach names itself only in its token frames (turn_id). The
// fixtures below start the turn through those frames, as a live turn does.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import {
  NOT_STOPPED,
  FakeSocket,
  deliverFrame,
  flushInbound,
  installFakeWebSocket,
  mountComposer,
  resetBindingWorld,
  seedStreamingAnswer,
  typeIntoComposer,
} from '@/test/sendBinding1428'

let live: WsConnection | null = null

afterEach(() => {
  live?.disconnect()
  live = null
  vi.unstubAllGlobals()
})

const EMITTED = '2026-11-18T00:00:00Z'

function openLive(): FakeSocket {
  installFakeWebSocket()
  live = new WsConnection({
    onFrames: (frames) => { for (const frame of frames) deliverFrame(frame as never) },
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

/** A turn that started after the attach: it names itself only in its tokens. */
async function liveTurn(socket: FakeSocket, sessionId: string, turnId: string): Promise<void> {
  seedStreamingAnswer(sessionId, '')
  for (const chunk of ['Working ', 'on it']) {
    act(() => { socket.receive({ type: 'token', session_id: sessionId, content: chunk, turn_id: turnId }) })
  }
  await flushInbound()
}

async function closeThenSnapshot(first: FakeSocket, sessionId: string, activeTurnId: string): Promise<void> {
  first.close(4008, 'catch-up required')
  const next = FakeSocket.instances.at(-1)
  if (!next || next === first) throw new Error('spec 4.7: the close opens the next connection')
  act(() => { next.open() })
  act(() => { next.receive({ type: 'server_capabilities', send_origin: true }) })
  act(() => {
    next.receive({
      type: 'session_state', user_id: 'ada', pending_approvals: [], session_id: sessionId, emitted_at: EMITTED,
      active_turn: { turn_id: activeTurnId, agent_id: 'mia', started_at: EMITTED },
    })
  })
  act(() => { next.receive({ type: 'catch_up_complete', session_id: sessionId, seq: 1, mode: 'snapshot' }) })
  await flushInbound()
}

async function typeRedirect(text: string): Promise<void> {
  await typeIntoComposer(text)
  const user = userEvent.setup()
  await user.click(screen.getByTestId('chat-input'))
  await user.keyboard('{Enter}')
}

describe('UAT-15: the redirect record captures the live turn id', () => {
  it('an applied redirect (server continues under a new turn) shows the uncertain item and no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    mountComposer()
    await liveTurn(socket, 'chat-a', 'turn-9')
    await typeRedirect('/stop-redirect do the other thing')
    expect(socket.sent.some((frame) => frame.type === 'redirect'), 'the redirect was handed over').toBe(true)
    await closeThenSnapshot(socket, 'chat-a', 'turn-10')
    expect(screen.getByText('May not have been delivered'), 'the uncertain item stays').toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'F-tt: turn-10 is not the captured turn-9').toBeNull()
  })

  it('control: the same live turn still running after the reconnect shows the notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    mountComposer()
    await liveTurn(socket, 'chat-a', 'turn-9')
    await typeRedirect('/stop-redirect do the other thing')
    await closeThenSnapshot(socket, 'chat-a', 'turn-9')
    expect(screen.getByText('May not have been delivered')).toBeTruthy()
    expect(screen.getByText(NOT_STOPPED), 'the captured turn-9 is still running').toBeTruthy()
  })
})
