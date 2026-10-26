// Fixture/type controls for seam J/K; not an implementation GREEN or CHECK.
import { describe, expect, expectTypeOf, it } from 'vitest'
import type { AvailableHistoryDetailView } from '../../tests/chat-history-paging/legacy-fixtures'
import { emptyHistoryFixture, emptyHistoryStateFixture } from '../../tests/chat-history-paging/empty-history'
import { pos } from '../../tests/chat-history-paging/fixtures'

describe('canonical empty history fixture — seams J/K', () => {
  it('starts unknown and not ready, with every canonical field and independent idle actions', () => {
    expect(emptyHistoryStateFixture()).toStrictEqual({
      viewGeneration: 0, viewMode: 'latest', serverState: null,
      historyLoaded: false, liveControlsReady: false,
      pageMetadata: null, readerRange: null, readerPositions: [], tailEntries: [], openedClearPositions: [],
      hasEarlier: null, hasLater: null, olderBoundary: null, newerBoundary: null,
      requests: {
        older: { status: 'idle', error: null, attempt: 0 },
        newer: { status: 'idle', error: null, attempt: 0 },
        latest: { status: 'idle', error: null, attempt: 0 },
        refresh: { status: 'idle', error: null, attempt: 0 },
      },
    })
  })

  it('never shares arrays/request objects between buckets or action kinds', () => {
    const first = emptyHistoryFixture().history
    const second = emptyHistoryFixture().history
    first.readerPositions.push(pos(1))
    first.openedClearPositions.push(pos(2))
    first.requests.older.status = 'loading'
    first.requests.newer.attempt = 1
    expect(second.readerPositions).toStrictEqual([])
    expect(second.openedClearPositions).toStrictEqual([])
    expect(second.requests.older).toStrictEqual({ status: 'idle', error: null, attempt: 0 })
    expect(second.requests.newer).toStrictEqual({ status: 'idle', error: null, attempt: 0 })
    expect(first.requests.older).not.toBe(first.requests.newer)
    expect(first.requests.older).not.toBe(second.requests.older)
    expect(first.tailEntries).not.toBe(second.tailEntries)
  })

  it('the available-detail type cannot carry unavailable reason/message facts', () => {
    expectTypeOf<AvailableHistoryDetailView['history_state']['status']>().toEqualTypeOf<'available'>()
    expectTypeOf<AvailableHistoryDetailView['history_state']['reason']>().toEqualTypeOf<undefined>()
    expectTypeOf<AvailableHistoryDetailView['history_state']['message']>().toEqualTypeOf<undefined>()
  })
})
