// RED — US-17 AS-11; FR-202/215; BDD-17.15; K33.
// The real adapter, ChatScreen, ChatMessageList and RoomTrafficMessage.
// The original cases use the plain-list branch (jsdom has no layout).
// Q81 additionally exposes browser-edge geometry for the REAL virtualizer's
// streaming-to-history handoff. No adapter, row or virtualizer is mocked.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/lib/featureSwitches', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/featureSwitches')>(),
  ROOMS_ENABLED: true,
}))

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Message as WireMessage, SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import type { RoomRef } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { expectValidPage, page, pos, snapshot } from '../../../tests/chat-history-paging/fixtures'
import { ChatScreen } from './ChatScreen'
import { OmnipusRuntimeProvider } from './OmnipusRuntimeProvider'
import {
  json, MAIN_ID, mountAt, Network, NOW, resetRealStores, stopHarness, TestSocket, WS,
} from '../../../tests/l1-room-ui/harness'

const REQUEST = 'draft it\nRoom context: Jim has finished the plan.'
const ANSWER = 'The draft is ready.\nSent from the group.'
const ORDINARY = 'ordinary one-to-one request'
const requestRef: RoomRef = { room_id: 'launch', room_name: 'Launch', request_id: 'delivery-mia-1', role: 'request' }
const answerRef: RoomRef = { ...requestRef, role: 'answer' }

const originalHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
const originalWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')
const originalScrollTo = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTo')
let net: Network
beforeEach(() => { resetRealStores(); net = new Network() })
afterEach(() => {
  stopHarness()
  for (const [key, descriptor] of [['offsetHeight', originalHeight], ['offsetWidth', originalWidth]] as const) {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, key, descriptor)
    else Reflect.deleteProperty(HTMLElement.prototype, key)
  }
  if (originalScrollTo) Object.defineProperty(Element.prototype, 'scrollTo', originalScrollTo)
  else Reflect.deleteProperty(Element.prototype, 'scrollTo')
})

function wire(id: string, role: 'user' | 'assistant', content: string, ref?: RoomRef): WireMessage {
  // agent_id is required on a stored Message. A user row still has no participant,
  // so it stays an ordinary bubble unless it also carries room_ref.
  return { id, role, content, timestamp: NOW, agent_id: 'mia', ...(ref ? { room_ref: ref } : {}) }
}

function historyFor(messages: WireMessage[]): SessionHistoryResponse {
  // Paging D3/D4/D10: the HTTP response is a bounded page, not the retired
  // array. Synthetic positions belong to fixture sources, never DOM indexes.
  const history = page(messages.map((message, index) => ({ ...message, history_position: pos(index + 1) })), { sessionId: MAIN_ID })
  expectValidPage(history, messages.map((_, index) => pos(index + 1)), messages.length)
  return history
}

async function mountChat(messages: WireMessage[], layout: 'plain' | 'virtualized' = 'plain') {
  const history = historyFor(messages)
  net.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(history))
  net.respond('GET', '/api/v1/skills', () => json([]))
  net.respond('GET', '/api/v1/commands', () => json([]))
  net.respond('GET', '/api/v1/providers', () => json([]))
  TestSocket.instances = []
  vi.stubGlobal('WebSocket', TestSocket)
  if (layout === 'plain') {
    vi.stubGlobal('ResizeObserver', undefined)
  } else {
    // Browser edge only, as in the stage-1 harness: the real TanStack
    // virtualizer needs a nonzero viewport. 800 is fixture geometry, not an
    // expected production measurement. resetRealStores supplies ResizeObserver.
    // jsdom lacks Element.scrollTo; implement only that browser edge, not the
    // AssistantUI auto-follow hook. This case asserts rendering, not pixels.
    if (!Element.prototype.scrollTo) {
      Object.defineProperty(Element.prototype, 'scrollTo', {
        configurable: true,
        value(this: Element, options: ScrollToOptions | number, y?: number) {
          const top = typeof options === 'number' ? y : options.top
          const left = typeof options === 'number' ? options : options.left
          if (top !== undefined) this.scrollTop = top
          if (left !== undefined) this.scrollLeft = left
        },
      })
    }
    for (const [key, original] of [['offsetHeight', originalHeight], ['offsetWidth', originalWidth]] as const) {
      Object.defineProperty(HTMLElement.prototype, key, {
        configurable: true,
        get() {
          return this.getAttribute('data-testid') === 'virtualized-message-list' ? 800 : original?.get?.call(this) ?? 0
        },
      })
    }
  }
  useSessionStore.setState({ attachedSessionType: 'main' })
  await mountAt(<OmnipusRuntimeProvider><ChatScreen /></OmnipusRuntimeProvider>, `/workspaces/${WS}/chat`)
  await screen.findByTestId('chat-input')
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID]?.history.historyLoaded,
    'Q81 input control: the real bounded HTTP history read completed before opening the socket').toBe(true))
}

