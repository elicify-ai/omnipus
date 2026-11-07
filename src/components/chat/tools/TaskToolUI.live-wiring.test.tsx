/**
 * TaskToolUI.live-wiring.test.tsx — issue #1683 / Team B WP-1 (RED).
 *
 * Founder decision 2026-11-07 (T1 A): when an agent creates, changes, runs or
 * deletes a task, the chat shows a READABLE card instead of raw JSON — live
 * AND on history replay. This file is the LIVE half: it captures the
 * `makeAssistantToolUI` registrations for create_task / update_task /
 * run_task / delete_task from `./TaskToolUI` (the BrowserTool.live-wiring
 * precedent — the capture intercepts the native call inside the
 * `@/lib/assistant-tool-status` wrapper too, because the wrapper forwards the
 * config to the mocked `makeNativeToolUI`), then drives the founder's case
 * matrix (plan §4.2 WP-1 rows a–h) through the real AssistantUI render
 * contract `{args, result, status, isError}`.
 *
 * RED route chosen: the IMPORT route. There is no existing surface that
 * renders a task-tool card live — until TaskToolUI.tsx exists, this file
 * fails on the unresolved `./TaskToolUI` import. The replay half
 * (TaskToolUI.replay.test.tsx) rides the existing ToolCallBadge surface and
 * its RED is assertion-level instead (raw JSON rendered). Between the two
 * files every case has a red reason naming the missing feature.
 *
 * ORACLES (never the implementation):
 *  - Founder decision 2026-11-07 (issue #1683): readable card, plain-language
 *    schedule "every Monday at 09:00 (Berlin time)", never raw rule text.
 *  - Plan /workspace/logs/task-screens/plan.md §1.2 S1, §3 WP-1, §4.2 (a)–(h).
 *  - tool-parity-spec-r17.md §7.2 TK-30 (TaskTrigger wire shape), §7.9 (tool
 *    results), §7.10/§7.11 (refusal texts; schedule_sync_warning = saved but
 *    NOT re-armed, trigger stays authoritative). SHA-pinned copy read via
 *    `git show c43b15d:designs-20261029/tool-parity-spec-r17.md` (8e5be393…,
 *    the pin in docs/internal/specs/t8-red-traceability.md).
 *  - Route paths for the links: src/routes/_app/workspaces.$workspaceId.
 *    calendar.tsx (`/workspaces/$workspaceId/calendar`) and the tasks-panel
 *    deep link `/workspaces/$workspaceId/chat?panel=tasks` (the board route
 *    is a redirect stub TO exactly this URL:
 *    workspaces.$workspaceId.board.tsx::WorkspaceBoardRedirect).
 *  - Result payload SHAPES for fixtures only (production truth: pkg/tools/
 *    task.go create/update/delete, pkg/tools/run_task.go) — the assertions
 *    never depend on those payloads beyond using them as inputs.
 *
 * TEST-ID CONTRACT this suite pins (the card's observable interface — the
 * developer implements it; see ta-report.md):
 *   task-tool-card              card root (same on live and replay)
 *   task-tool-verb              the readable action verb line
 *   task-tool-schedule          the plain-language schedule line
 *   task-tool-schedule-warning  the saved-but-not-active warning (r17 §7.11)
 *   task-tool-link-open         "open the task" link
 *   task-tool-link-calendar     "show in calendar" link
 * Case (h) asserts visible refusal TEXT, not a test id — the failure
 * presentation is deliberately not over-constrained.
 *
 * Vitest pins TZ=UTC and locale en-US (vite.config.ts test.env), so an
 * implementation that formats in the VIEWER's zone renders 08:00 for the
 * fixtures below and fails these assertions — the founder's example names the
 * TRIGGER's zone ("09:00 (Berlin time)"), not the viewer's.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'

type TaskRenderFn = (props: {
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => ReactNode

const captured = vi.hoisted((): Record<string, TaskRenderFn | undefined> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as TaskRenderFn
      }
      // Same dummy the dedicatedToolUIRegistration guard returns: the
      // component object itself is never rendered here.
      return () => null
    },
  }
})

// TanStack Router shim: renders Link as a plain <a> whose href is the route
// pattern with $params interpolated and `search` folded in, so the tests can
// assert the navigation TARGET (case (g): "assert the href / navigation
// target from the routes in src/routes") without mounting a full router.
// Real-router navigation behaviour stays e2e/UAT territory, not a unit test.
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const original = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...original,
    Link: (props: {
      to?: unknown
      params?: Record<string, unknown>
      search?: Record<string, unknown>
      children?: ReactNode
    } & Record<string, unknown>) => {
      let href = typeof props.to === 'string' ? props.to : ''
      for (const [k, v] of Object.entries(props.params ?? {})) href = href.replace(`$${k}`, String(v))
      const searchEntries = Object.entries(props.search ?? {}).map(
        ([k, v]) => [k, String(v)] as [string, string],
      )
      const qs = searchEntries.length > 0 ? '?' + new URLSearchParams(searchEntries).toString() : ''
      const { to: _to, params: _params, search: _search, children, ...rest } = props
      return (
        <a href={href + qs} {...rest}>
          {children}
        </a>
      )
    },
  }
})

// Static import — vi.mock intercepts makeAssistantToolUI before this runs, and
// importing the module executes its body, registering the four task tool UIs.
// RED TODAY: this import does not resolve — TaskToolUI.tsx does not exist yet.
import './TaskToolUI'

// ── fixtures ────────────────────────────────────────────────────────────────
// 2026-11-09 is a Monday; Berlin is UTC+1 in November, so 09:00 Berlin is
// 08:00 UTC (Date.UTC is absolute — no host-zone dependency in the fixture).
const MON_0900_BERLIN_MS = Date.UTC(2026, 10, 9, 8, 0, 0)

// r17 §7.2 TK-30 wire shape: recurring = config.rrule + dtstart_ms + tz.
const RECURRING_TRIGGER = {
  type: 'recurring',
  config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' },
}
// once = config.at_ms (tz is wire-legal on the partial config and is the zone
// the brief's case (b) requires the card to name).
const ONCE_TRIGGER = { type: 'once', config: { at_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' } }

const BASE_ARGS = {
  title: 'Weekly report',
  prompt: 'Write the weekly report',
  workspace_id: 'ws_1',
  agent_id: 'agent_mia',
}
const ARGS_CREATE = { ...BASE_ARGS, trigger: RECURRING_TRIGGER }
const ARGS_ONCE = { ...BASE_ARGS, trigger: ONCE_TRIGGER }
const ARGS_UPDATE = { task_id: 'task_1', ...BASE_ARGS, trigger: RECURRING_TRIGGER }
const ARGS_RUN = { task_id: 'task_1' }
const ARGS_DELETE = { task_id: 'task_1' }

// Tool RESULT fixtures — production truth (pkg/tools/task.go, run_task.go),
// used as inputs only. The live path carries the payload JSON as a STRING.
const RESULT_CREATE = JSON.stringify({ task_id: 'task_1', status: 'inbox' })
const RESULT_UPDATE = JSON.stringify({ task_id: 'task_1', status: 'inbox', updated_fields: ['trigger'] })
// Verbatim r17 §7.11 tool text (pkg/tools/task.go schedule_sync_warning): the
// task was saved but its schedule could NOT be re-armed — "saved but not
// active". The card's exact user-facing wording is an open question (report);
// this suite asserts the warning's PRESENCE by test id, per the brief.
const RESULT_UPDATE_WARN = JSON.stringify({
  task_id: 'task_1',
  status: 'inbox',
  updated_fields: ['trigger'],
  schedule_sync_warning:
    'the trigger was saved on the task, but its schedule could not be re-armed (scheduler unreachable); the saved trigger stays authoritative — re-save the schedule or restart the gateway',
})
const RESULT_RUN = JSON.stringify({ task_id: 'task_1', status: 'running', session_id: 'sess_1' })
const RESULT_DELETE = JSON.stringify({ deleted: 'task_1' })

// r17 §7.10 refusal text for a foreign-trigger create — case (h) asserts the
// distinctive tail so the assertion is specific, not "some text rendered".
const FOREIGN_REFUSAL =
  'Tasks for agents in another workspace start only when their team picks them up there: create it without a trigger, or ask the assignee (or a person) to add the schedule after pickup. Nothing was created.'

const COMPLETE = { type: 'complete' } as const

// Raw rule text the founder decision bans from the card (T1 A: "never raw
// RRULE"; plan §1.2 S1 "never raw RRULE"; D8/D9: no raw cron/rule string).
const RAW_RULE_RE = /RRULE|FREQ=|BYDAY|BYMONTHDAY|DTSTART|COUNT=|UNTIL=|INTERVAL=|cron/i

function renderTaskTool(
  tool: string,
  args: unknown,
  result: unknown,
  opts?: { isError?: boolean; status?: { type: string; reason?: string } },
) {
  const renderFn = captured[tool]
  if (!renderFn) throw new Error(`no live registration captured for "${tool}"`)
  return render(
    renderFn({ args, result, status: opts?.status ?? COMPLETE, isError: opts?.isError }) as ReactElement,
  )
}

// ── registrations ───────────────────────────────────────────────────────────
describe('task tool live registrations', () => {
  it.each(['create_task', 'update_task', 'run_task', 'delete_task'])(
    'registers a live render function for "%s"',
    (tool) => {
      expect(captured[tool]).toBeDefined()
    },
  )
})

// ── (a) recurring trigger: plain language, never raw rule ───────────────────
describe('case (a) — create_task with a recurring trigger', () => {
  it('renders the plain-language schedule (weekday, clock time, zone) and never the raw rule text', () => {
    renderTaskTool('create_task', ARGS_CREATE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    const schedule = within(card).getByTestId('task-tool-schedule')
    // Meaning-level assertions: the schedule names the weekday and the
    // schedule's own clock time in the trigger's zone (founder example:
    // "every Monday at 09:00 (Berlin time)"). Formats are deliberately not
    // pinned ("Monday" vs "Mon", "09:00" vs "9:00 AM").
    expect(schedule.textContent ?? '').toMatch(/mon/i)
    expect(schedule.textContent ?? '').toMatch(/0?9[:.]00/)
    const cardText = card.textContent ?? ''
    // The zone is named (founder example). "Berlin" matches both "Berlin
    // time" and a rendered "Europe/Berlin".
    expect(cardText).toMatch(/berlin/i)
    // Never the raw rule (founder T1 A; plan S1 "never raw RRULE").
    expect(cardText).not.toMatch(RAW_RULE_RE)
    // Brief (a): "no JSON braces".
    expect(cardText).not.toContain('{')
    expect(cardText).not.toContain('}')
    // The card is about the task: its title is visible (plan WP-1 "what").
    expect(cardText).toContain('Weekly report')
  })
})

// ── (b) once trigger: explicit date, time and zone ──────────────────────────
describe('case (b) — create_task with a once trigger', () => {
  it('shows an explicit date, the time in the trigger zone, and the zone — never raw epoch ms or rule text', () => {
    renderTaskTool('create_task', ARGS_ONCE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    const schedule = within(card).getByTestId('task-tool-schedule')
    const cardText = card.textContent ?? ''
    // Explicit date: the fixture year appears (date FORMAT is locale-driven
    // and deliberately not pinned).
    expect(schedule.textContent ?? '').toMatch(/2026/)
    // The schedule's own time in the trigger zone — under the pinned TZ=UTC a
    // viewer-zone implementation renders 08:00 and fails this (founder
    // example shows the trigger-zone time).
    expect(schedule.textContent ?? '').toMatch(/0?9[:.]00/)
    expect(cardText).toMatch(/berlin/i)
    // "Explicit" excludes the raw wire forms: no epoch-ms literal, no rule.
    expect(cardText).not.toMatch(/1[0-9]{12}/)
    expect(cardText).not.toMatch(RAW_RULE_RE)
    expect(cardText).not.toContain('{')
  })
})

// ── (c) manual / missing trigger: no schedule line ──────────────────────────
describe('case (c) — manual or missing trigger', () => {
  it.each([
    ['manual trigger', { ...BASE_ARGS, trigger: { type: 'manual', config: {} } }],
    ['no trigger key', { ...BASE_ARGS }],
  ])('renders the card with no schedule line and no invented schedule (%s)', (_name, args) => {
    renderTaskTool('create_task', args, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    expect(within(card).queryByTestId('task-tool-schedule')).toBeNull()
    const cardText = card.textContent ?? ''
    // Nothing schedule-shaped is invented (plan WP-1: the card shows a
    // schedule only when the task carries one).
    expect(cardText).not.toMatch(/(mon|tues|wednes|thurs|fri|satur|sun)day|repeats|runs once|every/i)
    expect(cardText).not.toMatch(RAW_RULE_RE)
  })
})

// ── (d) schedule_sync_warning: saved but not active ─────────────────────────
describe('case (d) — result carrying schedule_sync_warning', () => {
  it('shows the saved-but-not-active warning and stays a success card', () => {
    renderTaskTool('update_task', ARGS_UPDATE, RESULT_UPDATE_WARN)
    const card = screen.getByTestId('task-tool-card')
    // Presence by test id per the brief (d); the exact user-facing wording is
    // an open question (r17 §7.11 fixes the MEANING: saved, NOT re-armed).
    const warning = within(card).getByTestId('task-tool-schedule-warning')
    expect((warning.textContent ?? '').trim()).not.toBe('')
    // The update still SUCCEEDED — the card keeps its readable verb (the
    // warning must not turn the card into a failure presentation).
    expect(within(card).getByTestId('task-tool-verb')).toBeTruthy()
  })
})

// ── (e) malformed trigger: generic chip fallback ────────────────────────────
describe('case (e) — malformed trigger falls back to the generic tool chip', () => {
  // Plan WP-1 contract: parse the trigger defensively with the generated
  // TaskTrigger schema and fall back to the generic chip when parsing fails.
  // These three shapes fail the generated schema (wrong type / bad enum /
  // wrong field type — schemas.ts TaskTrigger Zod).
  it.each([
    ['trigger is a string, not an object', { ...BASE_ARGS, trigger: 'every monday' }],
    ['trigger type outside the wire enum', { ...BASE_ARGS, trigger: { type: 'weekly', config: {} } }],
    ['recurring rrule is a number', { ...BASE_ARGS, trigger: { type: 'recurring', config: { rrule: 42 } } }],
  ])('renders the generic chip, no card, no invented schedule (%s)', (_name, args) => {
    renderTaskTool('create_task', args, RESULT_CREATE)
    // The generic chip is the fallback surface (no crash = render returned).
    expect(screen.getByTestId('tool-call-badge')).toBeTruthy()
    expect(screen.queryByTestId('task-tool-card')).toBeNull()
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
  })

  it('a type-valid but schedule-less recurring trigger invents no concrete schedule', () => {
    // Degenerate but schema-valid ({type:'recurring'} with no rule fields
    // passes the partial config schema): whatever surface renders, it must
    // not claim a concrete time or weekday it cannot know.
    renderTaskTool('create_task', { ...BASE_ARGS, trigger: { type: 'recurring', config: {} } }, RESULT_CREATE)
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    const bodyText = document.body.textContent ?? ''
    expect(bodyText).not.toMatch(/0?[0-9]{1,2}[:.][0-9]{2}/)
    expect(bodyText).not.toMatch(/(mon|tues|wednes|thurs|fri|satur|sun)day|runs once/i)
  })
})

// ── (g) links: route-truth targets, named, keyboard-reachable ───────────────
describe('case (g) — card links to the task and the workspace calendar', () => {
  it('the open-task link targets the tasks-panel route', () => {
    renderTaskTool('create_task', ARGS_CREATE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    const open = within(card).getByTestId('task-tool-link-open')
    // Route truth: /workspaces/$workspaceId/chat?panel=tasks is the tasks
    // surface (the board route redirects to exactly this URL).
    expect(open).toHaveAttribute('href', '/workspaces/ws_1/chat?panel=tasks')
  })

  it('the calendar link targets the workspace calendar route', () => {
    renderTaskTool('create_task', ARGS_CREATE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    const cal = within(card).getByTestId('task-tool-link-calendar')
    // Route truth: workspaces.$workspaceId.calendar.tsx; precedent link in
    // TaskDetailPanel's Trigger field targets the same route.
    expect(cal).toHaveAttribute('href', '/workspaces/ws_1/calendar')
  })

  it('both links have accessible names and stay keyboard-reachable', () => {
    renderTaskTool('create_task', ARGS_CREATE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    for (const id of ['task-tool-link-open', 'task-tool-link-calendar']) {
      const link = within(card).getByTestId(id)
      // Accessible name: a real anchor with non-empty, meaningful text.
      expect(link.tagName).toBe('A')
      const name = (link.textContent ?? '').trim()
      expect(name).not.toBe('')
      expect(name.toLowerCase()).not.toBe('here')
      // Keyboard: anchors are natively activatable — the one thing to rule
      // out is an explicit removal from the tab order.
      expect(link).not.toHaveAttribute('tabindex', '-1')
    }
  })
})

// ── (h) failure and refusal: never a success card ───────────────────────────
describe('case (h) — failed or refused call', () => {
  it('a refused create shows the refusal text and no success-card content', () => {
    // r17 §7.10 foreign-trigger refusal — the tool's refusal sentence is the
    // call's outcome; isError marks it as a failure on the live path.
    renderTaskTool('create_task', ARGS_CREATE, FOREIGN_REFUSAL, { isError: true })
    // The refusal itself is visible (its distinctive spec tail).
    expect(screen.getByText(/Nothing was created/)).toBeTruthy()
    // …and nothing presents the call as a success: no schedule line, no
    // links, no readable verb.
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByTestId('task-tool-link-open')).toBeNull()
    expect(screen.queryByTestId('task-tool-verb')).toBeNull()
  })

  it('a bare error outcome never renders success-card content', () => {
    renderTaskTool('create_task', ARGS_CREATE, undefined, {
      isError: true,
      status: { type: 'incomplete', reason: 'error' },
    })
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByTestId('task-tool-link-open')).toBeNull()
    expect(screen.queryByTestId('task-tool-verb')).toBeNull()
  })
})

// ── verbs: each tool renders its own readable verb ──────────────────────────
describe('readable verb per task tool', () => {
  it.each([
    ['create_task', ARGS_CREATE, RESULT_CREATE],
    ['update_task', ARGS_UPDATE, RESULT_UPDATE],
    ['run_task', ARGS_RUN, RESULT_RUN],
    ['delete_task', ARGS_DELETE, RESULT_DELETE],
  ])('%s renders a readable verb line', (tool, args, result) => {
    renderTaskTool(tool, args, result)
    const card = screen.getByTestId('task-tool-card')
    const verb = within(card).getByTestId('task-tool-verb')
    expect((verb.textContent ?? '').trim()).not.toBe('')
  })

  it("create_task's verb announces scheduling (founder example: \"Task scheduled: …\")", () => {
    renderTaskTool('create_task', ARGS_CREATE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    const verb = within(card).getByTestId('task-tool-verb')
    expect((verb.textContent ?? '')).toMatch(/schedul/i)
  })

  it('each of the four tools renders its OWN verb — the four differ', () => {
    const verbs: string[] = []
    const calls: Array<[string, unknown, unknown]> = [
      ['create_task', ARGS_CREATE, RESULT_CREATE],
      ['update_task', ARGS_UPDATE, RESULT_UPDATE],
      ['run_task', ARGS_RUN, RESULT_RUN],
      ['delete_task', ARGS_DELETE, RESULT_DELETE],
    ]
    for (const [tool, args, result] of calls) {
      const { unmount } = renderTaskTool(tool, args, result)
      const card = screen.getByTestId('task-tool-card')
      verbs.push((within(card).getByTestId('task-tool-verb').textContent ?? '').trim())
      unmount()
    }
    expect(new Set(verbs).size).toBe(4)
  })
})
