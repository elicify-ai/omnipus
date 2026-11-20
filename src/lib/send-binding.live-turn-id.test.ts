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

// Team B, a86b8f92e6 (UAT-15 main run, W1 Mia chat, x-p4r9k): the positive
// control redirect was applied (mia-turn-2 stopped, mia-turn-3 started from
// its instruction); the second redirect left while mia-turn-3 had not yet
// sent a token. Its command_received was withheld, the socket closed, the
// reattach snapshot showed the applied redirect's new turn mia-turn-4.
// Spec UAT-15 main run: uncertain item, NO notice (F-tt). Option A (chief,
// 2026-11-19): the server names mia-turn-3 at its start with turn_started,
// which is what lets the redirect capture it.
describe('UAT-15 main run: the second redirect leaves before the new turn sends a token', () => {
  /** The control redirect is applied: mia-turn-2 is stopped and done. */
  async function controlRedirectApplied(socket: FakeSocket): Promise<void> {
    await liveTurn(socket, 'chat-a', 'mia-turn-2')
    await typeRedirect('/stop-redirect continue-1')
    const control = socket.sent.filter((frame) => frame.type === 'redirect').at(-1) as { client_frame_id: string } | undefined
    if (!control) throw new Error('the control redirect was not handed over')
    act(() => { socket.receive({ type: 'command_received', command: 'redirect', client_frame_id: control.client_frame_id, session_id: 'chat-a' }) })
    act(() => {
      socket.receive({ type: 'error', session_id: 'chat-a', message: 'Stopped', payload: { llm_error: { code: 'turn_canceled', message: 'Stopped', retryable: true } } })
    })
    act(() => { socket.receive({ type: 'done', session_id: 'chat-a', turn_id: 'mia-turn-2' }) })
    await flushInbound()
  }

  it('an applied redirect shows the uncertain item and no notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    mountComposer()
    await controlRedirectApplied(socket)
    act(() => {
      socket.receive({ type: 'turn_started', session_id: 'chat-a', active_turn: { turn_id: 'mia-turn-3', agent_id: 'mia', started_at: EMITTED } })
    })
    await flushInbound()
    await typeRedirect('/stop-redirect x-p4r9k')
    expect(socket.sent.filter((frame) => frame.type === 'redirect'), 'both redirects were handed over').toHaveLength(2)
    await closeThenSnapshot(socket, 'chat-a', 'mia-turn-4')
    expect(screen.getByText('May not have been delivered'), 'the uncertain item stays').toBeTruthy()
    expect(screen.queryByText(NOT_STOPPED), 'F-tt: mia-turn-4 is the applied redirect\'s new turn').toBeNull()
  })

  // Known limit R3 (spec 4.7 null-id rule, unchanged): a command typed before
  // the next turn has started (no turn_started received yet) captures no turn
  // id, so any running turn in the reattach snapshot counts as still running.
  it('known limit R3: a redirect typed before the next turn started shows the notice', async () => {
    resetBindingWorld()
    const socket = openLive()
    mountComposer()
    await controlRedirectApplied(socket)
    await typeRedirect('/stop-redirect x-p4r9k')
    expect(socket.sent.filter((frame) => frame.type === 'redirect'), 'both redirects were handed over').toHaveLength(2)
    await closeThenSnapshot(socket, 'chat-a', 'mia-turn-4')
    expect(screen.getByText('May not have been delivered'), 'the uncertain item stays').toBeTruthy()
    expect(screen.getByText(NOT_STOPPED), 'spec 4.7: no captured turn id, a running turn shows the notice').toBeTruthy()
  })
})
