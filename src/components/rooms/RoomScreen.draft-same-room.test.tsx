// RED round 2 — #1849 CHECK findings F1 and F2.
// Oracle: issue #1849 and the fix brief ("Nothing visible changes inside a
// single room"; a room's unsent draft belongs to that room only). Expected
// values below come from those requirements, never from the code under test.
//
// T1: a draft (text + attached file) typed in room A survives a failed refresh
//     of room A's data and the user's Retry; the route stays on room A.
// T2: a mention picked from room A's menu does not travel to room B: a "@Jim"
//     typed by hand in room B is sent with no explicit mention id.
//
// Same real RoomScreen, stores, router and process-edge doubles as the frozen
// room oracle (RoomScreen.draft-no-cross-room.test.tsx); only fetch and the
// browser WebSocket are doubled, through the shared l1-room-ui harness.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory, createRootRoute, createRoute, createRouter,
  Outlet, RouterProvider,
} from '@tanstack/react-router'
import type { Room } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { roomsQueryKeys } from '@/lib/api/rooms'
import { RoomScreen } from './RoomScreen'
import {
  Network, connectSocket, json, resetRealStores, room, snapshot,
  stopHarness, WS,
} from '../../../tests/l1-room-ui/harness'
import type { ObservedRequest } from '../../../tests/l1-room-ui/harness'

const ROOM_A = 'room-alpha'
const ROOM_B = 'room-beta'
const ROOM_A_NAME = 'Alpha'
const ROOM_B_NAME = 'Beta'
// Arbitrary user-chosen draft content; the oracle is "exactly what was typed".
const DRAFT_TEXT = 'Plan the offsite, '
const DRAFT_WITH_MENTION = 'Plan the offsite, @Jim '
const FILE_NAME = 'offsite-notes.txt'
const MEDIA_REF = 'media://workspace/sales/upl-offsite-1'
// T2: the text the user types by hand in room B, without using the menu.
const TYPED_BY_HAND = '@Jim'
const UPLOAD_RESPONSE = {
  files: [{
    name: FILE_NAME,
    path: `uploads/${ROOM_A}/${FILE_NAME}`,
    size: 27,
    content_type: 'text/plain',
    ref: MEDIA_REF,
  }],
}

// The body-less contract reads that legitimately happen around the room
// screen (same allow-list as the frozen room oracle; contracts/openapi.yaml).
const ALLOWED_READS: ReadonlyArray<{ path: string; query: string }> = [
  { path: '/api/v1/agents', query: '' },
  { path: '/api/v1/skills', query: '' },
  { path: '/api/v1/commands', query: '?surface=web' },
]
function isAllowedRead(request: ObservedRequest): boolean {
  if (request.method !== 'GET' || request.body !== null) return false
  const exact = ALLOWED_READS.some((read) => read.path === request.path && read.query === request.query)
  const roomOrWorkspace = request.query === ''
    && (request.path === `/api/v1/rooms/${ROOM_A}`
      || request.path === `/api/v1/rooms/${ROOM_B}`
      || request.path === `/api/v1/workspaces/${WS}`)
  return exact || roomOrWorkspace
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

afterEach(() => {
  stopHarness(connection)
})

/** The real room route shape: the component reads its params from the router. */
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

async function openRoomA() {
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

/** Pick @Jim through the real mention menu in the current room's box. */
async function pickJimFromMenu(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.type(groupInput(), DRAFT_TEXT)
  await user.type(groupInput(), '@')
  await user.click(screen.getByRole('option', { name: '@Jim' }))
  await waitFor(() => expect(groupInput()).toHaveValue(DRAFT_WITH_MENTION))
}

describe('#1849 a room\'s own draft belongs to that room only', () => {
  it('T1: room A keeps its draft text and attachment through a failed room refresh and Retry', async () => {
    const harness = await openRoomA()
    const user = userEvent.setup()
    // Step 1: in room A, type a draft and attach a file; do not send.
    await pickJimFromMenu(user)
    await user.click(screen.getByRole('button', { name: 'Attach files' }))
    const fileInput = await waitFor(() => {
      const input = document.querySelector<HTMLInputElement>('input[type="file"]')
      if (!input) throw new Error('the attach button did not open its file input')
      return input
    })
    await user.upload(fileInput, new File(['offsite planning notes'], FILE_NAME, { type: 'text/plain' }))
    await screen.findByText(FILE_NAME)
    // Control: the draft exists before the failure (a clean box is not vacuous).
    expect(groupInput()).toHaveValue(DRAFT_WITH_MENTION)

    // Step 2: the room's data refresh fails; the error state with Retry shows
    // and the route stays on room A.
    net.respond('GET', `/api/v1/rooms/${ROOM_A}`, () => json({ error: 'room refresh unavailable' }, 503))
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: roomsQueryKeys.detail(ROOM_A), exact: true })
    })
    await screen.findByTestId('query-error-state')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(harness.router.state.location.pathname, 'the error must not move the route').toBe(`/workspaces/${WS}/rooms/${ROOM_A}`)

    // Step 3: the endpoint recovers and the user clicks Retry.
    net.respond('GET', `/api/v1/rooms/${ROOM_A}`, () => json(roomRecord(ROOM_A, ROOM_A_NAME)))
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await screen.findByTestId('room-header')
    expect(harness.router.state.location.pathname, 'Retry stays in room A').toBe(`/workspaces/${WS}/rooms/${ROOM_A}`)

    // Pass condition: exactly the same draft text and the same attachment.
    expect(groupInput(), '1849: Retry in the same room must not discard the unsent draft text').toHaveValue(DRAFT_WITH_MENTION)
    expect(screen.getByText(FILE_NAME), '1849: Retry in the same room must not discard the staged attachment').toBeInTheDocument()
  })

  it('T2: a mention picked in room A is not sent from room B when "@Jim" is typed by hand there', async () => {
    const harness = await openRoomA()
    const user = userEvent.setup()
    // Steps 1-2: pick @Jim from the menu in room A, then go to room B unsent.
    await pickJimFromMenu(user)
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    // Control: room B starts with an empty box (the text did not travel), so
    // any inherited mention id could only come from a hidden carried pick.
    expect(groupInput()).toHaveValue('')

    // Step 3: type the same text by hand in room B, without the menu, and Send.
    const requestsBeforeSend = net.requests.length
    await user.type(groupInput(), TYPED_BY_HAND)
    expect(groupInput()).toHaveValue(TYPED_BY_HAND)
    await user.click(screen.getByRole('button', { name: /^send/i }))
    await waitFor(() => {
      expect(net.requests.slice(requestsBeforeSend).some((request) => request.method === 'POST')).toBe(true)
    })

    // The complete B-bound request list after the click, apart from the
    // allow-listed body-less reads, is exactly the one expected send.
    const remaining = net.requests.slice(requestsBeforeSend).filter((request) => !isAllowedRead(request))
    expect(
      remaining.map((request) => ({ method: request.method, path: request.path, query: request.query, body: request.body })),
      '1849: exactly one request, the typed send to room B, with no inherited explicit mention id',
    ).toEqual([{
      method: 'POST',
      path: `/api/v1/rooms/${ROOM_B}/messages`,
      query: '',
      body: { client_message_id: expect.any(String), text: TYPED_BY_HAND, mentions: [] },
    }])
  })
})
