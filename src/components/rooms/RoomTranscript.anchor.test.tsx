/**
 * #1449 S-40 / FR-030; architect N2 / code-reviewer R2-N02: an older-page
 * prepend must keep the reader's CURRENT message and viewport offset.
 * docs/group-chats.md::Opening a group and reading earlier messages promises
 * that scrolling back does not move the message being read.
 *
 * This is an explicitly limited component snapshot, not the 10,005-entry
 * browser scenario. Six 300px rows cover the shared opening fill goal in a
 * 400px viewport, so only the reader's scroll starts the one older-page GET.
 * The terminal page reaches seq 1. A 200px live row lands BELOW the reader.
 *
 * jsdom has no layout. Reuse the normal-chat tests' browser-edge geometry for
 * shared history-anchor wrappers, including the 32px boundary controls. The
 * list, viewport coordinator, room source and store remain real. This plain
 * list test does not certify browser painting or the virtualizer.
 */
import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest'
import { act, screen, fireEvent, waitFor } from '@testing-library/react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { useRoomStore } from '@/store/rooms'
import {
  Network, entry, loadRoomScreen, openRoom, resetRealStores, snapshot, stopHarness, ROOM_ID,
} from '../../../tests/l1-room-ui/harness'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

const fetchRoomHistory = vi.hoisted(() => vi.fn<(roomId: string, opts?: { beforeSeq?: number; limit?: number }) => Promise<RoomHistoryResponse>>())
vi.mock('@/lib/api/rooms', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  fetchRoomHistory,
}))

const displayed = (): RoomEntry[] => Array.from({ length: 6 }, (_, index) => {
  const seq = index + 3
  return entry({ seq, id: `room-message-${seq}`, client_message_id: `c${seq}`, text: `entry-${seq}` })
})

const firstPage = (): RoomHistoryResponse => ({
  entries: [
    entry({ seq: 1, id: 'room-message-1', client_message_id: 'c1', text: 'entry-1' }),
    entry({ seq: 2, id: 'room-message-2', client_message_id: 'c2', text: 'entry-2' }),
  ],
  // RoomHistoryResponse: reaching the first entry is terminal, so the shared
  // fill must not ask for a second page before seq 1.
  has_older: false,
})

const ROW_HEIGHT = 300
const LOG_VIEWPORT = 400
const CONTROL_HEIGHT = 32
const READER_OFFSET = -100
let layout: ReturnType<typeof installHistoryLayout> | undefined
let mounted: Awaited<ReturnType<typeof openRoom>> | undefined

function logElement(): HTMLElement {
  return screen.getByRole('region', { name: 'Conversation history' })
}

/** The shared row's position in the VIEWPORT, exactly what the reader sees. */
function viewportTopOf(text: string): number {
  const row = screen.getByText(text).closest<HTMLElement>('[data-history-anchor]')
  expect(row, `instrument: ${text} has a shared history anchor`).not.toBeNull()
  expect(row!.getBoundingClientRect().height, 'instrument: rows have physical height').toBe(ROW_HEIGHT)
  return row!.getBoundingClientRect().top - logElement().getBoundingClientRect().top
}

async function mountRoom() {
  mounted = await openRoom(new Network(), snapshot({
    seq: 8, entries: displayed(), members: ['mia', 'jim'],
    muted: [], member_states: [], paused: false, generation: 1,
    allocated_cost_micro_usd: 1_000, allocated_cost_unpriced: false,
    complete: true, has_older: true,
  }))
  // S-40 starts from an opened group at newest, not a still-covered list.
  await waitFor(() => expect(screen.queryByTestId('chat-open-cover')).toBeNull())
  expect(logElement().clientHeight, 'instrument: nonzero shared viewport').toBe(LOG_VIEWPORT)
  expect(logElement().scrollTop, 'instrument: six rows plus the earlier-page button, at newest')
    .toBe(6 * ROW_HEIGHT + CONTROL_HEIGHT - LOG_VIEWPORT)
  expect(useRoomStore.getState().rooms[ROOM_ID]?.snapshotReceived).toBe(true)
  expect(useRoomStore.getState().rooms[ROOM_ID]?.hasOlder).toBe(true)
  expect(fetchRoomHistory, 'the fixture already covers the opening fill goal').not.toHaveBeenCalled()
  return mounted
}

