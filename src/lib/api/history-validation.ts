// Semantic validation supplements (never replaces) generated structural schemas.
import { SessionHistoryResponse as PageSchema, Message as MessageSchema, HistoryEntryOrigin } from './generated/schemas'
import type { Message as WireMessage, SessionHistoryResponse } from './generated/openapi-types'
import { ApiSchemaError, _recordApiSchemaError } from './http'

export function historyProtocolError(endpoint: string, path: (string | number)[], raw: unknown): never {
  _recordApiSchemaError(endpoint, 1)
  throw new ApiSchemaError(endpoint, [{ path, message: 'Inconsistent session history response.' }], raw)
}

export function historyPosition(raw: unknown, endpoint: string, path: (string | number)[]): string {
  const result = HistoryEntryOrigin.safeParse({ position: raw })
  // A 32-byte base64url digest has two unused low bits in its final character.
  if (!result.success || !/[AEIMQUYcgkosw048]$/.test(result.data.position)) historyProtocolError(endpoint, path, raw)
  return result.data.position
}

/** Public logical rows, not transport frames or an outer wrapper plus children. */
export function historyRowWeight(entry: Pick<WireMessage, 'type' | 'role' | 'content' | 'summary' | 'tool_calls'>): number {
  const tools = new Set(entry.tool_calls?.map(call => call.id) ?? []).size
  const narration = !!(entry.content || entry.summary) || tools === 0
  return Number(narration) + tools
}

/** Validate metadata atomically while leaving per-item public corruption to the adapter. */
export function validateHistoryPage(raw: unknown, sessionId: string, endpoint: string): SessionHistoryResponse {
  if (!raw || typeof raw !== 'object' || !('entries' in raw) || !Array.isArray(raw.entries)) historyProtocolError(endpoint, ['entries'], raw)
  const source = raw as Record<string, unknown> & { entries: unknown[] }
  const parsed = PageSchema.safeParse({ ...source, entries: [] })
  if (!parsed.success) {
    _recordApiSchemaError(endpoint, parsed.error.issues.length)
    throw new ApiSchemaError(endpoint, parsed.error.issues, raw)
  }
  const page = { ...parsed.data, entries: source.entries as WireMessage[] }
  if (page.session_id !== sessionId) historyProtocolError(endpoint, ['session_id'], raw)
  const positions = source.entries.map((entry, index) => {
    if (!entry || typeof entry !== 'object') historyProtocolError(endpoint, ['entries', index], raw)
    return historyPosition(Reflect.get(entry, 'history_position'), endpoint, ['entries', index, 'history_position'])
  })
  validateHistoryMetadata(page, positions, endpoint, raw)
  // Invalid public items still have positions, but their row weight cannot be
  // reconstructed reliably. Do not turn item degradation into page rejection.
  const countable = page.entries.every(entry => MessageSchema.safeParse(entry).success)
  if (countable && page.entries.reduce((sum, entry) => sum + historyRowWeight(entry), 0) !== page.page_info.row_count) historyProtocolError(endpoint, ['page_info', 'row_count'], raw)
  return page
}

export function validateHistoryMetadata(page: Omit<SessionHistoryResponse, 'entries' | 'session_id'>, positions: readonly string[], endpoint: string, raw: unknown = page): void {
  const info = page.page_info
  const fail = (field: string): never => historyProtocolError(endpoint, ['page_info', field], raw)
  if (positions.length > 100 || new Set(positions).size !== positions.length || info.source_group_count !== positions.length) fail('source_group_count')
  if (info.first_position !== positions[0]) fail('first_position')
  if (info.last_position !== positions.at(-1)) fail('last_position')
  if (page.next_before !== (page.has_earlier ? positions[0] : undefined) || (page.has_earlier && !positions.length)) historyProtocolError(endpoint, ['next_before'], raw)
  if (page.next_after !== (page.has_later ? positions.at(-1) : undefined) || (page.has_later && !positions.length)) historyProtocolError(endpoint, ['next_after'], raw)
  if (info.min_rows > info.limit) fail('min_rows')
  if (!positions.length && info.row_count !== 0) fail('row_count')
  if (info.byte_budget_exceeded !== (info.projected_bytes > info.soft_byte_budget)) fail('byte_budget_exceeded')
  if (info.byte_budget_exceeded !== (info.budget_override !== undefined)) fail('budget_override')
  if (info.stop_reason === 'byte_budget' && info.row_count < info.min_rows) fail('stop_reason')
  if (info.stop_reason === 'row_target' && info.row_count < info.limit) fail('stop_reason')
  if (info.stop_reason === 'source_cap' && info.source_group_count !== 100) fail('stop_reason')
  if (info.stop_reason === 'clear' && !info.stopped_at_clear) fail('stop_reason')
}
