// Test-data instrument controls, not a product GREEN/CHECK verdict.
// Independent oracles: history paging D3/D4/D10 (whole sources, actual public bytes).
import { describe, expect, it } from 'vitest'
import type { Message, SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import type { ReplayMessageFrame } from '@/lib/api/generated/asyncapi-types'
import { parseFrameSafe } from '@/lib/ws'
import { fixtureHistoryPosition, wireHistoryPage, wireHistorySnapshot } from '../../tests/chat-history-paging/legacy-wire'

const SID = 'snapshot-fixture-session'
const POSITION = fixtureHistoryPosition(SID, 'first-source')
const TEXT = 'Saved fixture narration'
const STAMP = '2026-10-22T08:00:00Z'
const row: Message = { id: 'saved-1', type: 'message', role: 'assistant', agent_id: 'mia', content: TEXT, timestamp: STAMP, history_position: POSITION }
const carrier: ReplayMessageFrame = {
  type: 'replay_message', session_id: SID, id: row.id, role: 'assistant', content: TEXT,
  timestamp: STAMP, agent_id: 'mia', history_origin: { position: POSITION, entry_id: row.id },
}

// D4a diagnostic fields do not meter themselves; controls are not history.
function withoutAccounting(info: SessionHistoryResponse['page_info']) {
  const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...publicInfo } = info
  void projected_bytes; void soft_byte_budget; void byte_budget_exceeded; void budget_override
  return publicInfo
}

describe('generated-valid snapshot fixture accounting', () => {
  it('represents setup as a genuinely empty retained end, not a missing/unknown page', () => {
    const history = wireHistoryPage(SID, [], 0)
    const frame = wireHistorySnapshot(history, { seq: 1, boot_id: 'stage1-fixture', reason: 'unknown_position' })
    expect(parseFrameSafe(frame), 'real generated schema sees the completed snapshot').toStrictEqual(frame)
    expect(frame).toMatchObject({
      type: 'session_snapshot', session_id: SID, seq: 1, boot_id: 'stage1-fixture', reason: 'unknown_position',
      has_earlier: false, has_later: false,
      page_info: { mode: 'latest', limit: 40, min_rows: 20, row_count: 0, source_group_count: 0,
        stop_reason: 'end', byte_budget_exceeded: false, context: { spans: [], duplicate_ids: [] } },
    })
    expect(frame).not.toHaveProperty('next_before')
    expect(frame).not.toHaveProperty('next_after')
    expect(frame.page_info).not.toHaveProperty('first_position')
    expect(frame.page_info).not.toHaveProperty('last_position')
    const info = withoutAccounting(frame.page_info)
    const expectedBytes = Math.max(Buffer.byteLength(JSON.stringify({ ...history, page_info: info })),
      Buffer.byteLength(JSON.stringify({ ...frame, page_info: info })))
    expect(frame.page_info.projected_bytes).toBe(expectedBytes)
  })

  it('counts a real replay source once and meters actual snapshot headers plus its separate payload', () => {
    const history = wireHistoryPage(SID, [row], 1)
    // Fixture-only long boot label makes WS larger; not a product size boundary.
    const frame = wireHistorySnapshot(history, { seq: 42, boot_id: 'b'.repeat(600), reason: 'boot_mismatch' }, [carrier])
    expect(parseFrameSafe(frame)).toStrictEqual(frame)
    expect(frame.page_info).toMatchObject({ row_count: 1, source_group_count: 1, first_position: POSITION, last_position: POSITION })
    const info = withoutAccounting(frame.page_info)
    const restBytes = Buffer.byteLength(JSON.stringify({ ...history, page_info: info }))
    const wsBytes = Buffer.byteLength(JSON.stringify({ ...frame, page_info: info })) + Buffer.byteLength(JSON.stringify(carrier))
    expect(wsBytes).toBeGreaterThan(restBytes)
    expect(frame.page_info.projected_bytes).toBe(wsBytes)
    expect(history.page_info.projected_bytes, 'construction never mutates another fixture owner').not.toBe(frame.page_info.projected_bytes)
  })

  it('rejects a claimed selected source with no actual origin-bearing carrier', () => {
    expect(() => wireHistorySnapshot(wireHistoryPage(SID, [row], 1), { seq: 1 }))
      .toThrow('Invalid snapshot fixture: source groups and origin-bearing carriers must agree in occurrence order')
  })

  it('rejects live/control traffic masquerading as a selected historical source', () => {
    expect(() => wireHistorySnapshot(wireHistoryPage(SID, [row], 1), { seq: 1 }, [
      { type: 'done', session_id: SID },
    ])).toThrow('Invalid snapshot fixture: selected source frames require history_origin')
  })

  it('rejects invented rows on an empty selection instead of hiding inaccurate accounting', () => {
    expect(() => wireHistorySnapshot(wireHistoryPage(SID, [], 1), { seq: 1 }))
      .toThrow('Invalid snapshot fixture: empty terminal selection has zero rows and no continuations or positions')
  })
})
