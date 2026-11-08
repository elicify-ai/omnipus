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
 *  - Case (b) zone rule — C1 B, founder decision 2026-11-07 11:16Z: a ONCE
 *    trigger shows the VIEWER's own local time, the same way the task detail
 *    panel shows it today: src/components/workspaces/taskFormFields.ts:135
 *    (`Runs once — ${new Date(at).toLocaleString()}` — viewer locale, viewer
 *    zone), rendered in the panel at src/components/workspaces/
 *    TaskDetailPanel.tsx:1034. The card must show the same date and clock
 *    time for the same instant, and NOT the trigger-zone time.
 *  - Result payload SHAPES for fixtures only (production truth: pkg/tools/
 *    task.go create/update/delete, pkg/tools/run_task.go) — the assertions
 *    never depend on those payloads beyond using them as inputs.
 *
 * ROUND-4 oracles (team-lead decisions D3–D7 on the CHECK FAIL report;
 * voice rules: /workspace/logs/omnipus-voice-SKILL.md, founder rule
 * 2026-11-08 — in chat the agent speaks as "I", every failure ends with one
 * concrete way forward, plain and specific):
 *  - D3 exact headings per action: create WITH a schedule says
 *    "I scheduled the task"; create manual/without a trigger says
 *    "I created the task" and never claims scheduling; update/run/delete say
 *    "I updated the task" / "I started the task" / "I deleted the task".
 *    An already-running run_task is distinguishable on the wire TODAY:
 *    pkg/tools/run_task.go::TaskRunTool.Execute returns
 *    {"task_id","status","session_id","note":"already running"} for an
 *    in-progress task — its heading is "The task is already running".
 *  - D4 the saved-but-not-active warning text is pinned EXACTLY (live and
 *    replay): "I saved the task, but its schedule isn't active. The saved
 *    schedule is still the one to use. Open Calendar and save it again."
 *  - D5 a bare error (no reason given) says so per action:
 *    "I couldn't create/update/start/delete the task, and no reason was
 *    given. Try again." When the tool returns a reason, the reason is still
 *    shown (the existing refusal assertions pin its presence, not wording).
 *  - D6 the links say "Open tasks" and "Open calendar" (route-truth href
 *    assertions unchanged).
 *  - D7 the failure is rendered with the catalogued error semantics:
 *    role="alert" on the failure region (asserted via getByRole, not a CSS
 *    class).
 *  (D1 — the real history dispatcher — lives in
 *  TaskToolUI.historical.test.tsx; D2 — the end-date zone rule — in
 *  TaskToolUI.zone-anchor.test.tsx.)
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
 * presentation is deliberately not over-constrained. (Round 4 / D7 adds ONE
 * semantic constraint on top: the failure region carries role="alert".)
 *
 * Vitest pins TZ=UTC and locale en-US for every test (vite.config.ts:264,
 * the repo's zone-sensitive-test pin), so the viewer zone is deterministically
 * UTC while the fixtures carry Europe/Berlin. The two cases lean OPPOSITE
 * ways, by founder decision:
 *  - recurring (case (a)): the schedule shows the TRIGGER's zone time — the
 *    founder's example "every Monday at 09:00 (Berlin time)" — so an
 *    implementation that formats in the viewer's zone renders 08:00 and fails.
 *  - once (case (b)): C1 B (founder 2026-11-07 11:16Z) — the VIEWER's own
 *    local time, exactly what the task detail panel shows today — so an
 *    implementation that formats in the TRIGGER's zone renders 09:00 and
 *    fails, and the 09:00 time must not appear at all.
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
// once = config.at_ms. The tz stays in the fixture so the trigger's zone
// (Europe/Berlin) DIFFERS from the pinned viewer zone (UTC, vite.config.ts:
// 264) — under C1 B (founder 2026-11-07 11:16Z) the card must format the
// instant in the VIEWER's zone (08:00), and an implementation that formats in
// the trigger's zone (09:00) fails case (b) below.
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
// run_task's idempotent re-dispatch result: pkg/tools/run_task.go::
// TaskRunTool.Execute returns note "already running" (status in_progress)
// when the task is already in flight — the wire fact D3's second run heading
// is built on.
const RESULT_RUN_ALREADY = JSON.stringify({
  task_id: 'task_1',
  status: 'in_progress',
  session_id: 'sess_1',
  note: 'already running',
})
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

// ── (b) once trigger: explicit date + VIEWER-local time (C1 B) ─────────────
describe('case (b) — create_task with a once trigger (C1 B: viewer-local time)', () => {
  it('shows an explicit date and the VIEWER-local clock time for the instant — never the trigger-zone time, raw epoch ms, or rule text', () => {
    // Use the process-start TZ pin, not a runtime TZ override: see
    // src/lib/calendar/eventMapping.test.ts:289–297 and vite.config.ts:264.
    expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe('UTC')
    renderTaskTool('create_task', ARGS_ONCE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    const schedule = within(card).getByTestId('task-tool-schedule')
    const cardText = card.textContent ?? ''
    // C1 B requires the SAME viewer-local date, not merely the right year.
    // 2026-11-09T08:00Z is 9 November 2026 for the pinned UTC viewer. Accept
    // textual, local numeric, or ISO ordering without inventing display copy
    // (date-part precedent: src/components/ui/date-picker.test.tsx:27–39).
    expect(schedule.textContent ?? '').toMatch(/2026/)
    expect(schedule.textContent ?? '').toMatch(/\b(?:Nov(?:ember)?\s+0?9|0?9\s+Nov(?:ember)?|11\/0?9\/2026|2026-11-09)\b/i)
    // C1 B (founder 2026-11-07 11:16Z): a ONCE trigger shows the VIEWER's own
    // local time, the same way the task detail panel shows it today —
    // taskFormFields.ts:135 `Runs once — ${new Date(at).toLocaleString()}`
    // (viewer locale + viewer zone), rendered in the panel via
    // TaskDetailPanel.tsx:1034. The repo pins the test viewer zone and locale
    // globally (vite.config.ts:264: TZ 'UTC', LC_ALL 'en-US'), so for the
    // fixture instant (09:00 Europe/Berlin = 08:00 UTC) the card must show
    // 08:00. The (^|\D) guard keeps a wrong-zone "18:00" from matching.
    expect(schedule.textContent ?? '').toMatch(/(^|\D)0?8[:.]00/)
    // Kills the round-1 behaviour (trigger-zone formatting): the trigger's
    // zone clock time (09:00) must NOT be what is shown while the zones
    // differ.
    expect(schedule.textContent ?? '').not.toMatch(/(^|\D)0?9[:.]00/)
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
    // Round 4 / D3 (CHECK F3): an unscheduled create must not ANNOUNCE a
    // schedule either — its heading is exactly "I created the task" and the
    // word "schedul" never appears anywhere on the card. RED today: the
    // heading is "Task scheduled" for every create, which both the exact
    // equality and the negative reject.
    expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I created the task')
    expect(cardText).not.toMatch(/schedul/i)
  })
})

// ── (d) schedule_sync_warning: saved but not active ─────────────────────────
describe('case (d) — result carrying schedule_sync_warning', () => {
  it('shows the saved-but-not-active warning and stays a success card', () => {
    renderTaskTool('update_task', ARGS_UPDATE, RESULT_UPDATE_WARN)
    const card = screen.getByTestId('task-tool-card')
    // Round 4 / D4: the warning text is pinned EXACTLY, live and replay, so
    // an opposite-meaning text ("everything is active — no action needed",
    // CHECK's surviving p1 mutant) fails. The wording is the team-lead's
    // decision; the MEANING it protects is r17 §7.11: saved, schedule NOT
    // active, the saved schedule stays the one to use, one concrete remedy.
    const warning = within(card).getByTestId('task-tool-schedule-warning')
    expect((warning.textContent ?? '').trim()).toBe(
      "I saved the task, but its schedule isn't active. The saved schedule is still the one to use. Open Calendar and save it again.",
    )
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
    // Round 4 / D6 (voice rules 8/12: the label names the destination): the
    // first link opens the workspace TASKS PANEL (it does not select a task),
    // the second the workspace calendar. RED today: "Open task" /
    // "Show in Calendar". The route-truth href assertions above are unchanged.
    expect((within(card).getByTestId('task-tool-link-open').textContent ?? '').trim()).toBe('Open tasks')
    expect((within(card).getByTestId('task-tool-link-calendar').textContent ?? '').trim()).toBe('Open calendar')
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
    // Round 4 / D7: the failure carries the catalogued error semantics —
    // role="alert" on the failure region — and the refusal is what it says.
    // RED today: the failure is a bare styled <p> with no role.
    expect(screen.getByRole('alert').textContent ?? '').toContain('Nothing was created')
    // …and nothing presents the call as a success: no schedule line, no
    // readable verb.
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByTestId('task-tool-verb')).toBeNull()
    // Round 5 / D8 REPLACES the round-4 assertion that stood here
    // (`queryByTestId('task-tool-link-open')` is null): team-lead decision D8
    // (2026-11-08) says a call that came back with a KNOWN reason — this
    // refusal text is one — keeps the "Open tasks" link as the way forward.
    // The link is expected because ARGS_CREATE carries workspace_id. The
    // no-reason failure (the D5 rows below) is unchanged and still has no link.
    expect(screen.getByTestId('task-tool-link-open')).toHaveAttribute('href', '/workspaces/ws_1/chat?panel=tasks')
  })

  // Round 4 / D5 + D7: a bare error (no result, no error text) says so IN THE
  // AGENT'S VOICE, per action, and always leaves one way forward (voice
  // rules 1, 7, 8). RED today: one generic "This task action did not
  // succeed." renders for every action. The old create-only absence
  // assertions are kept inside the it.each (nothing deleted — strengthened).
  it.each([
    ['create_task', ARGS_CREATE, "I couldn't create the task, and no reason was given. Try again."],
    ['update_task', ARGS_UPDATE, "I couldn't update the task, and no reason was given. Try again."],
    ['run_task', ARGS_RUN, "I couldn't start the task, and no reason was given. Try again."],
    ['delete_task', ARGS_DELETE, "I couldn't delete the task, and no reason was given. Try again."],
  ])('a bare %s error names the action and the missing reason, with one way forward', (tool, args, expected) => {
    renderTaskTool(tool, args, undefined, {
      isError: true,
      status: { type: 'incomplete', reason: 'error' },
    })
    // D7: the failure region is the catalogued error presentation (role).
    const alert = screen.getByRole('alert')
    // D5: the exact per-action fallback — an action-swapped or generic
    // string fails.
    expect((alert.textContent ?? '').trim()).toBe(expected)
    // …and nothing presents the call as a success: no schedule line, no
    // links, no readable verb.
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByTestId('task-tool-link-open')).toBeNull()
    expect(screen.queryByTestId('task-tool-verb')).toBeNull()
  })
})

