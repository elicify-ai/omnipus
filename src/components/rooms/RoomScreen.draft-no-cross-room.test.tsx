// RED — #1849: an unsent group-room draft must not follow the user into another room.
// Oracle: gh issue view 1849 — room B's box does not show room A's draft text,
// files or mentions, and no send to room B carries room A's draft. The exact
// expected values (empty box; no POST body carrying room A's text, mention id
// or media ref) come from the issue, never from the code under test.
//
// Real RoomScreen, real stores, real router navigation between two room
// addresses (the sidebar's group-chat link mechanism: a route param change,
// no reload). Only the network edge is doubled: fetch (request-recording
// stub) and the browser WebSocket class (frame-recording stub below the real
// parser). The draft is driven through the real box: typing, the real @-picker
// and the real attach button with its real upload adapter.
//
// Brief item 3: what the box holds when the user RETURNS to room A is not
// decided (kept or cleared) — no test asserts it; see the RED report.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  QueryClientProvider,
} from '@tanstack/react-query'
import {
  createMemoryHistory, createRootRoute, createRoute, createRouter,
  Outlet, RouterProvider,
} from '@tanstack/react-router'
import type { Room } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { RoomScreen } from './RoomScreen'
import {
  Network, connectSocket, json, resetRealStores, room, snapshot,
  stopHarness, WS,
} from '../../../tests/l1-room-ui/harness'

const ROOM_A = 'room-alpha'
const ROOM_B = 'room-beta'
const ROOM_A_NAME = 'Alpha'
const ROOM_B_NAME = 'Beta'
// Arbitrary user-chosen draft content; only the issue's required behaviour
// (the other room's box holds none of it) is the oracle.
const DRAFT_TEXT = 'Plan the offsite, '
const DRAFT_WITH_MENTION = 'Plan the offsite, @Jim '
const FILE_NAME = 'offsite-notes.txt'
// UploadFilesResponse (contracts): the room attachment adapter reads
// files[0].ref and files[0].content_type off this body.
const UPLOAD_RESPONSE = {
  files: [{
    name: FILE_NAME,
    path: `uploads/${ROOM_A}/${FILE_NAME}`,
    size: 27,
    content_type: 'text/plain',
    ref: 'media://workspace/sales/upl-offsite-1',
  }],
}

function roomRecord(id: string, name: string): Room {
  return room({ id, name })
}

let net: Network
let connection: WsConnection | undefined

beforeEach(() => {
  resetRealStores()
  net = new Network()
  net.rooms = [roomRecord(ROOM_A, ROOM_A_NAME), roomRecord(ROOM_B, ROOM_B_NAME)]
  net.currentRoom = roomRecord(ROOM_A, ROOM_A_NAME)
  net.respond('GET', `/api/v1/rooms/${ROOM_A}`, () => json(roomRecord(ROOM_A, ROOM_A_NAME)))
  net.respond('GET', `/api/v1/rooms/${ROOM_B}`, () => json(roomRecord(ROOM_B, ROOM_B_NAME)))
  net.respond('POST', `/api/v1/rooms/${ROOM_A}/messages`, () => new Response(null, { status: 202 }))
  net.respond('POST', `/api/v1/rooms/${ROOM_B}/messages`, () => new Response(null, { status: 202 }))
  net.respond('POST', '/api/v1/upload', () => json(UPLOAD_RESPONSE, 201))
  connection = undefined
})

afterEach(() => stopHarness(connection))

/**
 * The real room route shape (src/routes/_app/workspaces.$workspaceId.rooms.$roomId.tsx):
 * the component reads the params from the ROUTER, so navigating to the other
 * room's address re-renders it with the new roomId and no remount — exactly
 * what a sidebar room link does.
 */
