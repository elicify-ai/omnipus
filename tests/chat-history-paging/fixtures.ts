// Test-only network payloads. Oracles: chat-history-paging-spec D3/D4/D10.
// No production adapter/selector is called to construct expected values.
import { createHash } from 'node:crypto'
import { expect } from 'vitest'
import type {
  Message, Session, SessionDetail, SessionHistoryResponse,
} from '@/lib/api/generated/openapi-types'
import type { ReplayMessageFrame, ServerFrame, SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'
import { SessionDetail as DetailSchema, SessionHistoryResponse as PageSchema } from '@/lib/api/generated/schemas'
import { parseFrameSafe } from '@/lib/ws'

// Generated component exists without a top-level named type export (seam J).
type SessionHistoryPageInfo = SessionHistoryResponse['page_info']

export const SID = 'history-paging-session'
export const STAMP = '2026-10-21T10:00:00Z'
export const UNAVAILABLE = 'This conversation\'s history could not be loaded.'

// Synthetic server-issued selectors: hash only fixture labels, NOT the product's
// D2 encoding. They deliberately have no useful lexical/acceptance-order relation.
export function pos(rank: number): string {
  return `hp1_${createHash('sha256').update(`1266-fixture-${rank}`).digest('base64url')}`
}

export function entry(rank: number, overrides: Partial<Message> = {}): Message {
  return {
    id: `history-${rank}`, type: 'message', role: 'user', agent_id: 'mia',
    content: `Saved row ${rank}`, timestamp: STAMP, history_position: pos(rank), ...overrides,
  }
}

export function session(id = SID): Session {
  return {
    id, type: 'chat', agent_id: 'mia', workspace_id: 'sales', title: 'Paging fixture', status: 'active',
    created_at: STAMP, updated_at: STAMP, channel: 'web', partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
  }
}

// not-wire-format: fixture construction options, never sent to the gateway.
export type PageOptions = {
  sessionId?: string
  earlier?: boolean
  later?: boolean
  rows?: number
  info?: Partial<SessionHistoryPageInfo>
}

export function page(entries: Message[], options: PageOptions = {}): SessionHistoryResponse {
  const first = entries[0]?.history_position
  const last = entries.at(-1)?.history_position
  const result: SessionHistoryResponse = {
    session_id: options.sessionId ?? SID, entries,
    has_earlier: options.earlier ?? false, has_later: options.later ?? false,
    ...(options.earlier && first ? { next_before: first } : {}),
    ...(options.later && last ? { next_after: last } : {}),
    page_info: {
      mode: 'latest', limit: 40, min_rows: 20,
      // Default datasets are one narration row per whole source group. Tool
      // datasets supply their independently counted drawn weight explicitly.
      row_count: options.rows ?? entries.length, source_group_count: entries.length,
      soft_byte_budget: 4194304, byte_ceiling: 8_388_608, projected_bytes: 0, byte_budget_exceeded: false,
      stop_reason: options.earlier ? 'row_target' : 'end', retention_generation: 'fixture-retained-1',
      context: { spans: [], duplicate_ids: [] },
      ...(first ? { first_position: first, last_position: last } : {}),
      ...options.info,
    },
  }
  // These fixtures use narration-only carriers unless a test supplies a
  // precomputed tool-heavy meter. Encode both public carriers independently;
  // diagnostic byte-accounting fields are excluded from their own meter (D4a).
  if (options.info?.projected_bytes === undefined) {
    const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...metadata } = result.page_info
    void projected_bytes; void soft_byte_budget; void byte_budget_exceeded; void budget_override
    const rest = { ...result, page_info: metadata }
    const { session_id, entries: sources, ...snapshot } = rest
    const ws = [{ type: 'session_snapshot', session_id, seq: 100, ...snapshot }, ...sources.map((source) => replay(source, session_id))]
    result.page_info.projected_bytes = Math.max(Buffer.byteLength(JSON.stringify(rest)), Buffer.byteLength(JSON.stringify(ws)))
  }
  return result
}

export function availableDetail(history: SessionHistoryResponse, identity = session(history.session_id)): SessionDetail {
  const { entries, session_id, ...metadata } = history
  void session_id
  return { session: identity, history_state: { status: 'available' }, messages: entries, ...metadata }
}

export function unavailableDetail(identity = session()): SessionDetail {
  return { session: identity, history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE } }
}

export function snapshot(history: SessionHistoryResponse, reason: SessionSnapshotFrame['reason'] = 'unknown_position'): SessionSnapshotFrame {
  const { session_id, entries, ...metadata } = history
  void entries
  return { type: 'session_snapshot', session_id, seq: 100, boot_id: 'history-boot', reason, ...metadata }
}

