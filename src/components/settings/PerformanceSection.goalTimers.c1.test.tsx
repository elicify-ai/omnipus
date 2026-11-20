/**
 * PerformanceSection.goalTimers.c1.test.tsx — the goal-timer rows of the
 * Limits "Agents and goals" group under the Settings redesign #1519
 * (spec docs/internal/specs/settings-redesign-spec.md, Approved).
 *
 * The old file drove the RETIRED autosave model (700 ms debounce + the
 * per-change "Change the performance settings?" confirm + a refetch after
 * every save). FR-010 replaces all of it: a change is a draft on the
 * section's Save bar (FR-011), Save changes sends one partial PUT carrying
 * only the changed fields and the consent token (FR-015, FR-026, FR-027),
 * and the retired per-change confirm survives only as the FR-016 pre-save
 * confirms, none of which are the goal timers. Replaced assertions cite the
 * FR that replaces them; every behaviour the spec still requires is kept:
 *   - the saved values render (quiet window, check-in switch, interval);
 *   - D-10 rows 6–7 boundaries: quiet 30/3600 and interval 5/1440 accepted,
 *     out-of-range and non-whole input rejected inline with the exact range
 *     message, the interval validated "even while check-ins are off";
 *   - turning check-ins off keeps the interval's value (docs/settings.md
 *     "retains its value"; not replaced by any FR) and is validated while off;
 *   - Discard returns the draft to the saved value (FR-012 — replaces the
 *     cancelled confirmation, FR-010);
 *   - a save whose apply is pending (200 `PerformanceSettings` with
 *     `pending_apply`) is "saved, not in use": the saved value stays shown,
 *     the stale running read never snaps it back (FR-020 outcome 1);
 *   - an off draft never sends the retained interval along (FR-015).
 *
 * Harness: the real component, query client and API layer; only the HTTP
 * process edge is a stub. The one password prompt is the shell's, supplied
 * through SettingsShellContext as a recorder (the section boundary owns
 * "asked once with these labels, answered with the token" — FR-026/FR-027);
 * the reauth wire itself is passwordOncePerSave.test's subject.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PerformanceSection } from './PerformanceSection'

// The Settings shell context ships with the redesign and is absent from the
// pre-change tree — loaded lazily and failing loudly as BLOCKED RED, never
// as a collection error.
type ShellCtx = typeof import('./SettingsShellContext')
let shellCtxModule: ShellCtx | null = null
async function loadShellContext() {
  if (shellCtxModule) return shellCtxModule
  try {
    shellCtxModule = await vi.importActual<ShellCtx>('./SettingsShellContext')
    return shellCtxModule
  } catch {
    throw new Error(
      'BLOCKED RED (FR-010/FR-026/FR-027, D-8): the Settings shell context (SettingsShellContext) does not ' +
        'exist yet — required for the one password prompt per Save and the consent-token write',
    )
  }
}

/** Records the shell's password prompts; each Save's token comes back at once. */
function promptRecorder() {
  const prompts: string[][] = []
  const requestPassword = vi.fn(async (labels: string[]) => {
    prompts.push(labels)
    return 'reauth-tok'
  })
  return { prompts, requestPassword }
}

const savedFields = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
  goal_max_rounds: 20,
  max_tool_iterations: 1000,
}
const timerDefaults = { goal_reminder_quiet_seconds: 60, goal_stalled_checkin_enabled: true, goal_stalled_checkin_minutes: 30 }
const timerKeys = Object.keys(timerDefaults)

let server: Record<string, unknown>
let puts: Array<Record<string, unknown>>
let reads: number
let clients: QueryClient[]
let pendingApplyNext: { stage: string; changed_fields: string[] } | null

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  server = { ...savedFields, ...timerDefaults }
  puts = []
  reads = 0
  clients = []
  pendingApplyNext = null
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=c1-ui-synthetic-cookie')
  vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL, options?: RequestInit) => {
    const path = String(url).split('?')[0]
    if (path === '/api/v1/state') {
      return json({ onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } })
    }
    if (path !== '/api/v1/performance') throw new Error('Unexpected C1 HTTP fixture request')
    if (options?.method !== 'PUT') {
      reads += 1
      return json(server)
    }
    const patch = JSON.parse(String(options.body)) as Record<string, unknown>
    puts.push(patch)
    if (pendingApplyNext) {
      // C-6 write-outcome table: PUT /performance written, apply pending →
      // 200 `PerformanceSettings` with `pending_apply`; the running values
      // the response and every later GET returns stay the OLD ones until the
      // gateway reloads (stage "refresh").
      server = { ...server, pending_apply: pendingApplyNext }
      return json(server)
    }
    server = { ...server, ...patch }
    return json(server)
  }))
})

afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
  clients.push(client)
  const { SettingsShellContext } = await loadShellContext()
  const { requestPassword } = recorder
  const view = render(
    <QueryClientProvider client={client}>
      <SettingsShellContext.Provider
        value={{
          announce: () => {},
          setDraftCount: () => {},
          offerRestart: () => {},
          refreshPendingRestart: () => {},
          requestPassword,
          saveBarVisible: false,
          setSaveBarVisible: () => {},
        }}
      >
        <PerformanceSection />
      </SettingsShellContext.Provider>
    </QueryClientProvider>,
  )
  await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Tries per goal' })).toHaveValue(server.goal_max_rounds as string | number))
  expect(reads).toBeGreaterThan(0)
  return { ...view, client }
}

let recorder: ReturnType<typeof promptRecorder>

/** The timer rows, addressed by the names the spec's own tables give them (C-7, D-10 rows 6–7). */
function timerControls() {
  const quiet = screen.getByRole('spinbutton', { name: 'Idle reminder quiet window' }) as HTMLInputElement
  const minutes = screen.getByRole('spinbutton', { name: 'Check-in interval' }) as HTMLInputElement
  const all = screen.getAllByRole('switch')
  const toolLoading = screen.getByRole('switch', { name: 'Tool loading' })
  const stalled = all.filter((control) => control !== toolLoading)
  expect(stalled, 'the check-in switch beside Tool loading (the group holds no other switch)').toHaveLength(1)
  expect(all).toHaveLength(2)
  return { quiet, minutes, stalled: stalled[0] }
}

