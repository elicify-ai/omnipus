// Test data only; usable by Vitest and Playwright without importing either runner.
// D3/D4/D10: fixed, small retained archives become positioned page envelopes.
// This is NOT the product position encoder, selector, projector or byte meter.
import { createHash } from 'node:crypto'
import type { SessionDetail, SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import type { ServerFrame, SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'

// not-wire-format: deliberately malformed per-item test input can remain unknown;
// the envelope/metadata still derive from the generated contract, not a clone.
export type FixtureHistoryPage = Omit<SessionHistoryResponse, 'entries'> & { entries: unknown[] }

// not-wire-format: construction controls for test data, not request parameters.
export type LegacyPageOptions = {
  positions?: string[]
  info?: Partial<SessionHistoryResponse['page_info']>
  earlier?: boolean
  later?: boolean
}

export function fixtureHistoryPosition(sessionId: string, sourceLabel: string): string {
  return `hp1_${createHash('sha256').update(`legacy-test-source\0${sessionId}\0${sourceLabel}`).digest('base64url')}`
}

export function clearHistoryPage(sessionId: string, sources: readonly unknown[], drawnRows: number): FixtureHistoryPage {
  const marker = sources[0]
  if (!object(marker) || typeof marker.id !== 'string') {
    throw new Error('Invalid clear-history fixture: the reached marker must be the first positioned source')
  }
  const markerPosition = typeof marker.history_position === 'string'
    ? marker.history_position
    : fixtureHistoryPosition(sessionId, `${marker.id}\0${1}`)
  // Include the marker metadata before the fixture's advertised bytes are set.
  return wireHistoryPage(sessionId, sources, drawnRows, {
    earlier: true, info: {
      stop_reason: 'clear', stopped_at_clear: true,
      latest_clear: { entry_id: marker.id, history_position: markerPosition },
    },
  })
}

// not-wire-format: malformed-session/item cases deliberately keep unknown input;
// the metadata is still the generated detail/page metadata, never a wire clone.
export function wireAvailableDetail(session: unknown, history: FixtureHistoryPage): Omit<SessionDetail, 'session' | 'messages'> & { session: unknown; messages: unknown[] } {
  const { session_id, entries, ...metadata } = history
  void session_id
  return { session, history_state: { status: 'available' }, messages: entries, ...metadata }
}

function object(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function wireHistoryPage(
  sessionId: string,
  sources: readonly unknown[],
  drawnRows: number,
  options: LegacyPageOptions = {},
): FixtureHistoryPage {
  if (!sessionId || sources.length > 100 || !Number.isInteger(drawnRows) || drawnRows < 0) {
    throw new Error('Invalid history fixture: require session, <=100 source groups and an explicit nonnegative logical-row count')
  }
  if (options.positions && options.positions.length !== sources.length) {
    throw new Error('Invalid history fixture: one supplied position per source occurrence is required')
  }
  const occurrences = new Map<string, number>()
  const lastIndexes = new Map<string, number>()
  const selectedPositions: string[] = []
  const entries = sources.map((source, index) => {
    if (!object(source)) {
      throw new Error('Invalid history fixture: a null/primitive item cannot carry history_position; construct that page-level negative explicitly')
    }
    const label = typeof source.id === 'string' && source.id ? source.id : `malformed-source-${index}`
    const occurrence = (occurrences.get(label) ?? 0) + 1
    occurrences.set(label, occurrence)
    lastIndexes.set(label, index)
    const position = options.positions?.[index]
      ?? (typeof source.history_position === 'string' ? source.history_position : fixtureHistoryPosition(sessionId, `${label}\0${occurrence}`))
    selectedPositions.push(position)
    return { ...source, history_position: position }
  })
  const duplicateIds = [...occurrences].filter(([, count]) => count > 1).map(([id, count]) => ({
    kind: 'message' as const, id, occurrence_count: count,
    canonical_position: selectedPositions[lastIndexes.get(id)!],
  }))
  const first = selectedPositions[0]
  const last = selectedPositions.at(-1)
  const earlier = options.earlier ?? false
  const later = options.later ?? false
  const result: FixtureHistoryPage = {
    session_id: sessionId, entries, has_earlier: earlier, has_later: later,
    ...(earlier && first ? { next_before: first } : {}),
    ...(later && last ? { next_after: last } : {}),
    page_info: {
      mode: 'latest', limit: 40, min_rows: 20, row_count: drawnRows,
      source_group_count: sources.length, soft_byte_budget: 4194304, byte_ceiling: 8_388_608,
      projected_bytes: 0, byte_budget_exceeded: false,
      stop_reason: earlier ? 'row_target' : 'end',
      retention_generation: 'legacy-fixture-retained-1',
      context: { spans: [], duplicate_ids: duplicateIds },
      ...(first ? { first_position: first, last_position: last } : {}),
      ...options.info,
    },
  }
  if (options.info?.projected_bytes === undefined) {
    // The mock advertises its encoded public fixture payload; diagnostic fields
    // do not meter themselves. Real gateway max-carrier metering is tested by BE,
    // not claimed by these small frontend contract/identity fixtures.
    const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...info } = result.page_info
    void projected_bytes; void soft_byte_budget; void byte_budget_exceeded; void budget_override
    result.page_info.projected_bytes = Buffer.byteLength(JSON.stringify({ ...result, page_info: info }))
  }
  return result
}

// not-wire-format: test construction options derived from the generated frame.
type SnapshotFixtureOptions = Pick<SessionSnapshotFrame, 'seq' | 'boot_id' | 'reason' | 'secret_requests'>

/** D3/D4/D10: metadata describes the selected sources, never live/control frames. */
export function wireHistorySnapshot(
  history: FixtureHistoryPage,
  options: SnapshotFixtureOptions,
  sourceFrames: readonly ServerFrame[] = [],
): SessionSnapshotFrame {
  const { session_id, entries, ...metadata } = history
  const positions = entries.map((source) => {
    if (!object(source) || typeof source.history_position !== 'string') {
      throw new Error('Invalid snapshot fixture: every selected source needs its recorded history_position')
    }
    return source.history_position
  })
  const framePositions = sourceFrames.map((frame) => {
    if (!('history_origin' in frame) || !frame.history_origin) {
      throw new Error('Invalid snapshot fixture: selected source frames require history_origin')
    }
    if (!('session_id' in frame) || frame.session_id !== session_id) {
      throw new Error('Invalid snapshot fixture: source carrier belongs to a different session')
    }
    return frame.history_origin.position
  })
  const uniqueFrames = [...new Set(framePositions)]
  if (new Set(positions).size !== positions.length || history.page_info.source_group_count !== positions.length
    || uniqueFrames.length !== positions.length || positions.some((position, index) => uniqueFrames[index] !== position)) {
    throw new Error('Invalid snapshot fixture: source groups and origin-bearing carriers must agree in occurrence order')
  }
  if (history.page_info.mode !== 'latest') throw new Error('Invalid snapshot fixture: snapshot must describe a latest page')
  if (positions.length === 0 && (history.page_info.row_count !== 0 || history.has_earlier || history.has_later
    || history.next_before !== undefined || history.next_after !== undefined
    || history.page_info.first_position !== undefined || history.page_info.last_position !== undefined)) {
    throw new Error('Invalid snapshot fixture: empty terminal selection has zero rows and no continuations or positions')
  }
  const frame: SessionSnapshotFrame = { type: 'session_snapshot', session_id, ...metadata, ...options }
  // Independently meter compact public JSON payloads, excluding accounting
  // fields themselves and control/live projection. WS frames are separate
  // payloads: no invented JSON-array brackets or transport headers are charged.
  const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...info } = frame.page_info
  void projected_bytes; void soft_byte_budget; void byte_budget_exceeded; void budget_override
  const restBytes = Buffer.byteLength(JSON.stringify({ ...history, page_info: info }))
  const { secret_requests, ...historicalFrame } = frame
  void secret_requests // Separately governed current secret-request projection, not history.
  const wsBytes = Buffer.byteLength(JSON.stringify({ ...historicalFrame, page_info: info }))
    + sourceFrames.reduce((sum, carrier) => sum + Buffer.byteLength(JSON.stringify(carrier)), 0)
  const bytes = Math.max(restBytes, wsBytes)
  if ((bytes > frame.page_info.soft_byte_budget) !== frame.page_info.byte_budget_exceeded) {
    throw new Error('Invalid snapshot fixture: actual public carriers contradict the declared budget override')
  }
  frame.page_info = { ...frame.page_info, projected_bytes: bytes }
  return frame
}
