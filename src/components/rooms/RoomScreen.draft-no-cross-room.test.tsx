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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
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
// The mention id the real @-picker binds (Jim is a room member) and the
// media ref the real upload adapter registers — the send body must carry
// neither anywhere in the recorded transport log.
const MENTIONED_MEMBER_ID = 'jim'
const MEDIA_REF = 'media://workspace/sales/upl-offsite-1'
// Revision 1 (CHECK F1): the sentinel is a second, known send after the
// checked Send click. Its POST must reach room B, which proves the recorded
// log observed activity strictly after the click — a clean log is never
// vacuously true.
const MARKER = 'room B marker sent after the checked send'
// Virtual-time windows for the fake-timer proof. The first settles the checked
// Send (a build that merely delays the submit — CHECK mutant M4 added 250 ms —
// fires inside it); the second runs every remaining pending timer before the
// recorded log is read. An unbounded runAllTimersAsync cannot be used because
// the real WsConnection keeps self-rescheduling its heartbeat timer, so the
// timer queue never drains; both windows are generous multiples of the known
// scheduling delays while staying bounded.
const SENTINEL_SETTLE_MS = 1_000
const QUEUED_SUBMIT_FLUSH_MS = 120_000
// UploadFilesResponse (contracts): the room attachment adapter reads
// files[0].ref and files[0].content_type off this body.
const UPLOAD_RESPONSE = {
  files: [{
    name: FILE_NAME,
    path: `uploads/${ROOM_A}/${FILE_NAME}`,
    size: 27,
    content_type: 'text/plain',
    ref: MEDIA_REF,
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

afterEach(() => {
  // A revised send test enables fake timers mid-test; never leak them into
  // the next test (or into stopHarness's waits).
  vi.useRealTimers()
  stopHarness(connection)
})

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
    await goToRoom((opts) => harness.router.navigate(opts), harness.socket, harness.emit, ROOM_B)
    // Revision 1 (CHECK F1): the submit path runs under fake timers so the
    // proof is not "not within 50 ms". The clicks and the flush run on the
    // fake clock (fireEvent — the suite's established way to act under fake
    // timers, because @testing-library/react's asyncWrapper drains a faked
    // setTimeout that only `jest` would advance), a SECOND known send (the
    // sentinel) proves the log observed activity after the click, and then
    // every remaining pending timer runs before the whole recorded log is
    // read — a build that merely delays the submit (M4: 250 ms) still fires
    // inside the flush and dies.
    vi.useFakeTimers()
    try {
      const sendButton = () => screen.getByRole('button', { name: /^send/i })
      fireEvent.click(sendButton())
      await act(async () => { await vi.advanceTimersByTimeAsync(SENTINEL_SETTLE_MS) })
      // The sentinel: a second, known send after the checked Send.
      fireEvent.change(groupInput(), { target: { value: MARKER } })
      fireEvent.click(sendButton())
      await act(async () => { await vi.advanceTimersByTimeAsync(QUEUED_SUBMIT_FLUSH_MS) })
      // Instrument control: the sentinel reached room B. Without it, a log
      // holding nothing could also mean the transport recorded nothing at all.
      const markerWrite = net.writes().find((write) =>
        write.path === `/api/v1/rooms/${ROOM_B}/messages`
        && ((write.body as { text?: string } | null)?.text ?? '').includes(MARKER))
      expect(markerWrite, 'instrument: the sentinel send reached room B after the checked Send').toBeDefined()
      // Issue requirement: NO send anywhere in the recorded log — before,
      // up to and including the sentinel, or after the full flush — may carry
      // room A's draft: not its text, not its picked mention id, not its
      // staged media ref, at room B's endpoint (the target room checked) or
      // anywhere else. With a clean box the checked Send is disabled and
      // nothing at all is posted.
      const draftWrites = net.writes().filter((write) => {
        const body = write.body as { text?: string; mentions?: string[]; media?: string[] } | null
        return write.path.endsWith('/messages')
          && ((body?.text ?? '').includes(DRAFT_TEXT.trim())
            || body?.mentions?.includes(MENTIONED_MEMBER_ID) === true
            || body?.media?.includes(MEDIA_REF) === true)
      })
      expect(draftWrites, '1849: no send may carry room A\'s draft (text, mention id, media ref — every endpoint checked, not only room B\'s)').toEqual([])
    } finally {
      vi.useRealTimers()
    }
  })
})