/** The row's error: the alert the field's aria-describedby names (FR-013). */
function fieldAlertText(input: HTMLElement): string {
  const ids = (input.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
  const alert = ids.map((id) => document.getElementById(id)).find((el) => el?.getAttribute('role') === 'alert')
  expect(alert, 'the field must show its error as the alert its aria-describedby names').not.toBeNull()
  return alert?.textContent ?? ''
}

/** A typed change is a draft on the bar: counted, password-marked, never sent (FR-010, FR-011). */
async function typeDraft(input: HTMLInputElement, value: string) {
  const count = puts.length
  fireEvent.change(input, { target: { value } })
  expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
  expect(screen.getByText(/1 needs your password/)).toBeInTheDocument()
  expect(puts).toHaveLength(count)
}

/** One Save changes: one prompt naming one row, one partial PUT with the token (FR-015, FR-026, FR-027). */
async function saveDraft(required: Record<string, unknown>, labelMatcher: RegExp, before: Record<string, unknown>) {
  const putCount = puts.length
  const promptCount = recorder.prompts.length
  fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
  await waitFor(() => expect(recorder.prompts.length).toBe(promptCount + 1))
  const labels = recorder.prompts[promptCount]
  expect(labels, 'the prompt lists exactly the password-marked rows of this Save (FR-026)').toHaveLength(1)
  expect(labels[0]).toMatch(labelMatcher)
  await waitFor(() => expect(puts).toHaveLength(putCount + 1))
  const patch = puts[putCount]
  expect(patch).toMatchObject(required)
  // Only the changed fields ride the PUT (FR-015); each keeps the drafted
  // value, never a sibling's saved value. The allowed set is the goal-timer
  // keys plus the budget key this file's flows change.
  const allowedKeys = [...timerKeys, 'goal_max_rounds']
  for (const key of Object.keys(patch)) {
    expect(allowedKeys).toContain(key)
    expect(patch[key]).toEqual({ ...before, ...required }[key])
  }
  const requestInit = lastPutInit()
  expect(requestInit?.headers).toHaveProperty('X-Reauth-Token', 'reauth-tok')
}

let putInits: RequestInit[]
function lastPutInit(): RequestInit | undefined {
  return putInits[putInits.length - 1]
}

beforeEach(() => {
  putInits = []
  recorder = promptRecorder()
  const stub = vi.mocked(globalThis.fetch)
  const original = stub.getMockImplementation()!
  stub.mockImplementation(async (url: RequestInfo | URL, options?: RequestInit) => {
    if (options?.method === 'PUT') putInits.push(options)
    return original(url, options)
  })
})

describe('C1 goal timer rows under the one save rule (FR-010)', () => {
  it('a drafted tries edit waits on the bar and one Save sends only goal_max_rounds with the token (FR-010, FR-015, FR-026, FR-027)', async () => {
    await mount()
    const before = { ...server }
    await typeDraft(screen.getByRole('spinbutton', { name: 'Tries per goal' }) as HTMLInputElement, '21')
    await saveDraft({ goal_max_rounds: 21 }, /^Tries per goal$/, before)
    expect(screen.getByRole('spinbutton', { name: 'Tries per goal' })).toHaveValue(21)
    // The saved value is served by a fresh read of the stub's state.
    expect(server.goal_max_rounds).toBe(21)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it.each([
    { ...timerDefaults },
    { goal_reminder_quiet_seconds: 120, goal_stalled_checkin_enabled: false, goal_stalled_checkin_minutes: 45 },
  ])('renders resolved saved values, explicit false, and the interval retained while off %#', async (fields) => {
    server = { ...savedFields, ...fields }
    await mount()
    const { quiet, minutes, stalled } = timerControls()
    expect(quiet).toHaveValue(fields.goal_reminder_quiet_seconds)
    expect(minutes).toHaveValue(fields.goal_stalled_checkin_minutes)
    expect(stalled).toHaveAttribute('aria-checked', String(fields.goal_stalled_checkin_enabled))
    // The interval input is disabled visually while check-ins are off but its
    // value is retained and its bounds still apply even while off
    // (docs/settings.md "Goal timers"; D-10 row 7 note). No FR retires this.
    if (fields.goal_stalled_checkin_enabled) expect(minutes).toBeEnabled()
    else expect(minutes).toBeDisabled()
    expect(puts).toEqual([])
  })

  it('OFF retains the edited interval across a quiet edit, a remount and back ON — each Save sends only its own field (FR-015)', async () => {
    const view = await mount()
    let controls = timerControls()
    let before = { ...server }
    // Interval 30 → 45 while the switch is still ON.
    await typeDraft(controls.minutes, '45')
    await saveDraft({ goal_stalled_checkin_minutes: 45 }, /interval/i, before)
    // Switch ON → OFF: only the switch rides this PUT; the interval value is
    // not re-sent and stays retained at 45.
    before = { ...server }
    fireEvent.click(controls.stalled)
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await saveDraft({ goal_stalled_checkin_enabled: false }, /check-in/i, before)
    expect(timerControls().minutes).toHaveValue(45)
    expect(timerControls().minutes).toBeDisabled()
    // An unrelated quiet edit while off: the switch stays off, the interval stays 45.
    before = { ...server }
    await typeDraft(timerControls().quiet, '120')
    await saveDraft({ goal_reminder_quiet_seconds: 120 }, /quiet/i, before)
    expect(server.goal_stalled_checkin_enabled).toBe(false)
    expect(server.goal_stalled_checkin_minutes).toBe(45)
    // A remount re-reads the saved state: retained value comes back.
    view.unmount()
    await mount()
    controls = timerControls()
    expect(controls.quiet).toHaveValue(120)
    expect(controls.minutes).toHaveValue(45)
    expect(controls.minutes).toBeDisabled()
    expect(controls.stalled).toHaveAttribute('aria-checked', 'false')
    // Back ON: the switch save carries only the boolean; the interval stays 45.
    before = { ...server }
    fireEvent.click(controls.stalled)
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await saveDraft({ goal_stalled_checkin_enabled: true }, /check-in/i, before)
    await waitFor(() => expect(timerControls().minutes).toBeEnabled())
    expect(timerControls().minutes).toHaveValue(45)
    expect(server.goal_stalled_checkin_minutes).toBe(45)
    // The rows outside this flow were never written.
    expect(server.goal_max_rounds).toBe(20)
    expect(server.max_parallel_agents).toBe(4)
    expect(server.tools_on_demand).toBe(true)
  })

  for (const [field, values] of [
    ['goal_reminder_quiet_seconds', [30, 3600]],
    ['goal_stalled_checkin_minutes', [5, 1440]],
  ] as const) {
    it.each(values)(`accepts the inclusive D-10 boundary ${field} %i through the Save bar`, async (value) => {
      const view = await mount()
      const controls = timerControls()
      const before = { ...server }
      const target = field === 'goal_reminder_quiet_seconds' ? controls.quiet : controls.minutes
      await typeDraft(target, String(value))
      await saveDraft(
        { [field]: value },
        field === 'goal_reminder_quiet_seconds' ? /quiet/i : /interval/i,
        before,
      )
      view.unmount()
      await mount()
      const reloaded = field === 'goal_reminder_quiet_seconds' ? timerControls().quiet : timerControls().minutes
      expect(reloaded).toHaveValue(value)
    })
  }

  const QUIET_MESSAGE = 'Enter a whole number from 30 to 3,600.'
  const MINUTES_MESSAGE = 'Enter a whole number from 5 to 1,440.'
  for (const [field, input, message] of [
    ['goal_reminder_quiet_seconds', ['29', '3601', '0', '-1', '30.5', ''], QUIET_MESSAGE],
    ['goal_stalled_checkin_minutes', ['4', '1441', '0', '-1', '5.5', ''], MINUTES_MESSAGE],
  ] as const) {
    it.each(input as unknown as string[])(`rejects ${field} invalid input %# visibly, naming the D-10 range, and sends nothing (FR-013)`, async (value) => {
      await mount()
      const controls = timerControls()
      const target = field === 'goal_reminder_quiet_seconds' ? controls.quiet : controls.minutes
      const before = { ...server }
      fireEvent.change(target, { target: { value } })
      await waitFor(() => expect(target.getAttribute('aria-invalid')).toBe('true'))
      expect(fieldAlertText(target)).toBe(message)
      expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
      const saveBtn = screen.getByRole('button', { name: /save changes/i })
      expect(saveBtn).toHaveAttribute('aria-disabled', 'true')
      fireEvent.click(saveBtn)
      // FR-013: focus moves to the first invalid field and nothing is sent.
      expect(target).toHaveFocus()
      expect(puts).toHaveLength(0)
      expect(recorder.prompts).toHaveLength(0)
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      expect(server).toEqual(before)
    })
  }

  it('Discard returns the drafted quiet value to the saved one and stores nothing (FR-012; FR-010 retires the cancelled confirmation)', async () => {
    await mount()
    const controls = timerControls()
    await typeDraft(controls.quiet, '120')
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => expect(controls.quiet).toHaveValue(60))
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(puts).toHaveLength(0)
    expect(recorder.prompts).toHaveLength(0)
  })

  it('a saved-but-unapplied interval survives the stale running read: 200 with pending_apply keeps the saved value shown (FR-020 outcome 1)', async () => {
    await mount()
    const controls = timerControls()
    pendingApplyNext = { stage: 'refresh', changed_fields: ['goal_stalled_checkin_minutes'] }
    await typeDraft(controls.minutes, '45')
    await saveDraft({ goal_stalled_checkin_minutes: 45 }, /interval/i, { ...savedFields, ...timerDefaults })
    // The disk write stands; the running values (server and every GET) still
    // say 30 — the field must keep the SAVED 45, never snap back (FR-020
    // outcome 1: "saved, not in use"; C-6 stage "refresh").
    expect(server.goal_stalled_checkin_minutes).toBe(30)
    await waitFor(() => expect(reads).toBeGreaterThanOrEqual(1))
    expect(timerControls().minutes).toHaveValue(45)
    // The saved-not-in-use state announces itself (the pending-apply notice
    // is the element PerformanceSection.pendingApply.test pins the copy of).
    const notice = await screen.findByTestId('performance-unapplied-notice')
    expect(notice).toHaveAttribute('role', 'status')
    // The untouched rows keep their values.
    expect(timerControls().quiet).toHaveValue(60)
    expect(timerControls().stalled).toHaveAttribute('aria-checked', 'true')
  })
})

describe('C1 saved interval while an off draft is pending', () => {
  it('the saved interval stays shown while the off draft waits on the bar; nothing is sent until Save, and Save sends only the switch (FR-010, FR-015)', async () => {
    // 75 is an arranged valid nondefault saved value, not an observation of
    // the implementation.
    const retainedMinutes = 75
    const view = await mount()
    let controls = timerControls()
    let before = { ...server }
    await typeDraft(controls.minutes, String(retainedMinutes))
    await saveDraft({ goal_stalled_checkin_minutes: retainedMinutes }, /interval/i, before)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect(puts).toHaveLength(1)
    expect(timerControls().minutes).toHaveValue(retainedMinutes)
    // Drafting the switch off: the bar counts it, no prompt runs and nothing
    // is sent (FR-010: drafts wait for Save; FR-026: the prompt runs only at
    // Save — the retired model's confirm-time prompt is gone).
    before = { ...server }
    fireEvent.click(timerControls().stalled)
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(puts).toHaveLength(1)
    expect(recorder.prompts).toHaveLength(1) // only the interval Save asked
    expect(timerControls().minutes).toHaveValue(retainedMinutes)
    expect(timerControls().minutes).toBeDisabled()
    // Saving the off draft sends ONLY the switch; the interval is retained
    // server-side and on screen (FR-015 partial update).
    await saveDraft({ goal_stalled_checkin_enabled: false }, /check-in/i, before)
    expect(timerControls().minutes).toHaveValue(retainedMinutes)
    expect(server.goal_stalled_checkin_minutes).toBe(retainedMinutes)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
  })
})
