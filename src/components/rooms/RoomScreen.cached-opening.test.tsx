/**
 * F-a / R3-N03: this opening's attach snapshot, not stale cached rows, owns
 * the initial jump to newest (dispatch and group-chats.md::Scrolling back).
 * Real screen, stores, router, HTTP client and WS parser; only browser
 * transport/scroll geometry are doubled. Three rows of 300 px in a 400 px
 * viewport end at 500; five rows end at 1100. jsdom has no native anchoring.
 */
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { Button } from '@/components/ui/button'
import { roomsQueryKeys } from '@/lib/api/rooms'
import { queryClient } from '@/lib/queryClient'
import { useConnectionStore } from '@/store/connection'
import { useRoomStore } from '@/store/rooms'
import {
  connectSocket, entry, json, mountAt, Network, resetRealStores, room,
  ROOM_ID, snapshot, stopHarness, WS,
} from '../../../tests/l1-room-ui/harness'
import { RoomScreen } from './RoomScreen'

const ROW_HEIGHT = 300
const VIEWPORT_HEIGHT = 400
let connection: ReturnType<typeof connectSocket>['connection'] | undefined
let net: Network
let positions: WeakMap<Element, number>
let assignments: number[]

function logHeight(log: Element): number {
  return log.querySelectorAll('article').length * ROW_HEIGHT
}

let readingLog = false
function isLog(element: Element): boolean {
  // getAttribute from a scrollTop getter re-enters itself. The flag stops that loop.
  if (readingLog) return false
  readingLog = true
  try {
    return element.getAttribute('data-testid') === 'virtualized-message-list'
  } finally {
    readingLog = false
  }
}

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
  const article = screen.getByText(text).closest('article')
  const index = [...logElement().querySelectorAll('article')].indexOf(article!)
  expect(index).toBeGreaterThanOrEqual(0)
  return (index + 1) * ROW_HEIGHT - logElement().scrollTop
}

beforeEach(() => {
  resetRealStores()
  useRoomStore.setState(useRoomStore.getInitialState(), true)
  net = new Network()
  positions = new WeakMap()
  assignments = []
  const nativeTop = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop')!
  const nativeHeight = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollHeight')!
  vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(function (this: Element) {
    return isLog(this) ? logHeight(this) : nativeHeight.get!.call(this)
  })
  vi.spyOn(Element.prototype, 'scrollTop', 'get').mockImplementation(function (this: Element) {
    return isLog(this) ? positions.get(this) ?? 0 : nativeTop.get!.call(this)
  })
  vi.spyOn(Element.prototype, 'scrollTop', 'set').mockImplementation(function (this: Element, value: number) {
    if (!isLog(this)) { nativeTop.set!.call(this, value); return }
    const top = Math.max(0, Math.min(value, Math.max(0, logHeight(this) - VIEWPORT_HEIGHT)))
    positions.set(this, top)
    assignments.push(top)
  })
})

afterEach(() => {
  stopHarness(connection)
  connection = undefined
  vi.restoreAllMocks()
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

  it('an empty opening snapshot settles before later ordinary live rows arrive', async () => {
    queryClient.setQueryData(roomsQueryKeys.detail(ROOM_ID), net.currentRoom)
    const socket = connectSocket()
    connection = socket.connection
    await mountAt(<RoomScreen workspaceId={WS} roomId={ROOM_ID} />)
    await socket.emit(snapshot({ entries: [] }))
    expect(logElement().scrollTop).toBe(0)

    // A burst of ordinary live rows is not the opening snapshot. Settling
    // even an empty snapshot prevents a delayed jump on this later burst.
    act(() => {
      for (const seq of [3, 4, 5]) {
        useRoomStore.getState().applyRoomFrame({ type: 'room_entry', room_id: ROOM_ID, seq, entry: message(seq) })
      }
    })
    expect(screen.getByText('entry-5')).toBeInTheDocument()
    expect(logElement().scrollTop).toBe(0)
  })

  it('control: ordinary live traffic and a later reattach do not pull a settled reader to the tail', async () => {
    const socket = await mountCachedRoom()
    await socket.emit(snapshot({ seq: 7, entries: [message(6), message(7)] }))
    logElement().scrollTop = 100
    await socket.emit({ type: 'room_entry', room_id: ROOM_ID, seq: 8, entry: message(8) })
    expect(logElement().scrollTop).toBe(100)

    act(() => { useRoomStore.getState().attachRoom(ROOM_ID) })
    await socket.emit(snapshot({ seq: 9, entries: [message(9)] }))
    expect(screen.getByText('entry-9')).toBeInTheDocument()
    expect(logElement().scrollTop).toBe(100)
  })
})
