// RED — US-17 AS-3/12, FR-164/168/175/189/205/206, §6.13, K26.
// REAL RoomScreen, stores, QueryClient, parser and room API. Only HTTP/socket
// edges are doubled. Server fan-out and command execution remain backend/UAT.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RoomMessageRequest } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import {
  agents, Network, entry, json, openRoom, resetRealStores, room, ROOM_ID,
  snapshot, stopHarness, WS,
} from '../../../tests/l1-room-ui/harness'

let net: Network
let connection: WsConnection | undefined
beforeEach(() => { resetRealStores(); net = new Network(); connection = undefined })
afterEach(() => stopHarness(connection))

async function open(state = snapshot()) {
  const harness = await openRoom(net, state)
  connection = harness.connection
  return harness
}

async function sendText(text: string) {
  const user = userEvent.setup()
  await user.type(screen.getByRole('textbox'), text)
  await user.click(screen.getByRole('button', { name: /^send(?: message)?$/i }))
}

async function posted(): Promise<RoomMessageRequest> {
  await waitFor(() => expect(net.writes()).toHaveLength(1))
  const write = net.writes()[0]!
  expect(write.method).toBe('POST')
  expect(write.path).toBe(`/api/v1/rooms/${ROOM_ID}/messages`)
  const request = write.body as RoomMessageRequest
  expect(request.client_message_id).toEqual(expect.any(String))
  expect(request.client_message_id.length).toBeGreaterThan(0)
  return request
}

describe('Room composer — user addressing, not main-chat steering', () => {
  it('[FR-164; BDD-17.3] ordinary text goes to the room with no invented explicit mentions', async () => {
    const harness = await open()
    await sendText('status?')
    expect(await posted()).toEqual({ client_message_id: expect.any(String), text: 'status?', mentions: [] })
    expect(harness.socket.sent.filter((frame) => frame.type === 'message')).toEqual([])
  })

  it('[FR-175; §6.13; BDD-17.3] the @ picker offers @all and exactly the current room members, not the entire workspace', async () => {
    net.currentRoom = room({ members: ['mia', 'jim'] })
    await open(snapshot({ members: ['mia', 'jim'] }))
    await userEvent.setup().type(screen.getByRole('textbox'), '@')
    expect(await screen.findByRole('option', { name: /^@?all$/i })).toBeVisible()
    expect(screen.getByRole('option', { name: /Mia/ })).toBeVisible()
    expect(screen.getByRole('option', { name: /Jim/ })).toBeVisible()
    expect(screen.getAllByRole('option')).toHaveLength(3)
    for (const excluded of ['Researcher2', 'Ray', 'GP Worker', 'External Worker', 'Judge', 'Omnipus']) {
      expect(screen.queryByRole('option', { name: new RegExp(excluded) })).toBeNull()
    }
  })

  it('[FR-158/164; BDD-17.18; §6.13] a muted member stays in the @ picker and can be explicitly addressed', async () => {
    net.currentRoom = room({ muted: ['jim'] })
    await open(snapshot({ muted: ['jim'] }))
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox'), '@')
    await user.click(await screen.findByRole('option', { name: /Jim/ }))
    await user.type(screen.getByRole('textbox'), ' anything?')
    await user.click(screen.getByRole('button', { name: /^send(?: message)?$/i }))
    expect((await posted()).mentions).toEqual(['jim'])
  })

  it('[FR-164; §6.13; K26] choosing @all carries the reserved all token, not member display names', async () => {
    await open()
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox'), '@')
    await user.click(await screen.findByRole('option', { name: /^@?all$/i }))
    await user.type(screen.getByRole('textbox'), ' go')
    await user.click(screen.getByRole('button', { name: /^send(?: message)?$/i }))
    const request = await posted()
    expect(request.mentions).toEqual(['all'])
    expect(request.text).toContain('go')
  })

  it('[§6.13; BDD-17.31; K26] a picked agent id survives a display-name change before send', async () => {
    net.roster = agents.map((agent) => agent.id === 'jim' ? { ...agent, name: 'Jim Smith' } : agent)
    await open()
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox'), '@')
    await user.click(await screen.findByRole('option', { name: /Jim Smith/ }))
    await user.type(screen.getByRole('textbox'), ' review this')
    net.roster = net.roster.map((agent) => agent.id === 'jim' ? { ...agent, name: 'James' } : agent)
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['agents'] }) })
    expect(await screen.findByText('James', { exact: true })).toBeVisible()
    await user.click(screen.getByRole('button', { name: /^send(?: message)?$/i }))
    expect((await posted()).mentions).toEqual(['jim'])
  })

  it('[§6.13; BDD-17.31] an ambiguous typed name shows the exact refusal and never looks delivered', async () => {
    const duplicate = { ...agents[0]!, id: 'mia2' }
    net.roster = [...agents, duplicate]
    net.workspaces[0] = { ...net.workspaces[0]!, member_configs: { ...net.workspaces[0]!.member_configs, mia2: { main_session_id: 'main-mia2-sales' } } }
    net.currentRoom = room({ members: ['mia', 'mia2', 'jim'] })
    net.respond('POST', `/api/v1/rooms/${ROOM_ID}/messages`, () => json({ error: 'Several members are called Mia — pick from the list' }, 422))
    await open(snapshot({ members: ['mia', 'mia2', 'jim'] }))
    await sendText('@Mia hello')
    expect(await screen.findByText('Several members are called Mia — pick from the list', { exact: true })).toBeVisible()
    expect(screen.queryByText('working', { exact: true })).toBeNull()
  })

  it('[§6.13; BDD-17.31] a typed short name is kept as text and is not converted into a picker id', async () => {
    net.roster = agents.map((agent) => agent.id === 'jim' ? { ...agent, name: 'Jim Smith' } : agent)
    await open()
    await sendText('@Jim go')
    expect(await posted()).toEqual({ client_message_id: expect.any(String), text: '@Jim go', mentions: [] })
  })

  it('[§6.13; K26] deleting a picked mention cannot keep addressing that agent invisibly', async () => {
    await open()
    const user = userEvent.setup()
    const input = screen.getByRole('textbox')
    await user.type(input, '@')
    await user.click(await screen.findByRole('option', { name: /Jim/ }))
    await user.clear(input)
    await user.type(input, 'ordinary note')
    await user.click(screen.getByRole('button', { name: /^send(?: message)?$/i }))
    expect(await posted()).toEqual({ client_message_id: expect.any(String), text: 'ordinary note', mentions: [] })
  })
})

