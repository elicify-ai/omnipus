// T33/D10/D15 migration instruments, NOT implementation GREEN or self-CHECK.
// Exact fixture oracles: D3 continuation, D4 logical rows, generated wire shape.
import { describe, expect, it } from 'vitest'
import type { Message } from '@/lib/api'
import { SessionDetail as DetailSchema, SessionHistoryResponse as PageSchema } from '@/lib/api/generated/schemas'
import { entry, expectPageAccounting, pos, session, SID, STAMP } from '../../tests/chat-history-paging/fixtures'
import {
  availableHistoryDetail, emptyHistoryPage, expectAvailableHistoryDetail,
  expectHistoryPage, narrationHistoryPage, queryDataWithEmptyHistory,
} from '../../tests/chat-history-paging/legacy-fixtures'
import { clearHistoryPage, wireAvailableDetail, wireHistoryPage } from '../../tests/chat-history-paging/legacy-wire'

const display: Message = { id: 'normalized-source', role: 'user', content: 'Retained narration', timestamp: STAMP, status: 'done' }

describe('T33 legacy fixture instruments — independent valid and invalid controls', () => {
  it('empty is a generated-valid zero-row terminal page with no positions or continuation', () => {
    const value = emptyHistoryPage(SID)
    expect(PageSchema.safeParse(value).success).toBe(true)
    expectPageAccounting(value, [], 0)
    expect(value.entries).toStrictEqual([])
    expect(value.has_earlier).toBe(false)
    expect(value.has_later).toBe(false)
    expect(Object.hasOwn(value, 'next_before')).toBe(false)
    expect(Object.hasOwn(value, 'next_after')).toBe(false)
  })

  it('two complete narration sources have exactly two groups/rows and keep supplied opaque positions', () => {
    const value = wireHistoryPage(SID, [entry(1), entry(2)], 2)
    expect(PageSchema.safeParse(value).success).toBe(true)
    expectPageAccounting(value, [pos(1), pos(2)], 2)
    expect(value.entries).toStrictEqual([entry(1), entry(2)])
  })

  it('one narration plus a finished tool is one source and two logical rows, not three transport frames', () => {
    const source = entry(1, { role: 'assistant', tool_calls: [{ id: 'logical-tool', tool: 'read_file', status: 'success', content_state: 'full', parameters: { path: 'fixture.txt' } }] })
    const value = wireHistoryPage(SID, [source], 2)
    expect(PageSchema.safeParse(value).success).toBe(true)
    expectPageAccounting(value, [pos(1)], 2)
  })

  it('a reached clear explains a short nonterminal page and latest_clear names its actual first source', () => {
    const marker = entry(10, { id: 'clear-marker', type: 'system', role: 'system', content: 'Conversation context cleared' })
    const value = clearHistoryPage(SID, [marker, entry(11)], 2)
    expect(PageSchema.safeParse(value).success).toBe(true)
    expectPageAccounting(value, [pos(10), pos(11)], 2)
    expect(value).toMatchObject({ has_earlier: true, next_before: pos(10), page_info: {
      stop_reason: 'clear', stopped_at_clear: true,
      latest_clear: { entry_id: 'clear-marker', history_position: pos(10) },
    } })
    // Property of this HTTP fixture's advertised meter, not the real gateway:
    // latest_clear is public page data; accounting fields do not meter themselves.
    const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...publicInfo } = value.page_info
    void soft_byte_budget; void byte_budget_exceeded; void budget_override
    expect(projected_bytes).toBe(Buffer.byteLength(JSON.stringify({ ...value, page_info: publicInfo })))
  })

  it('the raw available detail carries generated-valid complete metadata rather than only messages[]', () => {
    const value = wireAvailableDetail(session(), wireHistoryPage(SID, [entry(1)], 1))
    expect(DetailSchema.safeParse(value).success).toBe(true)
    expect(value.history_state).toStrictEqual({ status: 'available' })
    expect(value.messages).toStrictEqual([entry(1)])
    expect(value.page_info?.source_group_count).toBe(1)
  })

  it('the normalized mock port retains the original message fields and adds an occurrence position', () => {
    const positioned = { ...display, historyPosition: pos(1) }
    const value = narrationHistoryPage(SID, [positioned])
    expectHistoryPage(value)
    expect(value.entries).toStrictEqual([{ ...display, historyPosition: pos(1) }])
    expect(value.entries[0].historyPosition).toMatch(/^hp1_[A-Za-z0-9_-]{43}$/)
    expectPageAccounting(value, [pos(1)], 1)
    const detail = availableHistoryDetail({ id: SID, agent_id: 'mia', type: 'chat', title: 'Fixture', created_at: STAMP, updated_at: STAMP, message_count: 1 }, [positioned])
    expectAvailableHistoryDetail(detail)
    expect(detail.messages).toStrictEqual([{ ...display, historyPosition: pos(1) }])
  })

  it('only the messages query gets an envelope; unrelated list arrays retain their identity', () => {
    const list = [{ id: 'agent' }]
    expect(queryDataWithEmptyHistory(['agents'], list)).toBe(list)
    expect(queryDataWithEmptyHistory(['messages', SID])).toStrictEqual(emptyHistoryPage(SID))
    expect(queryDataWithEmptyHistory(['messages', null])).toBeUndefined()
    expect(queryDataWithEmptyHistory(['messages', '__pending'])).toBeUndefined()
  })

  it('an otherwise valid page with a false source-group count fails the accounting instrument', () => {
    const control = wireHistoryPage(SID, [entry(1), entry(2)], 2)
    expectPageAccounting(control, [pos(1), pos(2)], 2)
    const wrong = { ...control, page_info: { ...control.page_info, source_group_count: 0 } }
    expect(() => expectPageAccounting(wrong, [pos(1), pos(2)], 2)).toThrow(/source count/)
  })

  it('an otherwise valid page with a false logical-row count fails the accounting instrument', () => {
    const control = wireHistoryPage(SID, [entry(1), entry(2)], 2)
    expectPageAccounting(control, [pos(1), pos(2)], 2)
    const wrong = { ...control, page_info: { ...control.page_info, row_count: 0 } }
    expect(() => expectPageAccounting(wrong, [pos(1), pos(2)], 2)).toThrow(/logical tool rows/)
  })

  it('a one-row nonterminal row_target fixture is refused instead of counting forty fake rows', () => {
    const wrong = wireHistoryPage(SID, [entry(1)], 1, { earlier: true })
    expect(() => expectPageAccounting(wrong, [pos(1)], 1)).toThrow(/unexplained short page/)
  })

  it.each([null, 7, 'not-an-entry'])('a primitive/null source (%s) cannot be manufactured into positioned success', (bad) => {
    expect(() => wireHistoryPage(SID, [bad], 1)).toThrow(new Error('Invalid history fixture: a null/primitive item cannot carry history_position; construct that page-level negative explicitly'))
  })

  it('the observation guard refuses retired bare arrays rather than adapting real output', () => {
    expect(() => expectHistoryPage([display])).toThrow(/adapter result is an envelope/)
  })

  it('the observation guard refuses a missing normalized occurrence position', () => {
    const control = narrationHistoryPage(SID, [display])
    expectHistoryPage(control)
    const wrong = { ...control, entries: [display] }
    expect(() => expectHistoryPage(wrong)).toThrow(/historyPosition/)
  })
})
