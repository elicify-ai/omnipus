// RED — #1449 US-5, US-7, US-11.
// Oracles: docs/internal/specs/1449-group-chat-spec.md S-35, S-36, S-50, S-98.
// Real RoomScreen. Only the socket and fetch are doubled.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import {
  Network, delivery, entry, openRoom, resetRealStores, snapshot, stopHarness,
} from '../../../tests/l1-room-ui/harness'
import type { WsConnection } from '@/lib/ws'

let net: Network
let connection: WsConnection | undefined

beforeEach(() => {
  resetRealStores()
  net = new Network()
  connection = undefined
})
afterEach(() => stopHarness(connection))

describe('calm live view and the participants control (US-5, US-7, US-11)', () => {
  it('S-35 shows one "<Agent> is working..." line per agent and no partial answer', async () => {
    const harness = await openRoom(net, snapshot({
      seq: 2,
      entries: [entry({ id: 'ask', text: '@Jim and @Mia go' })],
      member_states: [
        delivery('working', { agent_id: 'jim', request_id: 'jim-1' }),
        delivery('working', { agent_id: 'mia', request_id: 'mia-1' }),
      ],
    }))
    connection = harness.connection
    expect(screen.getByText('Jim is working...')).toBeInTheDocument()
    expect(screen.getByText('Mia is working...')).toBeInTheDocument()
    expect(screen.queryByText(/partial draft/)).toBeNull()
  })

  it('S-36 replaces only the finished agent line with the answer', async () => {
    const harness = await openRoom(net, snapshot({
      seq: 2,
      member_states: [
        delivery('working', { agent_id: 'jim', request_id: 'jim-1' }),
        delivery('working', { agent_id: 'mia', request_id: 'mia-1' }),
      ],
    }))
    connection = harness.connection
    await harness.emit({
      type: 'room_entry',
      room_id: 'launch',
      seq: 3,
      entry: entry({
        id: 'jim-answer',
        seq: 3,
        text: 'Jim is done',
        author: { kind: 'agent', agent_id: 'jim' },
        client_message_id: undefined,
        addressees: [],
      }),
    })
    await harness.emit({
      type: 'room_member_state',
      room_id: 'launch',
      seq: 4,
      delivery: delivery('answered', { agent_id: 'jim', request_id: 'jim-1' }),
    })
    expect(screen.getByText('Jim is done')).toBeInTheDocument()
    expect(screen.queryByText('Jim is working...')).toBeNull()
    expect(screen.getByText('Mia is working...')).toBeInTheDocument()
  })

  it('S-50 and S-98 show one Participants button with the avatar stack and count, not the member list', async () => {
    const harness = await openRoom(net)
    connection = harness.connection
    const button = screen.getByRole('button', { name: /participants/i })
    expect(button).toHaveAccessibleName(/3/)
    expect(screen.queryByRole('button', { name: /^mute$/i }), 'S-98: mute stays in the panel, not the header').toBeNull()
  })
})
