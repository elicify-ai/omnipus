/**
 * RoomTranscript.opening.test.tsx — Train 3 gate E2E stubs-1 (job
 * 118494220743, tests/e2e/room-history-paging.spec.ts:57): the before_seq
 * older-page request never fires.
 *
 * Mechanism: a room opened at scrollTop 0 showing the OLDEST loaded message
 * with the "Earlier messages aren't shown" hint already in view, and
 * onScroll is loadOlder's only trigger — so no scroll event ever fires and
 * the older page is unreachable unless the reader scrolls down and back up.
 *
 * Required behaviour (docs/group-chats.md "Scrolling back"):
 *   1. a group opens at the NEWEST message — on first snapshot and when
 *      switching rooms (the E2E walk then produces real scroll events);
 *   2. the older page stays reachable when the loaded window fits without
 *      scrolling (no scrollbar exists): the hint itself is the trigger —
 *      chosen over auto-loading because the design pins "does NOT walk
 *      history automatically on mount" (RoomScreen.history.test.tsx T23
 *      mount guard) and every page load must stay a user action;
 *   3. the 6a1644619 prepend anchor is untouched (RoomTranscript.anchor.test.tsx
 *      keeps pinning that; this file only adds the opening behaviours).
 *
 * jsdom does no layout, so scroll/geometry are modelled on the prototypes
 * keyed by role="log": scrollHeight is the real DOM order's content height,
 * scrollTop assignments are captured. Installed BEFORE mounting, so the
 * open-time jump is observable.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { Network, entry, openRoom, snapshot, stopHarness } from '../../../tests/l1-room-ui/harness'
import { RoomTranscript } from './RoomTranscript'

const fetchRoomHistory = vi.hoisted(() => vi.fn<(roomId: string, opts?: { beforeSeq?: number; limit?: number }) => Promise<RoomHistoryResponse>>())
vi.mock('@/lib/api/rooms', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  fetchRoomHistory,
}))

const HEIGHTS: Record<string, number> = {
  'entry-3': 300, 'entry-4': 300, 'entry-5': 300,
  'beta-1': 250, 'beta-2': 250,
}
const LOG_VIEWPORT = 400

let scrollTopValue = 0
let lastAssignedScrollTop: number | undefined
let originalScrollTop: PropertyDescriptor | undefined
let originalScrollHeight: PropertyDescriptor | undefined
let originalRect: typeof Element.prototype.getBoundingClientRect

function entryHeightOf(el: Element): number {
  return HEIGHTS[el.querySelector('p')?.textContent ?? ''] ?? 0
}

function contentTopOf(el: Element): number {
  let top = 0
  for (let sib = el.previousElementSibling; sib; sib = sib.previousElementSibling) top += entryHeightOf(sib)
  return top
}

function contentHeightOf(log: Element): number {
  // The shared list wraps rows. Height is still the known entries (3 × 300 = 900),
  // not the wrapper. Direct RoomTranscript children stay the fallback.
  const rows = [...log.querySelectorAll('article')].filter((row) => (row.querySelector('p')?.textContent ?? '') in HEIGHTS)
  const measured = rows.length > 0 ? rows : [...log.children]
  return measured.reduce((sum, child) => sum + entryHeightOf(child), 0)
}

function rect(top: number, height: number): DOMRect {
  return { top, height, bottom: top + height, left: 0, right: 0, width: 0, x: 0, y: 0, toJSON: () => ({}) } as unknown as DOMRect
}

let readingLog = false
function isLog(el: Element): boolean {
  // The shared list is a region. RoomTranscript's own log stays a log.
  // getAttribute from the scrollTop getter re-enters; the flag stops that loop.
  if (readingLog) return false
  readingLog = true
  try {
    return el.getAttribute?.('data-testid') === 'virtualized-message-list' || el.getAttribute?.('role') === 'log'
  } finally {
    readingLog = false
  }
}

function installScrollGeometry(): void {
  originalScrollTop = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop')
  Object.defineProperty(Element.prototype, 'scrollTop', {
    configurable: true,
    get(this: Element) { return isLog(this) ? scrollTopValue : (originalScrollTop?.get?.call(this) ?? 0) },
    set(this: Element, value: number) {
      if (!isLog(this)) { originalScrollTop?.set?.call(this, value); return }
      scrollTopValue = value
      lastAssignedScrollTop = value
    },
  })
  originalScrollHeight = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollHeight')
  Object.defineProperty(Element.prototype, 'scrollHeight', {
    configurable: true,
    get(this: Element) { return isLog(this) ? contentHeightOf(this) : (originalScrollHeight?.get?.call(this) ?? 0) },
  })
  originalRect = Element.prototype.getBoundingClientRect
  Element.prototype.getBoundingClientRect = function (this: Element) {
    if (isLog(this)) return rect(0, LOG_VIEWPORT)
    if (this.closest?.('[role="log"], [data-testid="virtualized-message-list"]')) return rect(contentTopOf(this) - scrollTopValue, entryHeightOf(this))
    return originalRect.call(this)
  }
}

function restoreScrollGeometry(): void {
  if (originalScrollTop) Object.defineProperty(Element.prototype, 'scrollTop', originalScrollTop)
  if (originalScrollHeight) Object.defineProperty(Element.prototype, 'scrollHeight', originalScrollHeight)
  Element.prototype.getBoundingClientRect = originalRect
}

let mounted: Awaited<ReturnType<typeof openRoom>> | undefined

async function mountRoom() {
  mounted = await openRoom(new Network(), snapshot({
    entries: [
      entry({ seq: 3, id: 'room-message-3', client_message_id: 'c3', text: 'entry-3' }),
      entry({ seq: 4, id: 'room-message-4', client_message_id: 'c4', text: 'entry-4' }),
      entry({ seq: 5, id: 'room-message-5', client_message_id: 'c5', text: 'entry-5' }),
    ],
    members: ['mia', 'jim'],
    muted: [], member_states: [], paused: false, generation: 1,
    allocated_cost_micro_usd: 1_000, allocated_cost_unpriced: false,
    complete: true, has_older: true,
  }))
  return mounted
}

beforeEach(() => {
  vi.clearAllMocks()
  fetchRoomHistory.mockReset()
  scrollTopValue = 0
  lastAssignedScrollTop = undefined
  installScrollGeometry()
})

afterEach(async () => {
  restoreScrollGeometry()
  stopHarness(mounted?.connection)
  mounted = undefined
})

describe('1 — a group opens at the newest message', () => {
  it('the first snapshot lands the reader at the bottom — and never fetches history to get there', async () => {
    await mountRoom()

    // 3 entries × 300 px = the log's content height: the assignment that
    // lands at the bottom happened when the room's first content rendered.
    expect(lastAssignedScrollTop).toBe(900)
    expect(scrollTopValue).toBe(900)
    // Opening a group never loads its whole history (T23's mount guarantee).
    expect(fetchRoomHistory).not.toHaveBeenCalled()
  })

  it("switching rooms lands at the NEW room's newest message", async () => {
    const view = render(
      <RoomTranscript
        roomId="alpha"
        snapshotReceived
        entries={[
          entry({ seq: 3, id: 'a-3', client_message_id: 'a3', text: 'entry-3' }),
          entry({ seq: 4, id: 'a-4', client_message_id: 'a4', text: 'entry-4' }),
        ]}
        agentName={(id) => id}
      />,
    )
    expect(lastAssignedScrollTop).toBe(600)

    view.rerender(
      <RoomTranscript
        roomId="beta"
        snapshotReceived
        entries={[
          entry({ seq: 1, id: 'b-1', client_message_id: 'b1', room_id: 'beta', text: 'beta-1' }),
          entry({ seq: 2, id: 'b-2', client_message_id: 'b2', room_id: 'beta', text: 'beta-2' }),
        ]}
        agentName={(id) => id}
      />,
    )
    // 2 × 250 px — the beta content height, not alpha's stale offset.
    expect(lastAssignedScrollTop).toBe(500)
  })
})

describe('2 — the older page stays reachable when the window fits without scrolling', () => {
  it("the \"Earlier messages aren't shown\" hint itself loads the previous page", () => {
    const loadOlder = vi.fn()
    render(
      <RoomTranscript
        roomId="alpha"
        snapshotReceived
        entries={[entry({ seq: 3, id: 'a-3', client_message_id: 'a3', text: 'entry-3' })]}
        agentName={(id) => id}
        hasOlder
        history={{ status: 'idle', failed: false, loadOlder }}
      />,
    )

    fireEvent.click(screen.getByText(/Earlier messages aren't shown/))
    expect(loadOlder).toHaveBeenCalledTimes(1)
  })

  it('the hint does nothing while a page is already in flight (one request per room)', () => {
    const loadOlder = vi.fn()
    render(
      <RoomTranscript
        roomId="alpha"
        snapshotReceived
        entries={[entry({ seq: 3, id: 'a-3', client_message_id: 'a3', text: 'entry-3' })]}
        agentName={(id) => id}
        hasOlder
        history={{ status: 'loading', failed: false, loadOlder }}
      />,
    )

    fireEvent.click(screen.getByText(/Earlier messages aren't shown/))
    expect(loadOlder).not.toHaveBeenCalled()
  })
})
