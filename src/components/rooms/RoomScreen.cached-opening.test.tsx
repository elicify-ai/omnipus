/**
 * #1449 S-42 / FR-030; F-a / R3-N03: this opening's attach snapshot, not
 * stale cached rows, owns the initial placement at newest.
 *
 * Oracle: docs/group-chats.md::Opening a group and reading earlier messages;
 * D11 and docs/using-omnipus-ui.md::Reading earlier messages require the same
 * latest-follow / paused-reader behavior as normal chat. Real screen, stores,
 * router, HTTP client and WS parser; only browser transport/layout are doubled.
 * Reuse the ChatScreen tests' shared-history geometry: three 300px rows in a
 * 400px viewport end at 500; five rows end at 1100. This exercises the real
 * plain list, not browser painting or virtualizer certification.
 */
import { useState, type ComponentType } from 'react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { Button } from '@/components/ui/button'
import { roomsQueryKeys } from '@/lib/api/rooms'
import { queryClient } from '@/lib/queryClient'
import { useConnectionStore } from '@/store/connection'
import { useRoomStore } from '@/store/rooms'
import {
  connectSocket, entry, json, loadRoomScreen, mountAt, Network, resetRealStores, room,
  ROOM_ID, snapshot, stopHarness, WS, type RoomScreenProps,
} from '../../../tests/l1-room-ui/harness'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

const ROW_HEIGHT = 300
const VIEWPORT_HEIGHT = 400
let RoomScreen: ComponentType<RoomScreenProps>
let connection: ReturnType<typeof connectSocket>['connection'] | undefined
let net: Network
let layout: ReturnType<typeof installHistoryLayout> | undefined
let assignments: number[]

function message(seq: number) {
  return entry({ seq, id: `message-${seq}`, client_message_id: `client-${seq}`, text: `entry-${seq}` })
}

function SwitchableRoom({ initialRoomId }: { initialRoomId: string }) {
  const [selected, setSelected] = useState(initialRoomId)
  return (
    <>
      <Button onClick={() => setSelected(ROOM_ID)}>Open cached group</Button>
      <Button onClick={() => setSelected('other')}>Open other group</Button>
      <RoomScreen workspaceId={WS} roomId={selected} />
    </>
  )
}

async function mountCachedRoom(initialRoomId = ROOM_ID) {
  const cached = snapshot({ seq: 5, entries: [message(3), message(4), message(5)] })
  act(() => useRoomStore.getState().applyRoomFrame(cached))
  // The old room WAS ready. Caching this REST result also makes the stale
  // transcript render before RoomScreen's passive attach effect can reset it.
  expect(useRoomStore.getState().rooms[ROOM_ID]?.snapshotReceived).toBe(true)
  queryClient.setQueryData(roomsQueryKeys.detail(ROOM_ID), net.currentRoom)
  const other = room({ id: 'other', name: 'Other' })
  queryClient.setQueryData(roomsQueryKeys.detail(other.id), other)
  net.respond('GET', '/api/v1/rooms/other', () => json(other))

  const socket = connectSocket()
  connection = socket.connection
  await mountAt(<SwitchableRoom initialRoomId={initialRoomId} />)
  await waitFor(() => expect(socket.socket.sent).toContainEqual({
    type: 'attach_room', room_id: initialRoomId, after_seq: initialRoomId === ROOM_ID ? 5 : 0,
  }))
  return socket
}

function logElement(): HTMLElement {
  return screen.getByRole('region', { name: 'Conversation history' })
}

function visibleBottomOf(text: string): number {
  const row = screen.getByText(text).closest<HTMLElement>('[data-history-anchor]')
  expect(row, `instrument: ${text} belongs to the shared history list`).not.toBeNull()
  expect(row!.getBoundingClientRect().height, 'instrument: nonzero row height').toBe(ROW_HEIGHT)
  expect(logElement().clientHeight, 'instrument: nonzero viewport height').toBe(VIEWPORT_HEIGHT)
  return row!.getBoundingClientRect().bottom - logElement().getBoundingClientRect().top
}

// #616: import/transform setup uses the existing 60-second hook budget.
beforeAll(async () => { RoomScreen = await loadRoomScreen() })
beforeEach(() => {
  // Install before resetRealStores to supply functional scrollTo, rather than
  // its missing-browser-method stand-in. No viewport or paging code is mocked.
  layout = installHistoryLayout()
  layout.state.height = VIEWPORT_HEIGHT
  layout.state.rowHeight = ROW_HEIGHT
  resetRealStores()
  net = new Network()
  assignments = []
  const setTop = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTop')?.set
  if (!setTop) throw new Error('instrument: shared history layout must install a scrollTop setter')
  vi.spyOn(HTMLElement.prototype, 'scrollTop', 'set').mockImplementation(function (this: HTMLElement, value: number) {
    setTop.call(this, value)
    if (this.getAttribute('aria-label') === 'Conversation history') assignments.push(this.scrollTop)
  })
})

afterEach(() => {
  stopHarness(connection)
  connection = undefined
  vi.restoreAllMocks()
  layout?.restore()
  layout = undefined
})

