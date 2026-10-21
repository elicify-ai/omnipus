// Normalized SPA transform types. All wire members derive from generated contracts.
import type { Message, Session } from './sessions'
import type { Message as WireMessage, SessionDetail as WireDetail, SessionHistoryResponse, operations } from './generated/openapi-types'

export type HistoryPosition = NonNullable<WireMessage['history_position']>
export type SessionHistoryPageInfo = SessionHistoryResponse['page_info']
export type SessionHistoryMetadata = Omit<SessionHistoryResponse, 'session_id' | 'entries'>
export type SessionHistoryEntry = Message & { historyPosition: HistoryPosition }
export type SessionHistoryPage = Omit<SessionHistoryResponse, 'entries'> & { entries: SessionHistoryEntry[] }
export type SessionHistoryQuery = NonNullable<operations['getSessionMessages']['parameters']['query']>
export type SessionHistoryReadOptions = SessionHistoryQuery & { signal?: AbortSignal }
export type SessionDetailReadOptions = { signal?: AbortSignal }
type WireHistoryState = WireDetail['history_state']
type DetailBase = Omit<WireDetail, 'session' | 'messages' | 'history_state' | keyof SessionHistoryMetadata> & { session: Session }
export type AvailableSessionDetail = DetailBase & SessionHistoryMetadata & {
  history_state: WireHistoryState & { status: 'available'; reason?: never; message?: never }
  messages: SessionHistoryEntry[]
}
export type UnavailableSessionDetail = DetailBase & {
  history_state: WireHistoryState & { status: 'unavailable' } & Required<Pick<WireHistoryState, 'reason' | 'message'>>
} & { [K in keyof SessionHistoryMetadata | 'messages']?: never }
export type SessionDetail = AvailableSessionDetail | UnavailableSessionDetail

