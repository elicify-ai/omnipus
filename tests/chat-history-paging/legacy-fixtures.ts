// Test-only observations and mock ports for the D15 array -> envelope migration.
// The real adapter is never wrapped/coerced into the new shape. Missing or old
// output fails at the assertion, while test-owned mocks use the agreed seam J.
import { expect } from 'vitest'
import type { Message as DisplayMessage, Session as DisplaySession } from '@/lib/api/sessions'
import type { Message as WireMessage, SessionDetail, SessionHistoryResponse, operations } from '@/lib/api/generated/openapi-types'
import { page } from './fixtures'
import { fixtureHistoryPosition, wireHistoryPage } from './legacy-wire'

// not-wire-format: normalized SPA observation, derived from generated metadata.
export type HistoryPageView = Omit<SessionHistoryResponse, 'entries'> & {
  entries: (DisplayMessage & { historyPosition: NonNullable<WireMessage['history_position']> })[]
}

// not-wire-format: available normalized detail observation, not a wire clone.
export type AvailableHistoryDetailView = Omit<SessionDetail, 'session' | 'messages' | 'history_state'>
  & Omit<SessionHistoryResponse, 'session_id' | 'entries'> & {
    session: DisplaySession
    messages: HistoryPageView['entries']
    history_state: SessionDetail['history_state'] & { status: 'available'; reason?: never; message?: never }
  }

// not-wire-format: a test-owned dependency port, not a production declaration.
export type HistoryReadPort = (
  sessionId: string,
  options?: NonNullable<operations['getSessionMessages']['parameters']['query']> & { signal?: AbortSignal },
) => Promise<HistoryPageView>

export function emptyHistoryPage(sessionId: string): HistoryPageView {
  // D3/D10: a genuinely empty terminal archive has no positions or cursors,
  // both continuation flags false, and exactly zero rows/source groups.
  return { ...page([], { sessionId }), entries: [] }
}

export function narrationHistoryPage(sessionId: string, messages: DisplayMessage[]): HistoryPageView {
  if (messages.some((message) => !message.content || message.tool_calls?.length)) {
    throw new Error('narrationHistoryPage requires exactly one nonempty narration row per source; give tool/metadata fixtures explicit row accounting')
  }
  const occurrences = new Map<string, number>()
  const positions = messages.map((message) => {
    const count = (occurrences.get(message.id) ?? 0) + 1
    occurrences.set(message.id, count)
    const supplied: unknown = Reflect.get(message, 'historyPosition')
    return typeof supplied === 'string' ? supplied : fixtureHistoryPosition(sessionId, `${message.id}\0${count}`)
  })
  const metadata = wireHistoryPage(sessionId, messages, messages.length, { positions })
  return { ...metadata, entries: messages.map((message, index) => ({ ...message, historyPosition: positions[index] })) }
}

export function availableHistoryDetail(session: DisplaySession, messages: DisplayMessage[] = []): AvailableHistoryDetailView {
  const { session_id, entries, ...metadata } = narrationHistoryPage(session.id, messages)
  void session_id
  return { session, messages: entries, history_state: { status: 'available' }, ...metadata }
}

export function expectAvailableHistoryDetail(value: unknown): asserts value is AvailableHistoryDetailView {
  expect(value).toHaveProperty('history_state', { status: 'available' })
  expect(value).toHaveProperty('session')
  expect(value).toHaveProperty('messages')
  if (typeof value !== 'object' || value === null) throw new Error('D10: missing available session detail')
  const session: unknown = Reflect.get(value, 'session')
  if (typeof session !== 'object' || session === null) throw new Error('D10: missing normalized session')
  expectHistoryPage({ ...value, session_id: Reflect.get(session, 'id'), entries: Reflect.get(value, 'messages') })
}

// not-wire-format: query-cache test double, preserving unrelated list data.
export function queryDataWithEmptyHistory(queryKey: readonly unknown[] | undefined, otherData: unknown = []): unknown {
  if (queryKey?.[0] !== 'messages') return otherData
  const sessionId = queryKey[1]
  // A disabled/no-session query has no data, not a fabricated available page.
  return typeof sessionId === 'string' && sessionId.length > 0 && sessionId !== '__pending'
    ? emptyHistoryPage(sessionId)
    : undefined
}

export function expectHistoryPage(value: unknown): asserts value is HistoryPageView {
  expect(Array.isArray(value), 'D15: the adapter result is an envelope, not an array or an array-like compatibility result').toBe(false)
  expect(value).toHaveProperty('session_id')
  expect(value).toHaveProperty('page_info')
  expect(value).toHaveProperty('has_earlier')
  expect(value).toHaveProperty('has_later')
  expect(value).toHaveProperty('entries')
  if (typeof value !== 'object' || value === null) throw new Error('D15: missing history envelope')
  const entries: unknown = Reflect.get(value, 'entries')
  expect(Array.isArray(entries), 'D15: entries is the normalized message array, not Array.prototype.entries').toBe(true)
  if (!Array.isArray(entries)) throw new Error('D15: missing history entries')
  for (const entry of entries) {
    expect(entry).toHaveProperty('historyPosition', expect.stringMatching(/^hp1_[A-Za-z0-9_-]{43}$/))
  }
}
