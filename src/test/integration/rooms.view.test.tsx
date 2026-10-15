// RED: WU-15 SPA room view. Each oracle is a spec label/outcome.
// Backend effects (admission, cost allocation, audit, pause fences) are not
// proven by these socket fixtures; see the browser/UAT rows in the RED plan.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RoomDeliveryState, RoomEntryFrame, RoomMetaFrame } from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import {
  Network, connectSocket, delivery, entry, json, loadRoomScreen, loadRoomStore,
  mountAt, openRoom, resetRealStores, ROOM_ID, room, snapshot, stopHarness, WS,
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

function meta(overrides: Partial<RoomMetaFrame> = {}): RoomMetaFrame {
  return {
    type: 'room_meta', room_id: ROOM_ID, seq: 2,
    members: ['mia', 'jim', 'researcher2'], muted: [], paused: false,
    generation: 0, allocated_cost_micro_usd: 10_000,
    allocated_cost_unpriced: false, ...overrides,
  }
}

async function compose(text: string) {
  const user = userEvent.setup()
  const input = screen.getByRole('textbox')
  await user.type(input, text)
  await user.click(screen.getByRole('button', { name: /^send(?: message)?$/i }))
  return user
}

describe('RoomScreen header and request states — FR-171/175/201; §6.11', () => {
  it('[US-17 AS-6; BDD-17.7/30] shows current members and $0.01 allocated cost, not the whole main-turn cost', async () => {
    await open()
    for (const name of ['Mia', 'Jim', 'Researcher2']) expect(screen.getByText(name, { exact: true })).toBeVisible()
    expect(screen.getByText(/allocated cost/i)).toHaveTextContent(/allocated cost/i)
    expect(screen.getByText('$0.01', { exact: true })).toBeVisible()
    expect(screen.queryByText('$0.03', { exact: true })).toBeNull()
    expect(screen.queryByText('plus unpriced turns', { exact: true })).toBeNull()
  })

  it('[FR-171; §6.14; BDD-17.30] an unpriced turn adds its warning without inventing a price', async () => {
    net.currentRoom = room({ allocated_cost_unpriced: true })
    await open(snapshot({ allocated_cost_unpriced: true }))
    expect(screen.getByText('plus unpriced turns', { exact: true })).toBeVisible()
    expect(screen.getByText('$0.01', { exact: true })).toBeVisible()
  })

  it('[FR-171; K45] live metadata updates the cost and unpriced flag without adding the old total twice', async () => {
    const harness = await open()
    await harness.emit(meta({ allocated_cost_micro_usd: 20_000, allocated_cost_unpriced: true }))
    expect(await screen.findByText('$0.02', { exact: true })).toBeVisible()
    expect(screen.getByText('plus unpriced turns', { exact: true })).toBeVisible()
    expect(screen.queryByText('$0.01', { exact: true })).toBeNull()
  })

  const labels: Array<{ state: RoomDeliveryState['state']; reason?: string; label: string }> = [
    { state: 'recorded', label: 'sending…' },
    { state: 'awaiting_approval', label: "awaiting your approval — in Jim's main chat" },
    { state: 'admitted', label: 'queued' },
    { state: 'held', label: 'held — Jim is stopped' },
    { state: 'working', label: 'working' },
    { state: 'waiting_on_user', label: "waiting for you in Jim's main chat" },
    { state: 'refused', label: 'refused' },
    { state: 'failed', reason: 'provider', label: 'failed: provider' },
    { state: 'interrupted', label: 'interrupted' },
    { state: 'unavailable', reason: 'rate_limit', label: 'unavailable: rate limit' },
    { state: 'cancelled', label: 'cancelled' },
    { state: 'cancelled', reason: 'removed', label: 'cancelled — removed' },
    { state: 'unknown', label: 'unknown' },
  ]

  it.each(labels)('[FR-201/213/252; §6.11; BDD-17.14/26/33] $state ($reason) renders "$label"', async ({ state, reason, label }) => {
    await open(snapshot({ member_states: [delivery(state, reason ? { reason } : {})] }))
    expect(await screen.findByText(label, { exact: true })).toBeVisible()
    if (state === 'unknown' || state === 'held') expect(screen.queryByText('working', { exact: true })).toBeNull()
    if (state === 'interrupted') expect(screen.getByRole('button', { name: /^ask again$/i })).toBeEnabled()
    if (state === 'awaiting_approval' || state === 'waiting_on_user') {
      const link = screen.getByRole('link', { name: /Jim.*main chat|main chat.*Jim/i })
      expect(link.getAttribute('href')).toMatch(/main-jim-sales|agent_id=jim/)
      expect(screen.queryByRole('button', { name: /approve|deny|save secret/i })).toBeNull()
    }
  })

  it('[FR-166/201; §6.11] answered is represented by Jim’s authored answer, not a fabricated working status', async () => {
    await open(snapshot({
      entries: [entry({ author: { kind: 'agent', agent_id: 'jim' }, text: 'The launch plan is ready.', client_message_id: undefined })],
      member_states: [delivery('answered')],
    }))
    expect(screen.getByText('The launch plan is ready.', { exact: true })).toBeVisible()
    expect(screen.getAllByText('Jim', { exact: true }).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText('working', { exact: true })).toBeNull()
  })

  it('[FR-201; §6.11] a late update of an older request never replaces the newest request’s held state', async () => {
    const harness = await open(snapshot({
      seq: 3,
      entries: [entry({ deliveries: [delivery('admitted', { request_id: 'jim-old' })] }), entry({ id: 'room-message-2', seq: 2, text: 'second request', deliveries: [delivery('held', { request_id: 'jim-new' })] })],
      member_states: [delivery('admitted', { request_id: 'jim-old' }), delivery('held', { request_id: 'jim-new' })],
    }))
    await harness.emit({ type: 'room_member_state', room_id: ROOM_ID, seq: 4, delivery: delivery('working', { request_id: 'jim-old' }) })
    const jim = screen.getByTestId('room-member-state-jim')
    expect(jim).toHaveTextContent('held — Jim is stopped')
    expect(jim).not.toHaveTextContent('working')
  })

  it('[FR-201; K45; SC-019] a live state in a different room cannot contaminate the selected room', async () => {
    const harness = await open(snapshot({ member_states: [delivery('held')] }))
    await harness.emit({ type: 'room_member_state', room_id: 'other-room', seq: 2, delivery: delivery('working') })
    expect(screen.getByText('held — Jim is stopped', { exact: true })).toBeVisible()
    expect(screen.queryByText('working', { exact: true })).toBeNull()
  })
})