export function replay(source: Message, sessionId = SID): ReplayMessageFrame {
  if (!source.history_position) throw new Error('Fixture instrument: selected occurrence has no position')
  return {
    type: 'replay_message', session_id: sessionId, id: source.id,
    role: source.role ?? 'assistant', content: source.content ?? '', timestamp: source.timestamp,
    ...(source.agent_id ? { agent_id: source.agent_id } : {}),
    history_origin: { position: source.history_position, entry_id: source.id },
    ...(source.turn_id ? { turn_id: source.turn_id } : {}),
    ...(source.terminal_outcome ? { terminal_outcome: source.terminal_outcome } : {}),
    ...(source.client_message_id ? { client_message_id: source.client_message_id } : {}),
  }
}

// Independent assertion, deliberately separate from the builders and the real
// consumer. Pass hand-computed positions/row weight, never the product's output.
export function expectPageAccounting(
  metadata: Pick<SessionHistoryResponse, 'has_earlier' | 'has_later' | 'next_before' | 'next_after' | 'page_info'>,
  positions: string[], drawnRows: number,
): void {
  const info = metadata.page_info
  expect(info.source_group_count, 'D4/D10 selected whole source count, NOT replay-frame count').toBe(positions.length)
  expect(info.row_count, 'D4 narration plus logical tool rows, each start/result pair counted once').toBe(drawnRows)
  expect(info.first_position, 'D3 first position iff nonempty').toBe(positions[0])
  expect(info.last_position, 'D3 last position iff nonempty').toBe(positions.at(-1))
  expect(metadata.next_before, 'D3 earlier flag iff first-position cursor').toBe(metadata.has_earlier ? positions[0] : undefined)
  expect(metadata.next_after, 'D3 later flag iff last-position cursor').toBe(metadata.has_later ? positions.at(-1) : undefined)
  expect(info.byte_budget_exceeded, 'D4a exact exceeded bit').toBe(info.projected_bytes > info.soft_byte_budget)
  expect(info.budget_override !== undefined, 'D4a override iff exceeded').toBe(info.byte_budget_exceeded)
  expect(info.min_rows, 'D4a advisory floor cannot exceed the row target').toBeLessThanOrEqual(info.limit)
  if (info.stop_reason === 'row_target') expect(info.row_count, 'row_target is not an unexplained short page').toBeGreaterThanOrEqual(info.limit)
  if (info.stop_reason === 'source_cap') expect(info.source_group_count, 'D3 source cap is exactly100').toBe(100)
  if (info.stop_reason === 'byte_budget') expect(info.row_count, 'bytes may not stop below the modeled floor').toBeGreaterThanOrEqual(info.min_rows)
  if (info.stop_reason === 'clear') expect(info.stopped_at_clear, 'clear underfill must identify the reached barrier').toBe(true)
  if (info.stop_reason === 'end' && (info.mode === 'latest' || info.mode === 'older')) {
    expect(metadata.has_earlier, 'true retained end cannot advertise an unexplained predecessor').toBe(false)
  }
}

export function expectValidDetail(detail: SessionDetail, positions: string[], drawnRows: number): void {
  expect(DetailSchema.safeParse(detail).success, 'fixture satisfies generated detail structure').toBe(true)
  if (detail.history_state.status === 'unavailable') {
    expect(Object.keys(detail).sort(), 'D10 metadata-only is not empty history').toStrictEqual(['history_state', 'session'])
    return
  }
  expect(detail.messages, 'D10 available history requires messages').toBeDefined()
  expect(detail.has_earlier, 'D10 available history requires has_earlier').toBeTypeOf('boolean')
  expect(detail.has_later, 'D10 available history requires has_later').toBeTypeOf('boolean')
  expect(detail.page_info, 'D10 available history requires page_info').toBeDefined()
  if (!detail.page_info || detail.has_earlier === undefined || detail.has_later === undefined) {
    throw new Error('Invalid available fixture: missing page metadata (D10)')
  }
  expectPageAccounting({ ...detail, page_info: detail.page_info, has_earlier: detail.has_earlier, has_later: detail.has_later }, positions, drawnRows)
}

export function expectValidPage(history: SessionHistoryResponse, positions: string[], drawnRows: number): void {
  expect(PageSchema.safeParse(history).success, 'fixture satisfies generated page structure').toBe(true)
  expectPageAccounting(history, positions, drawnRows)
}

export function expectValidFrame(frame: ServerFrame): void {
  expect(parseFrameSafe(frame), 'fixture satisfies generated WebSocket structure').not.toBeNull()
}

// Controlled promise barriers; no sleep or polling clock is a causality oracle.
export function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
