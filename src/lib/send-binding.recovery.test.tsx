// #1428 step 2 RED. Spec 4.9 BR-19, B-23 beforeunload, Unsent list.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { WsConnection } from '@/lib/ws'
import {
  RESTORED_LINE,
  UNCERTAIN_LINE,
  FakeSocket,
  apiMocks,
  deleteOpenChatThroughSidebar,
  deliverFrame,
  flushInbound,
  framesOf,
  installFakeWebSocket,
  makeSession,
  mountComposer,
  resetBindingWorld,
  seedSavedChat,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

describe('recovery UI and the leave warning (spec 4.9 / B-23)', () => {
  it('BR-19 Open, Discard and a narrow screen keep the specified focus', async () => {
    // Each item below is produced by the signal spec 4.8 / 4.9 names.
    // jsdom reports scrollWidth and clientWidth as 0, so a width comparison
    // cannot fail; the 375px horizontal-scroll check is an e2e gap.
    resetBindingWorld()
    mountComposer()
    const user = userEvent.setup()
    // Spec 4.8: the first terminal outcome for a client_message_id wins.
    // A refusal and a confirmed 404 are two messages, not two frames for one id.
    await typeIntoComposer('left behind')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const refusedId = (framesOf('message', sent())[0] as { client_message_id?: string } | undefined)?.client_message_id
    expect(refusedId, 'BR-19: the refusal has its own client_message_id').toEqual(expect.any(String))
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'message',
      session_id: 'chat-b',
      origin: { kind: 'chat', session_id: 'chat-a' },
      client_message_id: refusedId,
    })
    expect(screen.getByText(RESTORED_LINE), 'BR-19 (fix): A is open, so its own box carries the restored line').toBeTruthy()
    // The first send starts a turn, which replaces Send with Stop. Ending that
    // turn is not a second outcome for refusedId (the done names no message id).
    deliverFrame({ type: 'done', session_id: 'chat-a' } as never)
    await user.clear(screen.getByTestId('chat-input'))
    await typeIntoComposer('deleted origin')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const deletedId = (framesOf('message', sent()).at(-1) as { client_message_id?: string } | undefined)?.client_message_id
    expect(deletedId, 'BR-19: the deleted origin has its own client_message_id').toEqual(expect.any(String))
    expect(deletedId, 'BR-19: the two outcomes are different messages').not.toBe(refusedId)
    deliverFrame({
      type: 'error',
      code: 'not_found',
      message: '404',
      client_message_id: deletedId,
      session_id: 'chat-a',
    } as never)
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    expect(screen.getByText('Deleted chat'), 'BR-19: the confirmed 404 names a deleted origin').toBeTruthy()
    expect(screen.getByRole('button', { name: 'Move text here' }), 'BR-19: a deleted origin offers Move text here').toBeTruthy()
    await user.click(screen.getByRole('button', { name: /not sent/ }))
    const discard = screen.getAllByRole('button', { name: 'Discard' })
    expect(discard.length, 'BR-19: the seeded items each have Discard').toBeGreaterThan(1)
    discard[0].focus()
    await user.click(discard[0])
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(document.activeElement, 'BR-19: discarding a middle item focuses the next action').toBe(
      screen.getAllByRole('button', { name: /Open|Copy/ })[0],
    ))
    const last = screen.getAllByRole('button', { name: 'Discard' }).at(-1)
    expect(last, 'BR-19: a last item remains').toBeTruthy()
    last?.focus()
    await user.click(last!)
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(document.activeElement, 'BR-19: discarding the last item focuses the composer').toBe(
      screen.getByTestId('chat-input'),
    ))
  })

  it('BR-19 a close before receipt lists the uncertain command', async () => {
    // Spec 4.7 / 4.9: the connection that carried the command closes before a receipt.
    resetBindingWorld()
    installFakeWebSocket()
    const live = new WsConnection({
      onFrames: (frames) => { for (const frame of frames) deliverFrame(frame as never) },
      onConnected: () => {},
      onDisconnected: () => {},
      onError: () => {},
    })
    live.connect()
    const socket = FakeSocket.instances.at(-1)
    expect(socket, 'BR-19: the command rides a real socket').toBeTruthy()
    act(() => { socket!.open() })
    act(() => { socket!.receive({ type: 'server_capabilities', send_origin: true }) })
    useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
    mountComposer()
    await typeIntoComposer('/stop-redirect held')
    const user = userEvent.setup()
    await user.click(screen.getByTestId('chat-input'))
    await user.keyboard('{Enter}')
    expect(socket!.sent.some((frame) => frame.type === 'redirect'), 'BR-19: the redirect was handed over').toBe(true)
    const count = FakeSocket.instances.length
    socket!.close(4008, 'catch-up required')
    const next = FakeSocket.instances.at(-1)
    expect(next, 'BR-19: the close opens the next connection').not.toBe(socket)
    expect(FakeSocket.instances.length, 'BR-19: reconnect happened').toBe(count + 1)
    act(() => { next!.open() })
    act(() => {
      next!.receive({
        type: 'session_state',
        user_id: 'ada',
        pending_approvals: [],
        session_id: 'chat-a',
        emitted_at: '2026-11-18T00:00:00Z',
        auto_approve_modifier: true,
      })
    })
    act(() => {
      next!.receive({
        type: 'catch_up_complete',
        session_id: 'chat-a',
        seq: 1,
        mode: 'snapshot',
      })
    })
    await flushInbound()
    expect(
      useChatStore.getState().sessionsById['chat-a']?.autoApproveEffective,
      'BR-19: the reattach snapshot reached the reducer',
    ).toBe(true)
    expect(screen.getByText('May not have been delivered'), 'BR-19: the close before receipt is uncertain').toBeTruthy()
    expect(screen.getByText(UNCERTAIN_LINE), 'BR-19: an uncertain item carries the may-already line').toBeTruthy()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 375 })
    window.dispatchEvent(new Event('resize'))
    const line = screen.getByRole('button', { name: /not sent/ })
    expect(line.textContent ?? '', 'BR-19: below 640px the notice is one disclosure line').toMatch(/not sent/)
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
  })

  it('BR-19 an uncreated chat is named from the offline first send', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-new', title: 'Saved New', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved)
    apiMocks.fetchSessions.mockResolvedValue([saved])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
    await deleteOpenChatThroughSidebar('Saved New')
    const user = userEvent.setup()
    await typeIntoComposer('first for the new chat')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    expect(screen.getByText('New chat with Mia in Sales'), 'BR-19: an uncreated chat is named').toBeTruthy()
  })

  it('B-23 warns before unload while a draft exists, and not when the box is empty', async () => {
    resetBindingWorld()
    mountComposer()
    const empty = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(empty)
    expect(empty.defaultPrevented, 'B-23: an empty box does not warn').toBe(false)
    await typeIntoComposer('keep')
    const leaving = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(leaving)
    expect(leaving.defaultPrevented, 'B-23: a non-empty draft warns').toBe(true)
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    const still = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(still)
    expect(still.defaultPrevented, 'B-23: A\'s draft still warns while B is open').toBe(true)
  })
})