// ── Round 5 / D8 — a failed call with a KNOWN reason ────────────────────────
//
// Team-lead decisions D5 (reaffirmed) and D8 (2026-11-08), the oracle for
// every row here:
//  - D5: "I couldn't <verb> the task, and no reason was given. Try again."
//    appears ONLY when no reason exists anywhere. A reason counts when it is a
//    string result, a {text} result, the call's error field, or the `reason`
//    of a generated structured tool failure (delegation_denied).
//  - D8: with a known reason the card shows "I couldn't <verb> the task.
//    <reason>" — verb create / update / start / delete, reason the received
//    text UNCHANGED — keeps the "Open tasks" link, and the alert stays
//    role="alert".
// This file calls the CAPTURED render function with {args, result, status,
// isError}; the end-to-end carriers (REST history, the live adapter) are
// pinned in TaskToolUI.rest-history.test.tsx and TaskToolUI.live-failure.
// test.tsx. Each reason below has a lower-case start, an id, quotes or a
// trailing full stop, so a card that capitalises, trims, hides or rewrites it
// fails. The link is expected where the call's args carry workspace_id (the
// card builds the link from it); real run_task / delete_task args carry only
// task_id, so their rows assert the sentence alone.
describe('Round 5 / D8 — a failed call with a known reason', () => {
  const NO_REASON_RE = /no reason was given/i
  const SUCCESS_HEADING_RE = /^(I (scheduled|created|updated|started|deleted) the task|The task is already running)$/
  const WORKSPACE_LINK = '/workspaces/ws_1/chat?panel=tasks'

  function expectKnownReasonFailure(verb: string, reason: string, opts: { link: boolean }): void {
    const sentence = screen.getByText(`I couldn't ${verb} the task. ${reason}`)
    expect(sentence.closest('[role="alert"]'), 'the sentence sits in the catalogued error region (D7/D8)').not.toBeNull()
    if (opts.link) {
      const open = screen.getByTestId('task-tool-link-open')
      expect((open.textContent ?? '').trim()).toBe('Open tasks')
      expect(open).toHaveAttribute('href', WORKSPACE_LINK)
    }
    // D5: a call that has a reason never claims it has none.
    expect(screen.queryByText(NO_REASON_RE)).toBeNull()
    // Never a success card, never a success heading.
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByText(SUCCESS_HEADING_RE)).toBeNull()
  }

  const KNOWN_REASON_ROWS: Array<{ tool: string; verb: string; args: unknown; reason: string; link: boolean }> = [
    { tool: 'create_task', verb: 'create', args: ARGS_CREATE, reason: 'agent "agent_zz" is not a member of this workspace.', link: true },
    { tool: 'update_task', verb: 'update', args: ARGS_UPDATE, reason: 'task not found: task_99', link: true },
    {
      tool: 'run_task',
      verb: 'start',
      args: ARGS_RUN,
      reason: 'task "task_99" is blocked on an unmet dependency; it becomes runnable automatically once its blockers complete',
      link: false,
    },
    { tool: 'delete_task', verb: 'delete', args: ARGS_DELETE, reason: 'Task task_99 was already deleted', link: false },
  ]

  it.each(KNOWN_REASON_ROWS)(
    '$tool: a string result that is a reason is shown after "I couldn\'t $verb the task."',
    ({ tool, verb, args, reason, link }) => {
      renderTaskTool(tool, args, reason, { isError: true })
      expectKnownReasonFailure(verb, reason, { link })
    },
  )

  it.each(KNOWN_REASON_ROWS)(
    '5a, $tool: a reason carried only in a {text} result gets the same D8 framing',
    ({ tool, verb, args, reason, link }) => {
      renderTaskTool(tool, args, { text: reason }, { isError: true })
      expectKnownReasonFailure(verb, reason, { link })
    },
  )

  it('5a: a {text}-only reason also frames when the part is merely incomplete (status error, no isError)', () => {
    renderTaskTool('run_task', ARGS_RUN, { text: 'task "task_99" not found' }, {
      status: { type: 'incomplete', reason: 'error' },
    })
    expectKnownReasonFailure('start', 'task "task_99" not found', { link: false })
  })

  it('D5: the reason field of a structured delegation_denied failure counts as a reason', () => {
    // Shaped by the generated DelegationFailure contract (asyncapi.yaml) and
    // pkg/tools/result.go::DelegationDeniedResult — not by the card's code.
    const denial = {
      error: 'delegation_denied',
      reason: 'Scout is not in your trust set, so you cannot start its task.',
      policy: 'trust_set',
      tool: 'run_task',
      target_agent_id: 'scout',
    }
    renderTaskTool('run_task', { ...ARGS_RUN, workspace_id: 'ws_1' }, denial, { isError: true })
    expectKnownReasonFailure('start', denial.reason, { link: true })
  })

  it('D5 control: with no reason anywhere the no-reason sentence is still shown (the checks above can tell the two apart)', () => {
    renderTaskTool('update_task', ARGS_UPDATE, undefined, { isError: true })
    const alert = screen.getByRole('alert')
    expect((alert.textContent ?? '').trim()).toBe("I couldn't update the task, and no reason was given. Try again.")
    expect(screen.queryByText(/^I couldn't update the task\. /)).toBeNull()
  })

  it('D5 control: a {text} result that is only whitespace is not a reason — the no-reason sentence is still shown', () => {
    renderTaskTool('update_task', ARGS_UPDATE, { text: '   ' }, { isError: true })
    const alert = screen.getByRole('alert')
    expect((alert.textContent ?? '').trim()).toBe("I couldn't update the task, and no reason was given. Try again.")
    expect(screen.queryByText(/^I couldn't update the task\. /)).toBeNull()
  })
})

// ── verbs: each tool renders its own readable verb ──────────────────────────
describe('readable verb per task tool', () => {
  // Round 4 / D3: the heading is pinned EXACTLY per action, in the agent's
  // voice (voice rule 1) — swapping any two actions' headings, or falling
  // back to the round-1 neutral copy, fails. RED today: every heading is the
  // old neutral map entry (e.g. "Task scheduled").
  it.each([
    ['create_task', ARGS_CREATE, RESULT_CREATE, 'I scheduled the task'],
    ['update_task', ARGS_UPDATE, RESULT_UPDATE, 'I updated the task'],
    ['run_task', ARGS_RUN, RESULT_RUN, 'I started the task'],
    ['delete_task', ARGS_DELETE, RESULT_DELETE, 'I deleted the task'],
  ])('%s renders exactly its own heading', (tool, args, result, expected) => {
    renderTaskTool(tool, args, result)
    const card = screen.getByTestId('task-tool-card')
    const verb = within(card).getByTestId('task-tool-verb')
    expect((verb.textContent ?? '').trim()).toBe(expected)
  })

  it("create_task's verb announces scheduling for a SCHEDULED create (founder example; D3 exact form above)", () => {
    renderTaskTool('create_task', ARGS_CREATE, RESULT_CREATE)
    const card = screen.getByTestId('task-tool-card')
    const verb = within(card).getByTestId('task-tool-verb')
    expect((verb.textContent ?? '')).toMatch(/schedul/i)
  })

  it('run_task whose result says "already running" does not claim a new run was started (D3)', () => {
    // The wire distinguishes the cases TODAY: pkg/tools/run_task.go::
    // TaskRunTool.Execute returns {"task_id","status","session_id",
    // "note":"already running"} for an in-progress task (its idempotent
    // re-dispatch), and a plain payload for a new run. The card must not
    // imply a new run in that case (CHECK voice inventory, run_task row).
    // RED today: the heading is "Task run started" for both results.
    renderTaskTool('run_task', ARGS_RUN, RESULT_RUN_ALREADY)
    const card = screen.getByTestId('task-tool-card')
    expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe(
      'The task is already running',
    )
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
