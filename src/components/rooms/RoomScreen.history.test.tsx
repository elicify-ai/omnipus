/**
 * RoomScreen.history.test.tsx — #1332 room history paging, SPA screen half.
 *
 * Oracles: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/ROOM-HISTORY-PAGING-1332-20261018.md
 * rows T23 (integrated scroll-up flow), T24 (recoverable UI error) and the
 * T22 composer clause, rules D6 (user-facing behaviour) and D2 (cursor).
 * Expected values come from the design, never from current code.
 *
 * RED surfaces at 35998d756: no scroll-up wiring in RoomTranscript/RoomScreen,
 * no historyStatus/olderBoundary store surface, no loadOlder action, no
 * loading/error/Retry UI. The store-level half of these rows lives in
 * src/store/rooms.history.test.ts; the reader-offset preservation clause of
 * T23 needs a real browser and is covered by
 * tests/e2e/room-history-paging.spec.ts (CI only).
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
], false)

let connection: ReturnType<typeof import('../../../tests/l1-room-ui/harness').connectSocket>['connection']

function scrollLogToTop(): void {
  const log = screen.getByRole('log', { name: 'Group conversation' })
  Object.defineProperty(log, 'scrollTop', { configurable: true, value: 0 })
  Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 800 })
  Object.defineProperty(log, 'clientHeight', { configurable: true, value: 400 })
  fireEvent.scroll(log)
}

async function mountRoomWithHistoryHint() {
  const mounted = await openRoom(new Network(), snapshot({
    entries: displayed(), members: ['mia', 'jim'],
    muted: [], member_states: [], paused: false, generation: 1,
    allocated_cost_micro_usd: 1_000, allocated_cost_unpriced: false,
    complete: true, has_older: true,
  }))
  connection = mounted.connection
  expect(screen.getByText(/Earlier messages aren't shown/)).toBeInTheDocument()
  return mounted
}

beforeEach(() => {
  vi.clearAllMocks()
  fetchRoomHistory.mockReset()
})

afterEach(async () => {
  stopHarness(connection)
  connection = undefined as unknown as typeof connection
})

describe('T23 — the integrated scroll-up flow (design D6)', () => {
  // Keep-green mount guard: the absence already holds at RED; it pins that
  // GREEN must not introduce an automatic walk to the beginning on mount.
  it('does NOT walk history automatically on mount — attach alone opens the room', async () => {
    await mountRoomWithHistoryHint()
    expect(screen.getByTestId('room-header')).toBeInTheDocument()
    expect(fetchRoomHistory).not.toHaveBeenCalled()
  })

  it('reaching the top fires EXACTLY ONE request, using the oldest entry seq and the default limit — never the live high-water', async () => {
    await mountRoomWithHistoryHint()
    fetchRoomHistory.mockResolvedValueOnce(firstPage())

    scrollLogToTop()

    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))
    // Oldest DISPLAYED entry seq — never the live room high-water (5), never
    // seq - 1 (4): the design's D6 cursor rule.
    expect(fetchRoomHistory).toHaveBeenCalledWith(ROOM_ID, { beforeSeq: 3 })
    // Default limit: the param is omitted entirely (server default 100).
    const call = fetchRoomHistory.mock.calls[0] as unknown as [string, { limit?: number }]
    expect(call[1]).not.toHaveProperty('limit')
    // One in-flight request per room: scrolling again while it resolves
    // issues no second request.
    scrollLogToTop()
    expect(fetchRoomHistory).toHaveBeenCalledTimes(1)
  })

  it('at terminal false the earlier-history hint disappears and further scrolling issues no request', async () => {
    await mountRoomWithHistoryHint()
    fetchRoomHistory.mockResolvedValueOnce(firstPage()) // terminal

    scrollLogToTop()

    await waitFor(() => expect(screen.queryByText(/Earlier messages aren't shown/)).not.toBeInTheDocument())
    // The whole history is now visible, in order.
    for (const text of ['entry-1', 'entry-2', 'entry-3', 'entry-4', 'entry-5']) {
      expect(screen.getByText(text)).toBeInTheDocument()
    }
    scrollLogToTop()
    scrollLogToTop()
    expect(fetchRoomHistory).toHaveBeenCalledTimes(1)
  })
})

describe('T24 — recoverable earlier-history error (design D6)', () => {
  it('a failed GET keeps rows and the hint, shows an inline error with a keyboard-operable Retry, and Retry repeats the SAME boundary', async () => {
    await mountRoomWithHistoryHint()
    fetchRoomHistory.mockRejectedValueOnce(new Error('history offline'))

    scrollLogToTop()

    // Inline, recoverable — never a blank transcript: rows and hint remain.
    expect(screen.getByText('entry-5')).toBeInTheDocument()
    expect(screen.getByText(/Earlier messages aren't shown/)).toBeInTheDocument()
    const retry = await screen.findByRole('button', { name: 'Retry' })
    expect(retry).toBeVisible()

    // Keyboard-operable: focus it and press Enter.
    retry.focus()
    await userEvent.keyboard('{Enter}')

    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(2))
    // Same boundary — the request that failed is the one retried.
    expect(fetchRoomHistory).toHaveBeenNthCalledWith(2, ROOM_ID, { beforeSeq: 3 })

    // A later valid terminal page clears error and hint.
    fetchRoomHistory.mockResolvedValueOnce(firstPage())
    await userEvent.click(retry)
    await waitFor(() => expect(screen.queryByText(/Earlier messages aren't shown/)).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    // Sending stays enabled the whole time — no "Loading the conversation…" gate.
    expect(screen.queryByText('Loading the conversation…')).toBeNull()
  })
})

describe('T22 (composer clause) — history loading never blocks sending', () => {
  it('the composer stays unblocked while an older page is in flight', async () => {
    await mountRoomWithHistoryHint()
    let resolvePage!: (page: RoomHistoryResponse) => void
    fetchRoomHistory.mockReturnValueOnce(new Promise<RoomHistoryResponse>((resolve) => { resolvePage = resolve }))

    scrollLogToTop()
    await waitFor(() => expect(fetchRoomHistory).toHaveBeenCalledTimes(1))

    // The first-snapshot gate opened at the snapshot; history in flight must
    // not re-close it (blockedReasonOf's "Loading the conversation…" state).
    expect(screen.queryByText('Loading the conversation…')).toBeNull()
    act(() => resolvePage(firstPage()))
  })
})
