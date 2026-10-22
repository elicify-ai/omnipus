// Normalized SPA transform types. All wire members derive from generated contracts.
import type { Message, Session } from './sessions'
import type { Message as WireMessage, SessionDetail as WireDetail, SessionHistoryResponse, operations } from './generated/openapi-types'

export type HistoryPosition = NonNullable<WireMessage['history_position']>
export type SessionHistoryPageInfo = SessionHistoryResponse['page_info']
export type SessionHistoryMetadata = Omit<SessionHistoryResponse, 'session_id' | 'entries'>
export type SessionHistoryEntry = Message & { historyPosition: HistoryPosition } // not-wire-format: SPA-normalized Message with a camelCase source position; the actual Message wire schema is generated and validated.
export type SessionHistoryPage = Omit<SessionHistoryResponse, 'entries'> & { entries: SessionHistoryEntry[] }
export type SessionHistoryQuery = NonNullable<operations['getSessionMessages']['parameters']['query']>
export type SessionHistoryReadOptions = SessionHistoryQuery & { signal?: AbortSignal } // not-wire-format: client fetch options over generated query fields; AbortSignal cancels the read and is never serialized into the request.
export type SessionDetailReadOptions = { signal?: AbortSignal } // not-wire-format: browser-only fetch cancellation options; this object is not a gateway request or response payload.
type WireHistoryState = WireDetail['history_state']
type DetailBase = Omit<WireDetail, 'session' | 'messages' | 'history_state' | keyof SessionHistoryMetadata> & { session: Session }
export type AvailableSessionDetail = DetailBase & SessionHistoryMetadata & {
  history_state: WireHistoryState & { status: 'available'; reason?: never; message?: never }
  messages: SessionHistoryEntry[]
}
export type UnavailableSessionDetail = DetailBase & { // not-wire-format: SPA detail view with a normalized Session; narrows generated history_state and excludes page fields only after wire validation.
  history_state: WireHistoryState & { status: 'unavailable' } & Required<Pick<WireHistoryState, 'reason' | 'message'>>
} & { [K in keyof SessionHistoryMetadata | 'messages']?: never }
export type SessionDetail = AvailableSessionDetail | UnavailableSessionDetail
