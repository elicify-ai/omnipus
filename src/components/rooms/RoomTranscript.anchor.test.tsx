/**
 * RoomTranscript.anchor.test.tsx — architect N2 / code-reviewer R2-N02 (Train 3
 * gate round 2): the older-page prepend must keep the reader's CURRENT place.
 *
 * Oracle: ROOM-HISTORY-PAGING-1332-20261018.md D6/T23 ("the currently visible
 * message/offset stays put") as promised to users by docs/group-chats.md
 * "Scrolling back" — "The room keeps your place while older messages are added
 * above it". Two failure modes are pinned, both present pre-fix where the
 * anchor used the position captured when the request STARTED and compensated
 * with the whole scroll-height delta:
 *   1. scrolling during the pending GET is undone (request-start position
 *      restored), and
 *   2. a live message appended at the BOTTOM during the pending GET is counted
 *      as prepended height, pushing the reader down by that height.
 *
 * jsdom does no layout, so the test installs a real-DOM-order geometry model:
 * an entry's content top is the sum of its preceding siblings' heights, and
 * getBoundingClientRect maps content space to viewport space through the live
 * scrollTop — the same arithmetic a browser performs. The anchor assertion is
 * the reader's message viewport position (not just "attached").
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, screen, fireEvent, waitFor } from '@testing-library/react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { Network, entry, openRoom, snapshot, stopHarness, ROOM_ID } from '../../../tests/l1-room-ui/harness'

const fetchRoomHistory = vi.hoisted(() => vi.fn<(roomId: string, opts?: { beforeSeq?: number; limit?: number }) => Promise<RoomHistoryResponse>>())
vi.mock('@/lib/api/rooms', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  fetchRoomHistory,
}))

const displayed = (): RoomEntry[] => [
  entry({ seq: 3, id: 'room-message-3', client_message_id: 'c3', text: 'entry-3' }),
  entry({ seq: 4, id: 'room-message-4', client_message_id: 'c4', text: 'entry-4' }),
  entry({ seq: 5, id: 'room-message-5', client_message_id: 'c5', text: 'entry-5' }),
]

const olderPage = (entries: RoomEntry[], hasOlder: boolean): RoomHistoryResponse => ({
  entries, has_older: hasOlder,
  ...(hasOlder ? { next_before_seq: entries[0]?.seq } : {}),
})

const firstPage = () => olderPage([
  entry({ seq: 1, id: 'room-message-1', client_message_id: 'c1', text: 'entry-1' }),
  entry({ seq: 2, id: 'room-message-2', client_message_id: 'c2', text: 'entry-2' }),
], true)

// Layout model: heights per entry TEXT (stable across the fix), the log
// viewport, and the live scroll position the "browser" reports.
const HEIGHTS: Record<string, number> = {
  'entry-1': 300, 'entry-2': 300, 'entry-3': 300, 'entry-4': 300, 'entry-5': 300,
  'entry-6': 200, // the tall live message that lands during the pending GET
}
const LOG_VIEWPORT = 400

let logEl: HTMLElement | null = null
let scrollTopValue = 0
let lastAssignedScrollTop: number | undefined
let originalGetBoundingClientRect: typeof Element.prototype.getBoundingClientRect

function entryHeightOf(el: Element): number {
  return HEIGHTS[el.querySelector('p')?.textContent ?? ''] ?? 0
}

function contentTopOf(el: Element): number {
  let top = 0
  for (let sib = el.previousElementSibling; sib; sib = sib.previousElementSibling) top += entryHeightOf(sib)
  return top
}

function rect(top: number, height: number): DOMRect {
  return { top, height, bottom: top + height, left: 0, right: 0, width: 0, x: 0, y: 0, toJSON: () => ({}) } as unknown as DOMRect
}

/** The message's position in the VIEWPORT, exactly what the reader sees. */
function viewportTopOf(text: string): number {
  const article = screen.getByText(text).closest('article')
  expect(article).not.toBeNull()
  return article!.getBoundingClientRect().top
}

