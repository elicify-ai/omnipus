// RED — #1449 US-5, US-7, US-11.
// Oracles: docs/internal/specs/1449-group-chat-spec.md S-35, S-36, S-50, S-98.
// Real RoomScreen. Only the socket and fetch are doubled.
import type { ComponentType } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import {
  Network, ROOM_ID, WS, delivery, entry, mountAt, openRoom, requiredExport, resetRealStores, room, snapshot, stopHarness,
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

  // S-50 examples. avatars is min(members, 3); overflow is members-3 or none.
  it.each([
    { members: ['mia', 'jim'], avatars: 2, overflow: null },
    { members: ['mia', 'jim', 'researcher2'], avatars: 3, overflow: null },
    { members: ['mia', 'jim', 'researcher2', 'gp'], avatars: 3, overflow: '+1' },
    { members: ['m0', 'm1', 'm2', 'm3', 'm4', 'm5', 'm6', 'm7', 'm8', 'm9'], avatars: 3, overflow: '+7' },
  ])('S-50 shows $avatars avatars and $overflow for $members.length members', async ({ members, avatars, overflow }) => {
    await mountParticipantsHeader(members)
    const button = await screen.findByRole('button', { name: /participants/i })
    expect(button, 'S-50: the count reads the full member number').toHaveAccessibleName(
      new RegExp(`Participants,\\s*${members.length}\\s+members`),
    )
    expect(within(button).getAllByTestId('agent-icon'), 'S-50: at most three avatars').toHaveLength(avatars)
    if (overflow) expect(within(button).getByText(overflow, { exact: true })).toBeInTheDocument()
    else expect(button.textContent ?? '').not.toMatch(/\+\d+/)
    expect(screen.queryByRole('button', { name: /^mute$/i }), 'mute stays in the panel, not the header').toBeNull()
  })

  it('S-98 puts the Participants button in the header slot with no bar or border', async () => {
    await mountParticipantsHeader(['mia', 'jim', 'researcher2'])
    const button = await screen.findByRole('button', { name: /participants/i })
    expect(screen.getAllByRole('button', { name: /participants/i })).toHaveLength(1)
    const slot = button.closest('[data-testid="chat-header-slot"]')
    expect(slot, 'S-98: the button is in the header slot').not.toBeNull()
    expect(slot!.className, 'S-98: no border').not.toMatch(/\bborder\b/)
    expect(slot!.className, 'S-98: no surface fill').not.toMatch(/\bbg-/)
    expect(screen.queryByRole('button', { name: /^mute$/i })).toBeNull()
  })
})

/** The header slot on a group address. The button is not inside RoomScreen (US-11, FR-033). */
async function mountParticipantsHeader(members: string[]): Promise<void> {
  const ChatHeaderSlot = await requiredExport<ComponentType<{ workspaceId: string }>>(
    '/src/components/chat/ChatHeaderSlot.tsx',
    'ChatHeaderSlot',
    'S-50; S-98; US-11',
  )
  net.currentRoom = room({ members })
  const mounted = await mountAt(<ChatHeaderSlot workspaceId={WS} />, `/workspaces/${WS}/rooms/${ROOM_ID}`)
  connection = undefined
  void mounted
}