function startSnapshot(socket: TestSocket, history: SessionHistoryResponse) {
  // D6/D7: current state, positioned sources, replay done, then matching cut.
  socket.receive({ ...snapshot(history), seq: 1, boot_id: 'room-traffic-test' })
  socket.receive({ type: 'session_state', session_id: MAIN_ID, user_id: 'room-traffic-reader', history_state: { status: 'available' }, pending_approvals: [], emitted_at: NOW })
}

async function openSocket() {
  const socket = TestSocket.instances[0]
  if (!socket) throw new Error('ChatScreen did not open its hub socket')
  act(() => socket.open())
  await waitFor(() => {
    if (!socket.sent.some((frame) => frame.type === 'attach_session' && frame.session_id === MAIN_ID)) {
      throw new Error('ChatScreen did not attach the open main session')
    }
  })
  return socket
}

function collapsed(firstLine: string) {
  const toggle = screen.getAllByTestId('room-traffic-toggle').find((element) =>
    element.textContent?.startsWith(`From group Launch: ${firstLine}`),
  )
  expect(toggle, `collapsed From group Launch: ${firstLine}`).toBeTruthy()
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggle).not.toHaveTextContent('\n')
  return toggle!
}

async function expand(toggle: HTMLElement) {
  await userEvent.setup().click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  const entry = toggle.closest('[data-testid="room-traffic-entry"]')
  if (!(entry instanceof HTMLElement)) throw new Error('room toggle is outside room-traffic-entry')
  return entry
}

async function expectRoomTraffic() {
  await waitFor(() => {
    const bucket = useChatStore.getState().sessionsById[MAIN_ID]
    expect(bucket?.messagesById['main-room-request']?.roomRef, 'K33 input control: real request adapter retained the full room reference').toEqual(requestRef)
    expect(bucket?.messagesById['main-room-answer']?.roomRef, 'K33 input control: real answer adapter retained the full room reference').toEqual(answerRef)
    expect(bucket?.messagesById['main-room-request']?.content).toBe(REQUEST)
    expect(bucket?.messagesById['main-room-answer']?.content).toBe(ANSWER)
  })
  await waitFor(() => expect(screen.queryAllByTestId('room-traffic-entry'),
    'Q81: real ChatScreen → ChatMessageList must reach RoomTrafficMessage for BOTH admitted room sources').toHaveLength(2))
  await waitFor(() => expect(screen.getAllByTestId('room-traffic-entry')).toHaveLength(2))
  const request = await expand(collapsed('draft it'))
  // The paragraph keeps the newline; the matcher reads it as one block.
  expect(request).toHaveTextContent('draft it')
  expect(request).toHaveTextContent('Room context: Jim has finished the plan.')
  expect(within(request).getByRole('link', { name: /open group launch/i }).getAttribute('href'))
    .toMatch(/\/workspaces\/sales\/rooms\/launch(?:\?|$)/)

  const answer = await expand(collapsed('The draft is ready.'))
  expect(await within(answer).findByText('Mia', { exact: true })).toBeVisible()
  expect(answer).toHaveTextContent('The draft is ready.')
  expect(answer).toHaveTextContent('Sent from the group.')
  expect(within(answer).getByRole('link', { name: /open group launch/i }).getAttribute('href'))
    .toMatch(/\/workspaces\/sales\/rooms\/launch(?:\?|$)/)

  expect(screen.getAllByTestId('user-message')).toHaveLength(1)
  expect(screen.getByTestId('user-message')).toHaveTextContent(ORDINARY)
  expect(screen.queryByTestId('assistant-message')).toBeNull()
  expect(net.unexpected).toEqual([])
}