function installLogGeometry(log: HTMLElement): void {
  logEl = log
  Object.defineProperty(log, 'scrollTop', {
    configurable: true,
    get: () => scrollTopValue,
    set: (value: number) => { scrollTopValue = value; lastAssignedScrollTop = value },
  })
  Object.defineProperty(log, 'scrollHeight', {
    configurable: true,
    get: () => [...log.children].reduce((sum, child) => sum + entryHeightOf(child), 0),
  })
}

let mounted: Awaited<ReturnType<typeof openRoom>> | undefined

async function mountRoom() {
  mounted = await openRoom(new Network(), snapshot({
    entries: displayed(), members: ['mia', 'jim'],
    muted: [], member_states: [], paused: false, generation: 1,
    allocated_cost_micro_usd: 1_000, allocated_cost_unpriced: false,
    complete: true, has_older: true,
  }))
  installLogGeometry(screen.getByRole('region', { name: 'Conversation history' }))
  return mounted
}

function scrollLogToTop(): void {
  scrollTopValue = 0
  fireEvent.scroll(logEl!)
}

function deferredPage(): { promise: Promise<RoomHistoryResponse>; resolve: (page: RoomHistoryResponse) => void } {
  let resolve!: (page: RoomHistoryResponse) => void
  const promise = new Promise<RoomHistoryResponse>((res) => { resolve = res })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  fetchRoomHistory.mockReset()
  logEl = null
  scrollTopValue = 0
  lastAssignedScrollTop = undefined
  originalGetBoundingClientRect = Element.prototype.getBoundingClientRect
  Element.prototype.getBoundingClientRect = function (this: Element) {
    if (this === logEl) return rect(0, LOG_VIEWPORT)
    if (logEl && logEl.contains(this)) return rect(contentTopOf(this) - scrollTopValue, entryHeightOf(this))
    return originalGetBoundingClientRect.call(this)
  }
})

afterEach(async () => {
  Element.prototype.getBoundingClientRect = originalGetBoundingClientRect
  stopHarness(mounted?.connection)
  mounted = undefined
})

describe('N2/R2-N02 — the prepend keeps the reader CURRENT place, not the request-start place', () => {
  it('a live message arriving at the bottom during the GET is NOT counted as prepended height', async () => {
    const { emit } = await mountRoom()
    const pending = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(pending.promise)

    scrollLogToTop()
    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))

    // While the GET is pending the reader scrolls 400 px down into the loaded
    // messages: they are now reading entry-4 at viewport top −100.
    scrollTopValue = 400
    expect(viewportTopOf('entry-4')).toBe(-100)

    // A live answer (200 px tall) is appended BELOW the reader mid-flight.
    await emit({
      type: 'room_entry', room_id: ROOM_ID, seq: 6,
      entry: entry({ seq: 6, id: 'room-message-6', client_message_id: 'c6', text: 'entry-6' }),
    })
    expect(screen.getByText('entry-6')).toBeInTheDocument()

    // The older page (600 px) lands. Only the height added ABOVE the reader's
    // entry may be compensated: scrollTop 400 + 600 = 1000, never the live 200.
    await act(async () => {
      pending.resolve(firstPage())
      await new Promise((resolve) => setTimeout(resolve, 0))
    })

    expect(lastAssignedScrollTop).toBe(1000)
    // The message the reader was on sits at the SAME viewport position.
    expect(viewportTopOf('entry-4')).toBe(-100)
  })

  it('scrolling during the pending GET is preserved — the request-start position is not restored', async () => {
    await mountRoom()
    const pending = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(pending.promise)

    scrollLogToTop()
    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))

    scrollTopValue = 400
    expect(viewportTopOf('entry-4')).toBe(-100)

    await act(async () => {
      pending.resolve(firstPage())
      await new Promise((resolve) => setTimeout(resolve, 0))
    })

    expect(lastAssignedScrollTop).toBe(1000)
    expect(viewportTopOf('entry-4')).toBe(-100)
  })

  it('control — no mid-load scroll, no live traffic: the page still shifts the reader by exactly the prepended height', async () => {
    await mountRoom()
    fetchRoomHistory.mockResolvedValueOnce(firstPage())

    scrollLogToTop()
    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))

    await waitFor(() => expect(screen.getByText('entry-1')).toBeInTheDocument())
    expect(lastAssignedScrollTop).toBe(600)
  })
})