async function mountRoomRouter(initialPath: string) {
  const root = createRootRoute({
    component: () => <QueryClientProvider client={queryClient}><Outlet /></QueryClientProvider>,
  })
  const app = createRoute({ getParentRoute: () => root, id: '_app', component: Outlet })
  const wsRoute = createRoute({ getParentRoute: () => app, path: 'workspaces/$workspaceId', component: Outlet })
  const roomRoute = createRoute({
    getParentRoute: () => wsRoute,
    path: 'rooms/$roomId',
    component: function RoomRoute() {
      const { workspaceId, roomId } = roomRoute.useParams()
      return <RoomScreen workspaceId={workspaceId} roomId={roomId} />
    },
  })
  const router = createRouter({
    routeTree: root.addChildren([app.addChildren([wsRoute.addChildren([roomRoute])])]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
    defaultPendingMinMs: 0,
  })
  await act(async () => { await router.load() })
  const result = render(<RouterProvider router={router} />)
  return { ...result, router }
}

/** The room navigate's exact shape (typed against the harness's built tree). */
type RoomNavigate = (opts: {
  to: '/workspaces/$workspaceId/rooms/$roomId'
  params: { workspaceId: string; roomId: string }
}) => Promise<void>

async function goToRoom(
  navigate: RoomNavigate,
  socket: ReturnType<typeof connectSocket>['socket'],
  emit: ReturnType<typeof connectSocket>['emit'],
  roomId: string,
): Promise<void> {
  await act(async () => {
    await navigate({
      to: '/workspaces/$workspaceId/rooms/$roomId',
      params: { workspaceId: WS, roomId },
    })
  })
  await waitFor(() => {
    if (!socket.sent.some((frame) => frame.type === 'attach_room' && frame.room_id === roomId)) {
      throw new Error(`RoomScreen must attach ${roomId} on the existing hub connection`)
    }
  })
  await emit(snapshot({ room_id: roomId }))
  await screen.findByTestId('room-header')
  expect(screen.getByTestId('room-header')).toHaveTextContent(roomId === ROOM_B ? ROOM_B_NAME : ROOM_A_NAME)
}

/** Open room A the way the user does, through the router. */
async function openRoomA(): Promise<Awaited<ReturnType<typeof mountRoomRouter>> & {
  socket: ReturnType<typeof connectSocket>['socket']
  emit: ReturnType<typeof connectSocket>['emit']
}> {
  const { connection: conn, socket, emit } = connectSocket()
  connection = conn
  const harness = await mountRoomRouter(`/workspaces/${WS}/rooms/${ROOM_A}`)
  await waitFor(() => {
    if (!socket.sent.some((frame) => frame.type === 'attach_room' && frame.room_id === ROOM_A)) {
      throw new Error('RoomScreen must attach its room on the existing hub connection')
    }
  })
  await emit(snapshot({ room_id: ROOM_A }))
  await screen.findByTestId('room-header')
  return { ...harness, socket, emit }
}

const groupInput = () => screen.getByRole('combobox', { name: 'Message the group' })

/**
 * Type the draft in room A through the real box: text, then the real @-picker,
 * then a file through the real attach button and its real upload adapter.
 */
async function typeDraftInRoomA(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  const input = groupInput()
  await user.type(input, DRAFT_TEXT)
  await user.type(input, '@')
  // FR-158/§6.13: the picker lists the current members; picking Jim inserts
  // the @Jim token bound to his agent id.
  await user.click(screen.getByRole('option', { name: '@Jim' }))
  await waitFor(() => expect(groupInput()).toHaveValue(DRAFT_WITH_MENTION))
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  const fileInput = await waitFor(() => {
    const input = document.querySelector<HTMLInputElement>('input[type="file"]')
    if (!input) throw new Error('the attach button did not open its file input')
    return input
  })
  await user.upload(fileInput, new File(['offsite planning notes'], FILE_NAME, { type: 'text/plain' }))
  // The chip completes when the real adapter's upload answers (US-3 AS 3.3).
  await screen.findByText(FILE_NAME)
}

describe('#1849 an unsent room draft must not follow the user into another room', () => {
  it('control: the draft stays in room A\'s box while the user stays in room A', async () => {
    const harness = await openRoomA()
    const user = userEvent.setup()
    await typeDraftInRoomA(user)
    // Brief item 4: staying in the room, the box holds exactly what was typed,
    // the mention token included, with the file chip staged. This proves the
    // harness sees the real box and its draft.
    expect(groupInput()).toHaveValue(DRAFT_WITH_MENTION)
    expect(screen.getByText(FILE_NAME)).toBeInTheDocument()
    expect(harness.router.state.location.pathname).toBe(`/workspaces/${WS}/rooms/${ROOM_A}`)
  })

  it('RED: room B\'s box does not show room A\'s draft text or its mention token', async () => {
    const harness = await openRoomA()
    const user = userEvent.setup()
    // Issue #1849 steps: visit room B once (the user uses both rooms), come
    // back to room A, write the draft, then change the route to room B.
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_A)
    await typeDraftInRoomA(user)
    expect(groupInput()).toHaveValue(DRAFT_WITH_MENTION)
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    // Issue requirement: room B's box does not show room A's draft text —
    // the mention token is part of that text.
    expect(groupInput(), '1849: room B\'s box must not hold room A\'s draft').toHaveValue('')
  })

  it('RED: room B\'s box does not show room A\'s staged file', async () => {
    const harness = await openRoomA()
    const user = userEvent.setup()
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_A)
    await typeDraftInRoomA(user)
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    // Issue requirement: room B's box does not show room A's draft files.
    expect(screen.queryByText(FILE_NAME), '1849: room A\'s staged file must not appear in room B\'s box').toBeNull()
  })

  it('RED: a Send in room B posts nothing from room A (body and target room checked)', async () => {
    const harness = await openRoomA()
    const user = userEvent.setup()
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_A)
    await typeDraftInRoomA(user)
    const writesBefore = net.writes().filter((write) => write.path.endsWith('/messages')).length
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    await user.click(screen.getByRole('button', { name: /^send/i }))
    // Give the real submit path its microtasks, then read the recorded edge.
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)) })
    // Issue requirement: no send to room B carries room A's draft. With a
    // clean box the Send is disabled and nothing is posted at all; the
    // recorded message writes after arriving in room B must stay empty, for
    // room B's endpoint (target id) and for room A's.
    const messageWrites = net.writes().filter((write) => write.path.endsWith('/messages'))
    expect(messageWrites.slice(writesBefore), '1849: no send after arriving in room B may carry room A\'s draft (checked body and target room)').toEqual([])
  })
})