describe('F-a — cached opening waits for this attach snapshot', () => {
  it('does not spend the opening jump on cached rows carrying the previous ready flag', async () => {
    await mountCachedRoom()

    expect(screen.getByText('entry-5')).toBeInTheDocument()
    expect(useRoomStore.getState().rooms[ROOM_ID]?.snapshotReceived).toBe(false)
    expect(assignments).toEqual([])
  })

  it('lands on the newest catch-up row even when the reserved complete flag is false', async () => {
    const socket = await mountCachedRoom()
    await socket.emit(snapshot({ seq: 7, entries: [message(6), message(7)], complete: false }))

    expect(useRoomStore.getState().rooms[ROOM_ID]?.entries.map((row) => row.seq)).toEqual([3, 4, 5, 6, 7])
    expect(logElement().scrollTop).toBe(1100)
    expect(visibleBottomOf('entry-7')).toBe(VIEWPORT_HEIGHT)
  })

  it('the readiness change alone settles the opening when catch-up has no new rows', async () => {
    const socket = await mountCachedRoom()
    logElement().scrollTop = 75
    await socket.emit(snapshot({ seq: 5, entries: [] }))

    expect(logElement().scrollTop).toBe(500)
    expect(visibleBottomOf('entry-5')).toBe(VIEWPORT_HEIGHT)
  })

  it('switching to a cached room also waits for its own delayed catch-up', async () => {
    const socket = await mountCachedRoom('other')
    fireEvent.click(screen.getByRole('button', { name: 'Open cached group' }))
    await waitFor(() => expect(socket.socket.sent).toContainEqual({
      type: 'attach_room', room_id: ROOM_ID, after_seq: 5,
    }))
    await socket.emit(snapshot({ seq: 7, entries: [message(6), message(7)] }))

    expect(logElement().scrollTop).toBe(1100)
    expect(visibleBottomOf('entry-7')).toBe(VIEWPORT_HEIGHT)
  })

  it('switching away and back while disconnected still waits for the reconnect catch-up', async () => {
    const socket = await mountCachedRoom()
    await socket.emit(snapshot({ seq: 5, entries: [] }))
    act(() => useConnectionStore.getState().setConnected(false))
    fireEvent.click(screen.getByRole('button', { name: 'Open other group' }))
    fireEvent.click(screen.getByRole('button', { name: 'Open cached group' }))
    act(() => useConnectionStore.getState().setConnected(true))
    await socket.emit(snapshot({ seq: 7, entries: [message(6), message(7)] }))

    expect(logElement().scrollTop).toBe(1100)
    expect(visibleBottomOf('entry-7')).toBe(VIEWPORT_HEIGHT)
  })

  it('an empty opening becomes ready, then ordinary live rows follow latest as in normal chat', async () => {
    queryClient.setQueryData(roomsQueryKeys.detail(ROOM_ID), net.currentRoom)
    const socket = connectSocket()
    connection = socket.connection
    await mountAt(<RoomScreen workspaceId={WS} roomId={ROOM_ID} />)
    await socket.emit(snapshot({ entries: [] }))
    expect(logElement().scrollTop).toBe(0)
    await waitFor(() => expect(screen.queryByTestId('chat-open-cover')).toBeNull())

    // #1449 D11 / FR-030; using-omnipus-ui.md::Reading earlier messages:
    // once ready at latest, ordinary live messages are followed. The old
    // room-only no-follow expectation is not the shared chat's behavior.
    act(() => {
      for (const seq of [3, 4, 5]) {
        useRoomStore.getState().applyRoomFrame({ type: 'room_entry', room_id: ROOM_ID, seq, entry: message(seq) })
      }
    })
    expect(screen.getByText('entry-5')).toBeInTheDocument()
    expect(logElement().scrollTop).toBe(3 * ROW_HEIGHT - VIEWPORT_HEIGHT)
    expect(visibleBottomOf('entry-5')).toBe(VIEWPORT_HEIGHT)
    expect(screen.queryByTestId('chat-open-cover')).toBeNull()
  })

  it('control: ordinary live traffic and a later reattach do not pull a settled reader to the tail', async () => {
    const socket = await mountCachedRoom()
    await socket.emit(snapshot({ seq: 7, entries: [message(6), message(7)] }))
    const log = logElement()
    // D11: actual reader movement pauses following. Assigning scrollTop
    // without a scroll event never tells the shared viewport the reader moved.
    fireEvent.wheel(log, { deltaY: -1 })
    log.scrollTop = 100
    fireEvent.scroll(log)
    expect(useRoomStore.getState().rooms[ROOM_ID]?.viewMode).toBe('historical')
    await socket.emit({ type: 'room_entry', room_id: ROOM_ID, seq: 8, entry: message(8) })
    expect(logElement().scrollTop).toBe(100)
    expect(visibleBottomOf('entry-3')).toBe(ROW_HEIGHT - 100)

    act(() => { useRoomStore.getState().attachRoom(ROOM_ID) })
    await socket.emit(snapshot({ seq: 9, entries: [message(9)] }))
    expect(screen.getByText('entry-9')).toBeInTheDocument()
    expect(logElement().scrollTop).toBe(100)
    expect(visibleBottomOf('entry-3')).toBe(ROW_HEIGHT - 100)
  })
})