describe('ChatScreen room traffic (US-17 AS-11; FR-202/215; BDD-17.15; K33)', () => {
  it('[FR-202; K33] live room request and answer are compact on the real main chat, and an ordinary message is not', async () => {
    await mountChat([])
    const socket = await openSocket()
    act(() => {
      startSnapshot(socket, historyFor([]))
      socket.receive({ type: 'done', session_id: MAIN_ID })
      socket.receive({ type: 'catch_up_complete', session_id: MAIN_ID, seq: 1, boot_id: 'room-traffic-test', mode: 'snapshot' })
      socket.receive({ type: 'user_message', session_id: MAIN_ID, seq: 2, id: 'ordinary-main-message', timestamp: NOW, content: ORDINARY })
      socket.receive({ type: 'user_message', session_id: MAIN_ID, seq: 3, id: 'main-room-request', timestamp: NOW, content: REQUEST, agent_id: 'mia', room_ref: requestRef })
      socket.receive({ type: 'token', session_id: MAIN_ID, seq: 4, content: ANSWER, message_id: 'main-room-answer', turn_id: 'room-turn-1', agent_id: 'mia', room_ref: answerRef })
      socket.receive({ type: 'done', session_id: MAIN_ID, seq: 5, message_id: 'main-room-answer', turn_id: 'room-turn-1', room_ref: answerRef })
    })
    await expectRoomTraffic()
  })

  it('[FR-202/215; K33] cold history keeps the collapsed room entry, its author and the group link', async () => {
    await mountChat([
      wire('ordinary-main-message', 'user', ORDINARY),
      wire('main-room-request', 'user', REQUEST, requestRef),
      wire('main-room-answer', 'assistant', ANSWER, answerRef),
    ])
    await expectRoomTraffic()
  })

  it('[FR-202/215; K33] replay restores one collapsed entry per room message, and a duplicate replay does not add another', async () => {
    await mountChat([])
    const socket = await openSocket()
    const history = historyFor([
      wire('ordinary-main-message', 'user', ORDINARY),
      wire('main-room-request', 'user', REQUEST, requestRef),
      wire('main-room-answer', 'assistant', ANSWER, answerRef),
    ])
    const request = { type: 'replay_message' as const, session_id: MAIN_ID, role: 'user' as const, id: 'main-room-request', timestamp: NOW, content: REQUEST, agent_id: 'mia', room_ref: requestRef, history_origin: { position: pos(2), entry_id: 'main-room-request' } }
    act(() => {
      startSnapshot(socket, history)
      socket.receive({ type: 'replay_message', session_id: MAIN_ID, role: 'user', id: 'ordinary-main-message', timestamp: NOW, content: ORDINARY, history_origin: { position: pos(1), entry_id: 'ordinary-main-message' } })
      socket.receive(request)
      socket.receive(request)
      socket.receive({ type: 'replay_message', session_id: MAIN_ID, role: 'assistant', id: 'main-room-answer', timestamp: NOW, content: ANSWER, agent_id: 'mia', room_ref: answerRef, history_origin: { position: pos(3), entry_id: 'main-room-answer' } })
      socket.receive({ type: 'done', session_id: MAIN_ID })
      socket.receive({ type: 'catch_up_complete', session_id: MAIN_ID, seq: 1, boot_id: 'room-traffic-test', mode: 'snapshot' })
    })
    await expectRoomTraffic()
    expect(useChatStore.getState().sessionsById[MAIN_ID]?.history.readerPositions,
      'D7: three source groups, not four frames after duplicate replay').toEqual([pos(1), pos(2), pos(3)])
  })

  it('[Q81; FR-202; K33] RoomTrafficMessage renders in the real virtualized and streaming paths, without duplicating the finalized answer', async () => {
    await mountChat([], 'virtualized')
    const socket = await openSocket()
    act(() => {
      startSnapshot(socket, historyFor([]))
      socket.receive({ type: 'done', session_id: MAIN_ID })
      socket.receive({ type: 'catch_up_complete', session_id: MAIN_ID, seq: 1, boot_id: 'room-traffic-test', mode: 'snapshot' })
      socket.receive({ type: 'user_message', session_id: MAIN_ID, seq: 2, id: 'ordinary-main-message', timestamp: NOW, content: ORDINARY })
      socket.receive({ type: 'user_message', session_id: MAIN_ID, seq: 3, id: 'main-room-request', timestamp: NOW, content: REQUEST, agent_id: 'mia', room_ref: requestRef })
      socket.receive({ type: 'token', session_id: MAIN_ID, seq: 4, content: ANSWER, message_id: 'main-room-answer', turn_id: 'room-turn-1', agent_id: 'mia', room_ref: answerRef })
    })
    const streaming = await screen.findByTestId('streaming-message-anchor')
    expect(within(streaming).getByTestId('room-traffic-entry'),
      'Q81: a live room answer reaches RoomTrafficMessage, not an ordinary assistant bubble').toBeVisible()
    expect(within(streaming).getByTestId('room-traffic-toggle')).toHaveTextContent('From group Launch: The draft is ready.')
    const requestToggle = collapsed('draft it')
    expect(requestToggle.closest('[data-index]'),
      'Q81 instrument: the request really used a virtualizer row, not the plain fallback').not.toBeNull()
    await expectRoomTraffic()

    act(() => socket.receive({ type: 'done', session_id: MAIN_ID, seq: 5, message_id: 'main-room-answer', turn_id: 'room-turn-1', room_ref: answerRef }))
    await waitFor(() => expect(screen.queryByTestId('streaming-message-anchor'),
      'Q81: finalization removes the live slot').toBeNull())
    expect(screen.getAllByTestId('room-traffic-entry'), 'FR-215: one request and one answer after the handoff').toHaveLength(2)
    const answerToggle = screen.getAllByTestId('room-traffic-toggle').find((toggle) => toggle.textContent === 'From group Launch: The draft is ready.')
    expect(answerToggle?.closest('[data-index]'),
      'Q81: the SAME answer now renders through the real virtualized RoomTrafficMessage branch').toBeInstanceOf(HTMLElement)
    expect(useChatStore.getState().sessionsById[MAIN_ID]?.messageOrder.filter((id) => id === 'main-room-answer')).toEqual(['main-room-answer'])
    expect(screen.queryByTestId('assistant-message')).toBeNull()
    expect(net.unexpected).toEqual([])
  })
})
