/**
 * PerformanceSection.goalTimerOwnership.gate1.test.tsx — C1 combined-gate
 * timer-edit ownership, restated for the Settings redesign #1519 Save bar.
 *
 * The old file drove the RETIRED autosave race machinery: a 700 ms debounce,
 * a per-change "Change the performance settings?" confirm, a held in-flight
 * PUT racing a newer debounce flush, and an auto-reconsent queue that reused
 * a settled consent for queued intent. FR-010 retires all of it — a change
 * is a draft on the bar, saving is one Save changes per batch, and consent
 * is one prompt per Save (FR-026) whose token is never reused for a later
 * Save. The properties the spec still requires survive restated, each citing
 * the FR that replaces the retired mechanism:
 *   - CG-CR-1: a cancelled timer draft is discarded before an unrelated
 *     switch-only save, which carries ONLY the switch (FR-012, FR-015);
 *   - CG-CR-2: a newer edit is not lost — both when it is drafted after an
 *     older save settles and (the held-save interleaving, restored after
 *     CHECK W1) when it is typed into the SAME field while an older Save is
 *     still in flight; each is saved by its own Save as its own partial PUT
 *     (FR-010, FR-015);
 *   - TM5 (success): consent is per Save — no prompt fires between saves,
 *     and the second Save asks again before its write (FR-026); the retired
 *     automatic re-consent queue has no successor (FR-010 removed the queue
 *     by removing the always-writing model it fed);
 *   - TM5 (older save fails): the refused draft is kept with the not-saved
 *     banner (FR-020 outcome 3) and the newer value then saves with fresh
 *     consent (FR-026).
 *
 * The local-mode consent queue (in-product password dialog racing a held
 * PUT) is retired with the same machinery; the prompt boundary contract
 * below is mode-agnostic at this section's edge (the prompt UI itself is
 * passwordOncePerSave.test's subject).
 *
 * Harness: the real component, query client and API layer; only the HTTP
 * process edge is a stub. The password prompt is the shell's, supplied
 * through SettingsShellContext as a recorder (FR-026/FR-027).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PerformanceSection } from './PerformanceSection'

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

let server: Record<string, unknown>
let puts: Array<Record<string, unknown>>
let reads: number
let clients: QueryClient[]
let failNextPut: boolean
// Held-PUT seam (restored CG-CR-2): the next PUT parks at the HTTP edge
// until the test releases it, so a newer edit can be typed while an older
// Save is genuinely in flight.
let holdNextPut: boolean
let releaseHeldPut: (() => void) | null
let putStarted: Promise<void>
let signalPutStarted: () => void

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  server = { ...savedFields, ...timerDefaults }
  puts = []
  reads = 0
  clients = []
  failNextPut = false
  holdNextPut = false
  releaseHeldPut = null
  putStarted = new Promise<void>((resolve) => {
    signalPutStarted = resolve
  })
  recorder = promptRecorder()
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=c1-ui-synthetic-cookie')
  vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL, options?: RequestInit) => {
    const path = String(url).split('?')[0]
    if (path === '/api/v1/state') {
      return json({ onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } })
    }
    if (path !== '/api/v1/performance') throw new Error('Unexpected gate1 HTTP fixture request')
    if (options?.method !== 'PUT') {
      reads += 1
      return json(server)
    }
    const patch = JSON.parse(String(options.body)) as Record<string, unknown>
    puts.push(patch)
    if (holdNextPut) {
      holdNextPut = false
      await new Promise<void>((resolve) => {
        releaseHeldPut = resolve
        signalPutStarted()
      })
    }
    if (failNextPut) {
      failNextPut = false
      // FR-020 outcome 3: the server refuses; nothing is written.
      return json({ error: 'gate1 synthetic save refused', code: 'gate1_refused' }, 500)
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

let recorder: ReturnType<typeof promptRecorder>

async function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
  clients.push(client)
  const { SettingsShellContext } = await loadShellContext()
  const view = render(
    <QueryClientProvider client={client}>
      <SettingsShellContext.Provider
        value={{
          announce: () => {},
          setDraftCount: () => {},
          offerRestart: () => {},
          refreshPendingRestart: () => {},
          requestPassword: recorder.requestPassword,
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

/** The timer rows, addressed by the names the spec's own tables give them (C-7, D-10 rows 6–7). */
function timerControls() {
  const quiet = screen.getByRole('spinbutton', { name: 'Idle reminder quiet window' }) as HTMLInputElement
  const minutes = screen.getByRole('spinbutton', { name: 'Check-in interval' }) as HTMLInputElement
  const all = screen.getAllByRole('switch')
  const toolLoading = screen.getByRole('switch', { name: 'Tool loading' })
  const stalled = all.filter((control) => control !== toolLoading)
  expect(stalled, 'the check-in switch beside Tool loading (the group holds no other switch)').toHaveLength(1)
  expect(all).toHaveLength(2) // Tool loading + stalled check-ins; NO quiet-off switch.
  return { quiet, minutes, stalled: stalled[0] }
}

