// Test data only — seam K, not another history implementation/store.
// The RED base does not yet contain the GREEN-owned history.ts export. This
// fixture transcribes its agreed empty/unknown state until that module lands.
import type { Message as DisplayMessage } from '@/lib/api/sessions'
import type { Message as WireMessage, SessionDetail, SessionHistoryResponse, operations } from '@/lib/api/generated/openapi-types'

type Position = NonNullable<WireMessage['history_position']>
type Query = NonNullable<operations['getSessionMessages']['parameters']['query']>

// not-wire-format: canonical internal fixture state from seams-decision.md::K.
export type FixtureSessionHistoryState = {
  viewGeneration: number
  viewMode: 'latest' | 'historical'
  serverState: SessionDetail['history_state'] | null
  historyLoaded: boolean
  liveControlsReady: boolean
  pageMetadata: Omit<SessionHistoryResponse, 'session_id' | 'entries'> | null
  readerRange: Required<Pick<Query, 'window_start' | 'window_end'>> | null
  readerPositions: Position[]
  tailEntries: (DisplayMessage & { historyPosition: Position })[]
  openedClearPositions: Position[]
  hasEarlier: boolean | null
  hasLater: boolean | null
  olderBoundary: Position | null
  newerBoundary: Position | null
  requests: Record<'older' | 'newer' | 'latest' | 'refresh', {
    status: 'idle' | 'loading' | 'error'
    error: string | null
    attempt: number
  }>
}

export function emptyHistoryStateFixture(): FixtureSessionHistoryState {
  return {
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
  }
}

// Spreading a test fragment works on both the old RED bucket interface and the
// required GREEN history field without a cast or making production optional.
export function emptyHistoryFixture() {
  return { history: emptyHistoryStateFixture() }
}
