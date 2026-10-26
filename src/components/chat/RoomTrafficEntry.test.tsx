// RED — spec row 129; US-17 AS-11; FR-202/215; BDD-17.15; K33.
// Spec-vs-plan discrepancy: FR-202 says requests AND answers use the collapsed
// "From group <name>: …" form. The planned "Jim in group …" answer form is
// not used as an oracle. Direct props are only the public import interface.
import type { ComponentType } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Stage 1 hides RoomTrafficMessage's ChatThread/ChatScreen entry points.
// Keep the original enabled-feature carrier oracles for their later landing.
vi.mock('@/lib/featureSwitches', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/featureSwitches')>(),
  ROOMS_ENABLED: true,
}))
import { act } from 'react'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Message as WireMessage } from '@/lib/api/generated/openapi-types'
import type { RoomRef } from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { wireHistoryPage } from '../../../tests/chat-history-paging/legacy-wire'
import { ChatThread } from './ChatThread'
import {
  connectSocket, json, MAIN_ID, mountAt, Network, NOW, requiredExport,
  resetRealStores, stopHarness,
} from '../../../tests/l1-room-ui/harness'

const roomRef: RoomRef = { room_id: 'launch', room_name: 'Launch', request_id: 'delivery-mia-1', role: 'request' }
const text = 'draft it\nRoom context: Jim has finished the plan.'
// not-wire-format: public React props confirmed by team-lead.
type EntryProps = { roomRef: RoomRef; text: string; agentName?: string }
let net: Network
let connection: WsConnection | undefined
beforeEach(() => { resetRealStores(); net = new Network(); connection = undefined })
afterEach(() => stopHarness(connection))

async function direct(ref: RoomRef = roomRef, author?: string) {
  const RoomTrafficEntry = await requiredExport<ComponentType<EntryProps>>('/src/components/chat/RoomTrafficEntry.tsx', 'RoomTrafficEntry', 'FR-202/215; BDD-17.15; test row 129')
  return mountAt(<RoomTrafficEntry roomRef={ref} text={text} agentName={author} />, '/')
}

function expectCollapsed() {
  const toggle = screen.getByTestId('room-traffic-toggle')
  expect(toggle).toBeVisible()
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggle).toHaveTextContent(/^From group Launch: draft it/)
  expect(toggle).not.toHaveTextContent('Room context: Jim has finished the plan.')
  const expandedContext = screen.queryByText('Room context: Jim has finished the plan.', { exact: true })
  if (expandedContext) expect(expandedContext).not.toBeVisible()
  return toggle
}

describe('RoomTrafficEntry disclosure (FR-202, independent spec oracle)', () => {
  it('[BDD-17.15; FR-202] a room request starts compact and collapsed', async () => {
    await direct()
    expectCollapsed()
  })

  it('[BDD-17.15; FR-202] expansion reveals the whole request/context and a link to the correct group', async () => {
    await direct()
    const user = userEvent.setup()
    const toggle = expectCollapsed()
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByTestId('room-traffic-entry')).toHaveTextContent('draft it')
    expect(screen.getByTestId('room-traffic-entry')).toHaveTextContent('Room context: Jim has finished the plan.')
    const link = screen.getByRole('link', { name: /open group/i })
    expect(link.getAttribute('href')).toMatch(/\/rooms\/launch(?:\?|$)/)
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
  })

  it('[FR-202/215; K33] an agent’s answer also has the spec’s From group label and remains expandable', async () => {
    await direct({ ...roomRef, role: 'answer' }, 'Mia')
    const toggle = expectCollapsed()
    await userEvent.setup().click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByTestId('room-traffic-entry')).toHaveTextContent('Room context: Jim has finished the plan.')
    expect(screen.getByTestId('room-traffic-entry')).toHaveTextContent('Mia')
  })
})

describe('K33 carriers go through the real main-chat parser/store/renderer', () => {
  it('[FR-202; K33 live UserMessageFrame] live room requests are collapsed, not ordinary user bubbles', async () => {
    const linked = connectSocket(); connection = linked.connection
    await mountAt(<ChatThread />)
    await act(async () => linked.socket.receive({ type: 'user_message', session_id: MAIN_ID, id: 'main-room-request', timestamp: NOW, content: text, agent_id: 'mia', room_ref: roomRef }))
    await screen.findByTestId('room-traffic-toggle')
    expectCollapsed()
    expect(screen.getAllByTestId('room-traffic-entry')).toHaveLength(1)
  })

  it('[FR-202/215; K33 live TokenFrame/DoneFrame] a room answer is mirrored once with the same collapsed form', async () => {
    const linked = connectSocket(); connection = linked.connection
    await mountAt(<ChatThread />)
    await act(async () => {
      linked.socket.receive({ type: 'token', session_id: MAIN_ID, content: text, message_id: 'main-room-answer', turn_id: 'room-turn-1', agent_id: 'mia', room_ref: { ...roomRef, role: 'answer' } })
      linked.socket.receive({ type: 'done', session_id: MAIN_ID, message_id: 'main-room-answer', turn_id: 'room-turn-1', room_ref: { ...roomRef, role: 'answer' } })
    })
    await screen.findByTestId('room-traffic-toggle')
    expectCollapsed()
    expect(screen.getAllByTestId('room-traffic-entry')).toHaveLength(1)
  })

  it.each(['request', 'answer'] as const)('[FR-202/215; K33 REST Message] cold history keeps the %s room reference through the real transform', async (role) => {
    const wire: WireMessage = { id: `main-room-${role}`, role: role === 'request' ? 'user' : 'assistant', content: text, timestamp: NOW, agent_id: 'mia', room_ref: { ...roomRef, role } }
    net.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(wireHistoryPage(MAIN_ID, [wire], 1)))
    await mountAt(<ChatThread sessionId={MAIN_ID} />)
    await screen.findByTestId('room-traffic-toggle')
    expectCollapsed()
    expect(screen.getAllByTestId('room-traffic-entry')).toHaveLength(1)
    expect(net.unexpected).toEqual([])
  })

  it('[FR-202/215; K33 ReplayMessageFrame; SC-019] replay restores collapsed room traffic and duplicate replay cannot add another copy', async () => {
    const linked = connectSocket(); connection = linked.connection
    await mountAt(<ChatThread />)
    const replay = { type: 'replay_message' as const, session_id: MAIN_ID, role: 'user' as const, id: 'main-room-replayed', timestamp: NOW, content: text, room_ref: roomRef }
    await act(async () => { linked.socket.receive(replay); linked.socket.receive(replay) })
    await screen.findByTestId('room-traffic-toggle')
    expectCollapsed()
    expect(screen.getAllByTestId('room-traffic-entry')).toHaveLength(1)
  })

  it('[FR-202 negative control; K33] ordinary traffic stays ordinary beside a collapsed room request', async () => {
    const linked = connectSocket(); connection = linked.connection
    await mountAt(<ChatThread />)
    await act(async () => {
      linked.socket.receive({ type: 'user_message', session_id: MAIN_ID, id: 'ordinary-main-message', timestamp: NOW, content: 'ordinary one-to-one request' })
      linked.socket.receive({ type: 'user_message', session_id: MAIN_ID, id: 'main-room-request', timestamp: NOW, content: text, room_ref: roomRef })
    })
    await waitFor(() => expect(screen.getByText('ordinary one-to-one request', { exact: true })).toBeVisible())
    await screen.findByTestId('room-traffic-toggle')
    expectCollapsed()
    expect(screen.getAllByTestId('room-traffic-entry')).toHaveLength(1)
  })
})