/** A typed change is a draft on the bar: counted, password-marked, never sent (FR-010, FR-011). */
async function typeDraft(input: HTMLInputElement, value: string) {
  // Both counts are taken BEFORE the edit, independently of what the edit
  // itself does — comparing a post-edit length against itself (the old
  // putCountBeforeDraft helper) proves nothing.
  const putsBefore = puts.length
  const promptsBefore = recorder.prompts.length
  fireEvent.change(input, { target: { value } })
  expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
  expect(screen.getByText(/1 needs your password/)).toBeInTheDocument()
  expect(puts).toHaveLength(putsBefore) // a draft sends nothing (FR-010)
  expect(recorder.prompts).toHaveLength(promptsBefore) // drafting asks for no password (FR-010)
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
  const allowedKeys = [...Object.keys(timerDefaults), 'goal_max_rounds']
  for (const key of Object.keys(patch)) {
    expect(allowedKeys).toContain(key)
    expect(patch[key]).toEqual({ ...before, ...required }[key])
  }
}

describe('C1 combined-gate timer edit ownership under the one save rule', () => {
  it('CG-CR-1 discards a drafted timer before a switch-only save that carries only the switch (FR-012, FR-015)', async () => {
    const view = await mount()
    // Draft the quiet edit; FR-010: it waits on the bar, nothing is sent.
    await typeDraft(timerControls().quiet, '120')
    // FR-012 (replaces the cancelled confirmation, FR-010): Discard returns
    // the draft to the saved value and stores nothing.
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => expect(timerControls().quiet).toHaveValue(60))
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(puts).toHaveLength(0)
    expect(recorder.prompts).toHaveLength(0)
    const initialReads = reads
    // The unrelated switch draft saves alone: only the boolean rides the PUT,
    // and neither the discarded quiet value nor any sibling appears in it.
    fireEvent.click(timerControls().stalled)
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await saveDraft({ goal_stalled_checkin_enabled: false }, /check-in/i, { ...savedFields, ...timerDefaults })
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect(server.goal_stalled_checkin_enabled).toBe(false)
    expect(server.goal_reminder_quiet_seconds).toBe(60)
    expect(timerControls().quiet).toHaveValue(60)
    expect(reads).toBeGreaterThan(initialReads)
  })

  it('CG-CR-2 a newer edit drafted after an older save settles is not lost — its own Save sends it as its own partial PUT (FR-010, FR-015)', async () => {
    const view = await mount()
    // Older save: quiet 60 → 120.
    let before = { ...server }
    await typeDraft(timerControls().quiet, '120')
    await saveDraft({ goal_reminder_quiet_seconds: 120 }, /quiet/i, before)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    // Newer edit drafted only after the older save settled: 30 → 45.
    before = { ...server }
    await typeDraft(timerControls().minutes, '45')
    await saveDraft({ goal_stalled_checkin_minutes: 45 }, /interval/i, before)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    // Both edits persisted, each in its own partial PUT.
    expect(puts).toHaveLength(2)
    expect(puts[0]).toEqual({ goal_reminder_quiet_seconds: 120 })
    expect(puts[1]).toEqual({ goal_stalled_checkin_minutes: 45 })
    expect(timerControls().quiet).toHaveValue(120)
    expect(timerControls().minutes).toHaveValue(45)
    expect(server.goal_reminder_quiet_seconds).toBe(120)
    expect(server.goal_stalled_checkin_minutes).toBe(45)
  })

  it('CG-CR-2 (held save, restored) a newer edit of the same field typed while an older Save is still in flight survives the older response, then saves by its own Save (FR-010, FR-011, FR-015)', async () => {
    const view = await mount()
    // The older Save: quiet 60 → 120, with its PUT held at the HTTP edge so
    // the newer input is genuinely typed while a Save is outstanding (the
    // timing case the reconciled replacement lost — CHECK W1).
    holdNextPut = true
    let before = { ...server }
    await typeDraft(timerControls().quiet, '120')
    await saveDraft({ goal_reminder_quiet_seconds: 120 }, /quiet/i, before)
    // putStarted proves the older PUT reached the HTTP edge and is parked
    // there: the fetch handler is suspended until releaseHeldPut, so the
    // Save is genuinely still in flight (the form save bar does not ride a
    // React Query mutation, so isMutating is not its instrument).
    await putStarted
    expect(puts).toHaveLength(1)

    // The newer edit of the SAME field lands while the older response is
    // still outstanding. Drafting asks for no password: the prompt count is
    // taken BEFORE the edit, independently of it.
    const promptsBeforeEdit = recorder.prompts.length
    fireEvent.change(timerControls().quiet, { target: { value: '90' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(puts).toHaveLength(1)
    expect(recorder.prompts).toHaveLength(promptsBeforeEdit)

    // The older response now lands. FR-010/FR-011/FR-015: an unsaved draft
    // stays a draft until its own Save or a Discard — completing the OLDER
    // save must not erase the NEWER edit of the same field.
    await act(async () => {
      releaseHeldPut?.()
    })
    // The older response landed and its outcome ran; the newer draft of the
    // same field must still own the field.
    await waitFor(() => expect(screen.getByLabelText('Idle reminder quiet window')).toHaveValue(90))
    expect(timerControls().quiet).toHaveValue(90)
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(puts).toHaveLength(1)
    // The older save's own value did persist.
    expect(server.goal_reminder_quiet_seconds).toBe(120)

    // Explicit Save cadence (FR-010): the surviving draft is stored by its
    // own Save — one fresh prompt, one partial PUT of only that value.
    before = { ...server }
    await saveDraft({ goal_reminder_quiet_seconds: 90 }, /quiet/i, before)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect(puts).toHaveLength(2)
    expect(puts[1]).toEqual({ goal_reminder_quiet_seconds: 90 })
    expect(timerControls().quiet).toHaveValue(90)
    expect(server.goal_reminder_quiet_seconds).toBe(90)
  })
})

// Founder TM5, restated: consent is per Save. The retired auto-reconsent
// queue is gone with the always-writing model it fed (FR-010); its substance
// — a newer edit never rides an older save's consent — is FR-026's one
// prompt per Save: no prompt between saves, a fresh prompt for the next
// Save, and a refused draft kept for a fresh-consent retry.
describe('TM5 consent cadence for timer edits (FR-026)', () => {
  it('TM5 no prompt fires between two saves; the second Save asks again before its write', async () => {
    const view = await mount()
    let before = { ...server }
    await typeDraft(timerControls().minutes, '45')
    await saveDraft({ goal_stalled_checkin_minutes: 45 }, /interval/i, before)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    // The retired auto-reconsent queue would open the next prompt by itself
    // after settlement; FR-026 asks per Save instead.
    expect(recorder.prompts).toHaveLength(1)
    expect(view.client.isMutating()).toBe(0)
    before = { ...server }
    await typeDraft(timerControls().minutes, '75')
    expect(recorder.prompts).toHaveLength(1) // drafting asks nothing (FR-010)
    await saveDraft({ goal_stalled_checkin_minutes: 75 }, /interval/i, before)
    expect(recorder.requestPassword).toHaveBeenCalledTimes(2)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect(puts[1]).toEqual({ goal_stalled_checkin_minutes: 75 })
    expect(timerControls().minutes).toHaveValue(75)
    expect(server.goal_stalled_checkin_minutes).toBe(75)
  })

  it('TM5 an older save that fails keeps its draft with the not-saved banner; the newer value then saves with fresh consent (FR-020 outcome 3, FR-026)', async () => {
    const view = await mount()
    failNextPut = true
    await typeDraft(timerControls().minutes, '45')
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(recorder.prompts.length).toBe(1))
    await waitFor(() => expect(puts).toHaveLength(1))
    // FR-020 outcome 3: the refused draft is kept with the banner.
    expect(await screen.findByText('These changes were not saved. Save them again.')).toBeInTheDocument()
    expect(timerControls().minutes).toHaveValue(45)
    expect(server.goal_stalled_checkin_minutes).toBe(30)
    // The newer value replaces the refused draft on the bar (FR-010: a
    // drafted edit is never sent by itself; the not-saved count wording is
    // the bar's own state, not re-pinned here), then saves with a FRESH
    // prompt — the failed save's consent is not reused (FR-026).
    const putsBefore = puts.length
    fireEvent.change(timerControls().minutes, { target: { value: '75' } })
    expect(puts).toHaveLength(putsBefore)
    await saveDraft({ goal_stalled_checkin_minutes: 75 }, /interval/i, { ...savedFields, ...timerDefaults })
    expect(recorder.requestPassword).toHaveBeenCalledTimes(2)
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect(puts).toHaveLength(2)
    expect(puts[1]).toEqual({ goal_stalled_checkin_minutes: 75 })
    expect(timerControls().minutes).toHaveValue(75)
    expect(server.goal_stalled_checkin_minutes).toBe(75)
    expect(screen.queryByText(/These changes were not saved/)).not.toBeInTheDocument()
  })
})
