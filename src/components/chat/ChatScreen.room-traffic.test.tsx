// RED — US-17 AS-11; FR-202/215; BDD-17.15; K33.
// The real ChatScreen, not the standalone ChatThread. jsdom cannot lay out
// the virtualized list, so ChatScreen takes its plain-list branch here; the
// virtualized and streaming branches are the same room-traffic condition.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/lib/featureSwitches', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/featureSwitches')>(),
  ROOMS_ENABLED: true,
}))

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Message as WireMessage } from '@/lib/api/generated/openapi-types'
import type { RoomRef } from '@/lib/api/generated/asyncapi-types'
import { useSessionStore } from '@/store/session'
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

let net: Network
beforeEach(() => { resetRealStores(); net = new Network() })
afterEach(() => stopHarness())

function wire(id: string, role: 'user' | 'assistant', content: string, ref?: RoomRef): WireMessage {
  // agent_id is required on a stored Message. A user row still has no participant,
  // so it stays an ordinary bubble unless it also carries room_ref.
  return { id, role, content, timestamp: NOW, agent_id: 'mia', ...(ref ? { room_ref: ref } : {}) }
}

async function mountChat(messages: WireMessage[]) {
  net.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(messages))
  net.respond('GET', '/api/v1/skills', () => json([]))
  net.respond('GET', '/api/v1/commands', () => json([]))
  net.respond('GET', '/api/v1/providers', () => json([]))
  TestSocket.instances = []
  vi.stubGlobal('WebSocket', TestSocket)
  // Real ChatScreen branch: without ResizeObserver it renders PlainMessageList.
  vi.stubGlobal('ResizeObserver', undefined)
  useSessionStore.setState({ attachedSessionType: 'main' })
  await mountAt(<OmnipusRuntimeProvider><ChatScreen /></OmnipusRuntimeProvider>, `/workspaces/${WS}/chat`)
  await screen.findByTestId('chat-input')
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
      socket.receive({ type: 'catch_up_complete', session_id: MAIN_ID, seq: 1, boot_id: 'room-traffic-test', mode: 'snapshot' })
      socket.receive({ type: 'user_message', session_id: MAIN_ID, id: 'ordinary-main-message', timestamp: NOW, content: ORDINARY })
      socket.receive({ type: 'user_message', session_id: MAIN_ID, id: 'main-room-request', timestamp: NOW, content: REQUEST, agent_id: 'mia', room_ref: requestRef })
      socket.receive({ type: 'token', session_id: MAIN_ID, content: ANSWER, message_id: 'main-room-answer', turn_id: 'room-turn-1', agent_id: 'mia', room_ref: answerRef })
      socket.receive({ type: 'done', session_id: MAIN_ID, message_id: 'main-room-answer', turn_id: 'room-turn-1', room_ref: answerRef })
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
    const request = { type: 'replay_message' as const, session_id: MAIN_ID, role: 'user' as const, id: 'main-room-request', timestamp: NOW, content: REQUEST, agent_id: 'mia', room_ref: requestRef }
    act(() => {
      socket.receive({ type: 'replay_message', session_id: MAIN_ID, role: 'user', id: 'ordinary-main-message', timestamp: NOW, content: ORDINARY })
      socket.receive(request)
      socket.receive(request)
      socket.receive({ type: 'replay_message', session_id: MAIN_ID, role: 'assistant', id: 'main-room-answer', timestamp: NOW, content: ANSWER, agent_id: 'mia', room_ref: answerRef })
      socket.receive({ type: 'catch_up_complete', session_id: MAIN_ID, seq: 1, boot_id: 'room-traffic-test', mode: 'snapshot' })
    })
    await expectRoomTraffic()
  })
})