describe('RoomScreen transcript, pause and reachability — FR-166/170/217; K45', () => {
  it('[AS-3/4; BDD-17.3/4] renders ordered user requests and agent-authored answers from the real room frames', async () => {
    const harness = await open(snapshot({ entries: [entry()] }))
    const answer: RoomEntryFrame = { type: 'room_entry', room_id: ROOM_ID, seq: 2, entry: entry({
      id: 'jim-answer-1', seq: 2, author: { kind: 'agent', agent_id: 'jim' },
      client_message_id: undefined, text: '@Mia can you draft the email?', addressees: ['mia'],
    }) }
    await harness.emit(answer)
    expect(screen.getByText('status?', { exact: true })).toBeVisible()
    const reply = await screen.findByText('@Mia can you draft the email?', { exact: true })
    expect(reply).toBeVisible()
    expect(screen.getAllByText('Jim', { exact: true }).length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('status?', { exact: true }).compareDocumentPosition(reply) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  })

  it('[AS-14; FR-216; BDD-17.27/32; K45] a replayed entry id is rendered exactly once', async () => {
    const first = entry()
    const harness = await open(snapshot({ entries: [first] }))
    await harness.emit({ type: 'room_entry', room_id: ROOM_ID, seq: 1, entry: first })
    expect(screen.getAllByText('status?', { exact: true })).toHaveLength(1)
  })

  it('[AS-5/14; FR-170/217; BDD-17.17/29] an after-pause answer keeps that mark across resume metadata', async () => {
    const harness = await open(snapshot({ paused: true, generation: 1 }))
    await harness.emit({ type: 'room_entry', room_id: ROOM_ID, seq: 2, entry: entry({
      id: 'jim-after-pause', seq: 2, author: { kind: 'agent', agent_id: 'jim' }, text: '@Mia finished after the pause',
      after_pause: true, addressees: [], client_message_id: undefined,
    }) })
    expect(await screen.findByText('after pause', { exact: true })).toBeVisible()
    await harness.emit(meta({ seq: 3, generation: 1, paused: false }))
    expect(screen.getByText('after pause', { exact: true })).toBeVisible()
    expect(screen.getAllByText('@Mia finished after the pause', { exact: true })).toHaveLength(1)
    expect(net.writes()).toEqual([])
  })

  it('[AS-5; FR-170; BDD-17.6] a paused room still records a user message through its room endpoint', async () => {
    net.currentRoom = room({ paused: true, generation: 1 })
    await open(snapshot({ paused: true, generation: 1 }))
    expect(screen.getByText('paused', { exact: false })).toBeVisible()
    await compose('note while paused')
    await waitFor(() => expect(net.writes()).toHaveLength(1))
    const write = net.writes()[0]!
    expect({ method: write.method, path: write.path, body: write.body }).toEqual({
      method: 'POST', path: `/api/v1/rooms/${ROOM_ID}/messages`,
      body: { client_message_id: expect.any(String), text: 'note while paused', mentions: [] },
    })
  })

  it('[FR-204; BDD-17.20] a one-agent room displays the server’s read-only reason and blocks sending', async () => {
    const reason = 'a room needs at least two agents'
    net.currentRoom = room({ members: ['mia'], read_only_reason: reason })
    await open(snapshot({ members: ['mia'], read_only_reason: reason }))
    expect(screen.getByText(reason, { exact: true })).toBeVisible()
    expect(screen.getByRole('button', { name: /^send(?: message)?$/i })).toBeDisabled()
    expect(net.writes()).toEqual([])
  })

  it('[§6.3; K23] a failed room read exposes Error and Retry, never an empty successful room', async () => {
    const RoomScreen = await loadRoomScreen()
    const store = await loadRoomStore()
    act(() => store.setState(store.getInitialState(), true))
    const linked = connectSocket(); connection = linked.connection
    net.respond('GET', `/api/v1/rooms/${ROOM_ID}`, () => json({ error: 'room unavailable' }, 404))
    await mountAt(<RoomScreen workspaceId={WS} roomId={ROOM_ID} />)
    expect(await screen.findByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(screen.getByText(/could not|failed|unavailable/i)).toBeVisible()
    const send = screen.queryByRole('button', { name: /^send(?: message)?$/i })
    if (send) expect(send).toBeDisabled()
    expect(net.writes()).toEqual([])
  })

  it('[§6.3; K45] an incomplete snapshot cannot enable sending before the room is resolved', async () => {
    await open(snapshot({ complete: false }))
    expect(screen.getByRole('button', { name: /^send(?: message)?$/i })).toBeDisabled()
    expect(net.writes()).toEqual([])
  })

  it('[§6.3; K45] a disconnected room retains its transcript, says reconnecting, and blocks sending', async () => {
    await open(snapshot({ entries: [entry()] }))
    act(() => useConnectionStore.getState().setConnected(false))
    expect(screen.getByText('status?', { exact: true })).toBeVisible()
    expect(screen.getByText(/reconnecting/i)).toBeVisible()
    expect(screen.getByRole('button', { name: /^send(?: message)?$/i })).toBeDisabled()
  })

  it('[§6.3; K26] uncertain message save says not confirmed and Retry preserves the client message id', async () => {
    await open()
    net.respond('POST', `/api/v1/rooms/${ROOM_ID}/messages`, () => { throw new TypeError('network disconnected') })
    await compose('keep this exact request')
    expect(await screen.findByText(/not confirmed/i)).toBeVisible()
    const first = net.writes()[0]!
    expect(first.body).toEqual({ client_message_id: expect.any(String), text: 'keep this exact request', mentions: [] })
    net.respond('POST', `/api/v1/rooms/${ROOM_ID}/messages`, () => new Response(null, { status: 202 }))
    await userEvent.setup().click(screen.getByRole('button', { name: /^retry$/i }))
    await waitFor(() => expect(net.writes()).toHaveLength(2))
    expect(net.writes()[1]?.body).toEqual(first.body)
  })

  it('[FR-207; BDD-17.24] a room created from a main chat starts empty and has a link back to that main', async () => {
    net.currentRoom = room({ kind: 'adhoc', members: ['mia', 'jim'], origin_session_id: 'main-mia-sales' })
    await open(snapshot({ members: ['mia', 'jim'], entries: [] }))
    const back = screen.getByRole('link', { name: /Mia.*main chat|main chat.*Mia|back to.*chat/i })
    expect(back.getAttribute('href')).toMatch(/main-mia-sales|agent_id=mia/)
    const log = screen.getByRole('log')
    expect(within(log).queryByText('status?', { exact: true })).toBeNull()
    expect(net.writes()).toEqual([])
  })
})