describe('FR-205 reduced command set — room requests never call main-chat command controls', () => {
  it.each(['/pause', '/resume', '/mute @Jim', '/unmute @Jim', '/members', '/help'])(
    '[FR-205/206; BDD-17.21] %s is sent as room text with no agent-session command frame', async (text) => {
      net.currentRoom = room({ muted: ['jim'], paused: text === '/resume' })
      const harness = await open(snapshot({ muted: ['jim'], paused: text === '/resume' }))
      await sendText(text)
      expect(await posted()).toEqual({ client_message_id: expect.any(String), text, mentions: [] })
      expect(harness.socket.sent.filter((frame) => ['message', 'cancel', 'model_switch', 'goal_start'].includes(frame.type))).toEqual([])
      expect(net.writes().map((write) => write.path)).toEqual([`/api/v1/rooms/${ROOM_ID}/messages`])
    },
  )

  const refusals = [
    { command: '/model gpt-x', members: ['Mia', 'Jim', 'Researcher2'] },
    { command: '/clear', members: ['Mia', 'Jim', 'Researcher2'] },
    { command: '/stop', members: ['Mia', 'Jim', 'Researcher2'] },
    { command: '/goal @Jim ship it', members: ['Jim'] },
    { command: '/remember x', members: ['Mia', 'Jim', 'Researcher2'] },
    { command: '/add @Ray', members: ['Mia', 'Jim', 'Researcher2'] },
  ]
  it.each(refusals)('[FR-205; BDD-17.22/23] $command is visibly refused with links to $members main chats', async ({ command, members }) => {
    const harness = await open()
    // Transport refusal is the process edge. A local refusal is equally valid;
    // neither path may invoke a main-chat command or mutate membership.
    // S-13 / Q1: the spec sentence uses a hyphen, not an em dash.
    net.respond('POST', `/api/v1/rooms/${ROOM_ID}/messages`, () => json({ error: "Rooms don't steer agents - use Jim's main chat" }, 422))
    await sendText(command)
    expect(await screen.findByText(/Rooms don't steer agents - use .*main chat/)).toBeVisible()
    for (const member of members) {
      const link = screen.getByRole('link', { name: new RegExp(`${member}.*main chat|main chat.*${member}`, 'i') })
      expect(link.getAttribute('href')).toMatch(new RegExp(`main-${member.toLowerCase()}-sales|agent_id=${member.toLowerCase()}`))
    }
    if (members.length === 1) {
      expect(screen.queryByRole('link', { name: /Mia.*main chat|main chat.*Mia/i })).toBeNull()
      expect(screen.queryByRole('link', { name: /Researcher2.*main chat|main chat.*Researcher2/i })).toBeNull()
    }
    expect(harness.socket.sent.filter((frame) => frame.type === 'message' || frame.type === 'cancel')).toEqual([])
    expect(net.writes().every((write) => write.method === 'POST' && write.path === `/api/v1/rooms/${ROOM_ID}/messages`)).toBe(true)
    expect(net.requests.some((request) => request.path === `/api/v1/workspaces/${WS}/rooms` && request.method === 'POST')).toBe(false)
  })

  it('[FR-168/189; BDD-17.8/23; BDD-19.7] an agent’s /pause text stays visible input and never executes a command', async () => {
    const harness = await open()
    await harness.emit({ type: 'room_entry', room_id: ROOM_ID, seq: 2, entry: entry({
      id: 'jim-command-looking-text', seq: 2, author: { kind: 'agent', agent_id: 'jim' },
      text: '/pause', client_message_id: undefined, addressees: [],
    }) })
    expect(await screen.findByText('/pause', { exact: true })).toBeVisible()
    expect(net.writes()).toEqual([])
    expect(harness.socket.sent.filter((frame) => frame.type === 'message' || frame.type === 'cancel')).toEqual([])
    expect(screen.queryByText('paused', { exact: true })).toBeNull()
  })
})