function scrollLogToTop(): void {
  const log = logElement()
  // FR-030: a real reader scroll leaves follow-latest and captures a new
  // anchor. Merely changing a JS variable no longer drives the shared list.
  fireEvent.wheel(log, { deltaY: -1 })
  log.scrollTop = 0
  fireEvent.scroll(log)
  expect(log.scrollTop, 'instrument: the reader reached the loaded top').toBe(0)
  expect(useRoomStore.getState().rooms[ROOM_ID]?.viewMode, 'instrument: the real store received reader intent')
    .toBe('historical')
}

function moveReaderDuringGet(): void {
  const log = logElement()
  // Pending GET: one 32px button + one 32px status above the rows. Moving
  // 400px into row content puts entry-4 at -100, independently of product code.
  log.scrollTop = 2 * CONTROL_HEIGHT + ROW_HEIGHT - READER_OFFSET
  fireEvent.scroll(log)
  expect(viewportTopOf('entry-4')).toBe(READER_OFFSET)
}

function deferredPage(): { promise: Promise<RoomHistoryResponse>; resolve: (page: RoomHistoryResponse) => void } {
  let resolve!: (page: RoomHistoryResponse) => void
  const promise = new Promise<RoomHistoryResponse>((res) => { resolve = res })
  return { promise, resolve }
}

// #616: module transformation uses the existing beforeAll hook budget.
beforeAll(async () => { await loadRoomScreen() })
beforeEach(() => {
  vi.clearAllMocks()
  fetchRoomHistory.mockReset()
  // Install before resetRealStores so its missing-scrollTo stand-in does not
  // shadow the shared layout helper's functional browser-edge scrollTo.
  layout = installHistoryLayout()
  layout.state.height = LOG_VIEWPORT
  layout.state.rowHeight = ROW_HEIGHT
  layout.state.controlHeight = CONTROL_HEIGHT
  layout.state.rowHeights.set('room:9', 200)
  resetRealStores()
})

afterEach(() => {
  stopHarness(mounted?.connection)
  mounted = undefined
  layout?.restore()
  layout = undefined
})

describe('N2/R2-N02 — the prepend keeps the reader CURRENT place, not the request-start place', () => {
  it('a live message arriving at the bottom during the GET is NOT counted as prepended height', async () => {
    const { emit } = await mountRoom()
    const pending = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(pending.promise)

    scrollLogToTop()
    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))
    moveReaderDuringGet()

    await emit({
      type: 'room_entry', room_id: ROOM_ID, seq: 9,
      entry: entry({ seq: 9, id: 'room-message-9', client_message_id: 'c9', text: 'entry-9' }),
    })
    expect(screen.getByText('entry-9')).toBeInTheDocument()
    expect(viewportTopOf('entry-4'), 'a bottom append does not move the reader').toBe(READER_OFFSET)

    await act(async () => { pending.resolve(firstPage()) })
    await screen.findByText('entry-1')

    // Final content: two older rows (600) + entry-3 (300), no older-page
    // controls, minus the reader's -100 offset = 1000, never + the live 200.
    expect(logElement().scrollTop).toBe(3 * ROW_HEIGHT - READER_OFFSET)
    expect(viewportTopOf('entry-4')).toBe(READER_OFFSET)
    expect(fetchRoomHistory).toHaveBeenCalledTimes(1)
  })

  it('scrolling during the pending GET is preserved — the request-start position is not restored', async () => {
    await mountRoom()
    const pending = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(pending.promise)

    scrollLogToTop()
    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))
    moveReaderDuringGet()

    await act(async () => { pending.resolve(firstPage()) })
    await screen.findByText('entry-1')

    expect(logElement().scrollTop).toBe(3 * ROW_HEIGHT - READER_OFFSET)
    expect(viewportTopOf('entry-4')).toBe(READER_OFFSET)
    expect(fetchRoomHistory).toHaveBeenCalledTimes(1)
  })

  it('control — no mid-load scroll, no live traffic: only the prepend and removed boundary affect scrollTop', async () => {
    await mountRoom()
    fetchRoomHistory.mockResolvedValueOnce(firstPage())

    scrollLogToTop()
    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))
    await screen.findByText('entry-1')

    // S-40: preserve entry-3 at its original +32 screen position. The terminal
    // page adds 600px and removes the original 32px earlier-history button.
    expect(logElement().scrollTop).toBe(2 * ROW_HEIGHT - CONTROL_HEIGHT)
    expect(viewportTopOf('entry-3')).toBe(CONTROL_HEIGHT)
  })
})
