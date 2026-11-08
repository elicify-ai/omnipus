// TaskToolUI.historical.test.tsx — issue #1683, round 4 / D1.
//
// Team-lead decision D1 (2026-11-08, on CHECK finding F1): saved and
// replayed WORKSPACE history must render the same task card as live, through
// the REAL history dispatcher —
//   src/components/chat/tools/HistoricalToolCallBlock.tsx::renderHistoricalToolCall
// (reached in the app from ChatScreen.tsx::VirtualAssistantMessageRow) — for
// all four actions, and the card must NOT turn into the generic badge when a
// live message becomes a saved row.
//
// RED today: renderSavedToolCall has no task-tool branch, so every *_task
// call falls through to GenericToolCall (its generic markup is
// data-testid="tool-call-badge") and no task-tool-card renders — the exact
// live→saved disappearance CHECK reproduced.
//
// The dispatcher is imported and called REAL — the card, the Zod trigger
// parse and delegationSlotted all run; nothing in this file is mocked (the
// fixtures carry no workspace_id, so no router Link renders and no router
// context is needed). The persisted history result envelope
// `{ text: "<payload json>" }` is the shape a saved row forwards unchanged.

import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderHistoricalToolCall } from './HistoricalToolCallBlock'

// Same fixture instant as the live/replay halves: Mon 2026-11-09 09:00
// Europe/Berlin = 08:00 UTC; vitest pins the viewer to TZ=UTC/en-US
// (vite.config.ts::test.env).
const MON_0900_BERLIN_MS = Date.UTC(2026, 10, 9, 8, 0, 0)
const RECURRING_TRIGGER = {
  type: 'recurring',
  config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' },
}
const RESULT_CREATE = JSON.stringify({ task_id: 'task_1', status: 'inbox' })
const RESULT_UPDATE = JSON.stringify({ task_id: 'task_1', status: 'inbox', updated_fields: ['trigger'] })
const RESULT_RUN = JSON.stringify({ task_id: 'task_1', status: 'running', session_id: 'sess_1' })
const RESULT_DELETE = JSON.stringify({ deleted: 'task_1' })

const RAW_RULE_RE = /RRULE|FREQ=|BYDAY|BYMONTHDAY|DTSTART|COUNT=|UNTIL=|INTERVAL=|cron/i

type SavedTc = Parameters<typeof renderHistoricalToolCall>[0]['tc']

function renderSavedRow(tool: string, params: Record<string, unknown>, resultJson: string) {
  // A settled, saved row: status success, result under the persisted
  // `{ text }` envelope — exactly what history rendering forwards.
  const tc = {
    id: `tc_${tool}`,
    tool,
    params,
    status: 'success',
    result: { text: resultJson },
  } as unknown as SavedTc
  const node = renderHistoricalToolCall({
    tc,
    callId: `tc_${tool}`,
    inlineEvents: undefined,
    activeSessionId: null,
    verboseChatEnabled: false,
    goalRecordEmpty: false,
    liteMode: false,
  })
  return render(<>{node}</>)
}

describe('D1 — saved workspace history renders the same task card through the real dispatcher', () => {
  it.each([
    ['create_task', { task_id: 'task_1', title: 'Weekly report', trigger: RECURRING_TRIGGER }, RESULT_CREATE],
    ['update_task', { task_id: 'task_1', title: 'Weekly report', trigger: RECURRING_TRIGGER }, RESULT_UPDATE],
    ['run_task', { task_id: 'task_1' }, RESULT_RUN],
    ['delete_task', { task_id: 'task_1' }, RESULT_DELETE],
  ])('a saved %s row renders the task card — never the generic badge', (tool, params, resultJson) => {
    renderSavedRow(tool, params, resultJson)
    // The card is there (same test ids as the live path).
    expect(screen.getByTestId('task-tool-card')).toBeTruthy()
    // …and the live→saved transition did NOT collapse it into the generic
    // badge (GenericToolCall's markup). RED today: this is exactly what
    // happens — the dispatcher has no task branch.
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    expect(screen.queryByTestId('task-tool-schedule-warning')).toBeNull()
  })

  it('a saved recurring create shows the same schedule reading as live', () => {
    renderSavedRow(
      'create_task',
      { task_id: 'task_1', title: 'Weekly report', trigger: RECURRING_TRIGGER },
      RESULT_CREATE,
    )
    const card = screen.getByTestId('task-tool-card')
    const schedule = within(card).getByTestId('task-tool-schedule')
    // Founder example, trigger zone: "every Monday at 09:00 (Berlin time)".
    expect(schedule.textContent ?? '').toMatch(/mon/i)
    expect(schedule.textContent ?? '').toMatch(/0?9[:.]00/)
    expect(card.textContent ?? '').toMatch(/berlin/i)
    expect(card.textContent ?? '').toContain('Weekly report')
    expect(card.textContent ?? '').not.toMatch(RAW_RULE_RE)
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })
})
