/**
 * SettingsScreen.announce.test.tsx — Settings redesign #1519 (CHECK R3-07).
 *
 * The announce timer's lifecycle — spec FR-018 (the Save bar's "Saved at" and
 * its spoken twin) with FR-071 (announcements are spoken, not shown): the
 * shell announces save outcomes through a polite live region, one pending
 * announcement at a time. The screen's own comment states the contract under
 * test, and the spec's scoping gives its two edges: "a newer one replaces it"
 * (supersede) and — the oracle both cases here enforce — none may fire after
 * the screen is gone (SettingsScreen.tsx announce/unmount effect). CHECK
 * round 3 proved no existing test sees either edge: check mutants N27
 * (pending announcement survives unmount) and N28 (a newer announcement's
 * timer does not cancel the older one) both survived the suite.
 *
 * Oracles (spec, never the code):
 *   - Supersede (N28): two save outcomes in a row — when the second outcome
 *     announces, the first's pending timer must be gone, so between the two
 *     30ms deadlines (first due, second not yet) the live region is EMPTY;
 *     the second announcement lands at its own deadline. A region showing the
 *     first announcement in that window means the older timer fired — N28.
 *   - Unmount (N27): while an announcement is pending the screen goes away;
 *     after its deadline has passed the announcement must never have fired.
 *     The region is removed with the screen, so a DOM read cannot see the
 *   difference — the oracle is the announcement's callback itself: a
 *   delegating recorder around setTimeout records the pending handle and
 *   whether it ever ran. A fired handle after unmount — N27.
 *
 * Unit boundary: the REAL screen (real routeTree in a real router, the
 * settingsRoute harness) with the real Data & retention section driving the
 * outcomes through its real save path (useFormSaveBar::finish announces
 * "Changes saved." on success and "Nothing was saved. <reason>" on failure).
 * '@/lib/api' is mocked at the API seam only. The failed-save leg is used for
 * the unmount case because a failed save leaves no saved-at twin timer — the
 * pending announce handle is unambiguous.
 *
 * Fake-timer discipline: setTimeout/clearTimeout/Date are faked; setInterval
 * is NOT (the pending-restart poll's 10s interval and RTL's waitFor polling
 * stay real), and userEvent is set up with advanceTimers so its zero-delay
 * waits run on the fake clock without moving it.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import { updateRetentionSettings } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // _app.tsx beforeLoad + AppShell + Sidebar (the settingsRoute harness set).
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
    fetchKnowledgeBaseInfo: vi.fn(async () => ({
      workspace_id: 'ws-1',
      root_path: '',
      is_knowledge_base: false,
      marker: 'none',
    })),
    // The shell's own reads: gateway reachability (FR-067 — answers, so no
    // Callout) and the pending-restart poll (FR-033 — nothing waiting).
    fetchAboutInfo: vi.fn(async () => ({
      version: '0.1.1',
      go_version: 'go1.24',
      os: 'linux',
      arch: 'amd64',
      uptime_seconds: 100,
    })),
    fetchPendingRestart: vi.fn(async () => []),
    // The Data & retention section: the embedded storage counts + Danger zone.
    fetchConfig: vi.fn(async () => ({ gateway: {}, data: { session_retention_days: 90 } })),
    fetchStorageStats: vi.fn(async () => ({
      workspace_size_bytes: 10,
      session_count: 3,
      memory_entry_count: 5,
      warnings: [],
    })),
    // The retention group's read/write — each test pins the write's outcome.
    fetchRetentionSettings: vi.fn(async () => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    updateRetentionSettings: vi.fn(),
  }
})

// ── The timer recorder (the unmount case's oracle instrument) ───────────────

type Schedule = { id: unknown; delay: number | undefined }
let recorder: { scheduled: Schedule[]; fired: Set<unknown>; cleared: Set<unknown> } | null = null
let setSpy: { mockRestore(): void } | null = null
let clearSpy: { mockRestore(): void } | null = null

/**
 * Wrap the (already fake) global timers so every schedule is recorded with
 * its delay and every callback announces that it ran. Purely delegating —
 * the real (fake-clock) timers still do all the work. The wrap is a
 * vi.spyOn, NOT a plain reassignment: RTL's fake-timer detection
 * (jestFakeTimersAreEnabled) reads `setTimeout._isMockFunction`/`.clock`, and
 * a plain function replacement hides both — every asyncWrapper-driven await
 * (userEvent, waitFor) then deadlocks on a faked setTimeout(0) it never
 * advances. A vitest mock keeps `_isMockFunction` true.
 */
function installTimerRecorder() {
  const fakeSet = globalThis.setTimeout
  const fakeClear = globalThis.clearTimeout
  const active: NonNullable<typeof recorder> = { scheduled: [], fired: new Set(), cleared: new Set() }
  recorder = active
  setSpy = vi
    .spyOn(globalThis, 'setTimeout')
    .mockImplementation(((cb: (...args: unknown[]) => void, ms?: number, ...rest: unknown[]) => {
      const wrapped = (...args: unknown[]) => {
        active.fired.add(id)
        cb(...args)
      }
      const id = fakeSet(wrapped as never, ms, ...(rest as never[]))
      active.scheduled.push({ id, delay: ms })
      return id
    }) as typeof setTimeout) as unknown as { mockRestore(): void }
  clearSpy = vi
    .spyOn(globalThis, 'clearTimeout')
    .mockImplementation(((id?: unknown) => {
      if (id !== undefined && id !== null) active.cleared.add(id)
      return fakeClear(id as never)
    }) as typeof clearTimeout) as unknown as { mockRestore(): void }
}

function uninstallTimerRecorder() {
  setSpy?.mockRestore()
  clearSpy?.mockRestore()
  setSpy = null
  clearSpy = null
  recorder = null
}

/**
 * The announce timer: the save flow's only 30ms schedule (a failed save
 * leaves no saved-at twin, and the saved-at signal clears at 8s — far from
 * the 30–41ms window these cases read). `which` picks the sole schedule seen
 * so far ('only') or the newest one after several outcomes ('last').
 */
function announceSchedule(which: 'only' | 'last'): Schedule {
  const hits = (recorder as { scheduled: Schedule[] }).scheduled.filter((s) => s.delay === 30)
  expect(
    hits.length > 0,
    `BLOCKED: the save outcome must have scheduled a 30ms announce timer — schedules seen: ${JSON.stringify((recorder as { scheduled: Schedule[] }).scheduled.map((s) => s.delay))}`,
  ).toBe(true)
  if (which === 'only') {
    expect(hits, 'BLOCKED: exactly one announce schedule was expected so far').toHaveLength(1)
  }
  return hits[hits.length - 1] as Schedule
}

function regionText(): string {
  return (screen.getByTestId('settings-announcer') as HTMLElement).textContent ?? ''
}

async function renderSettings(search: string) {
  window.localStorage.setItem('omnipus_auth_username', 'settings-screen-tester')
  Element.prototype.scrollIntoView = Element.prototype.scrollIntoView ?? (() => {})
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
  const history = createMemoryHistory({ initialEntries: [`/settings${search}`] })
  const router = createRouter({ routeTree, history })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  // The section body once its read answers.
  const chat = await screen.findByLabelText('Keep chat history for')
  return { view, chat }
}

/**
 * Flush the pending work without moving the clock. Microtask hops drain the
 * write chain (mock, react-query with retry off, finish → shell.announce);
 * the sync advance(0) then runs only timers due RIGHT NOW (leftover
 * zero-delay scheduler hops). The announce timer (due +30ms) never comes due
 * in a flush, and no macrotask boundary lets the clock wander.
 */
async function flushOutcome() {
  await act(async () => {
    for (let i = 0; i < 50; i++) await Promise.resolve()
  })
  act(() => { vi.advanceTimersByTime(0) })
}

/**
 * Edit the chat-retention field and run the section's real Save changes.
 * Field entry and the click are fireEvent — synchronous DOM events inside
 * RTL's event-wrapper act. The unit boundary here is the SAVE FLOW (click →
 * useSaveBar.save → write → finish → shell.announce), not keystroke
 * mechanics, and fireEvent keeps every step off the timer queues: under fake
 * timers user-event's own per-call waits add macrotask hops that can push
 * the clock past the announce's 30ms deadline mid-interaction.
 */
function setRetentionDraft(days: string): HTMLInputElement {
  const chat = screen.getByLabelText('Keep chat history for') as HTMLInputElement
  fireEvent.change(chat, { target: { value: days } })
  return chat
}

async function saveRetention(days: string) {
  setRetentionDraft(days)
  fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
  await flushOutcome()
}

/**
 * Trigger the invalid-draft announce (FR-018's spoken fix): 366 is outside
 * the range, so clicking Save announces the fix and sends nothing — no
 * write, no saved-at twin. The only 30ms schedule this flow creates is the
 * announce itself.
 */
function clickSaveWithInvalidDraft() {
  const chat = setRetentionDraft('366')
  expect(screen.getByText('Enter a whole number from 1 to 365.')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
  void chat
  return flushOutcome()
}

beforeEach(() => {
  vi.clearAllMocks()
  // Pure fake clock. RTL's asyncWrapper drain (setTimeout(0) + advance) only
  // advances the clock when a `jest` global exists — vitest has none, so
  // without this shim every RTL await (waitFor/findBy) deadlocks on a
  // never-advanced faked setTimeout(0), and with the coarse real-time
  // alternative (shouldAdvanceTime) the clock jumps cannot hold a 30ms
  // deadline. The shim is the standard vitest+RTL recipe, inside this file.
  vi.stubGlobal('jest', vi)
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] })
  // The recorder instruments both cases: it locates the announce handle and
  // is the unmount case's oracle (fired/cleared sets). Purely delegating; a
  // vi.spyOn so RTL's fake-timer detection still sees a mock function.
  installTimerRecorder()
})

afterEach(() => {
  uninstallTimerRecorder()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('FR-018/FR-071 — the announce timer lifecycle (CHECK R3-07)', () => {
  it('a newer save outcome supersedes the pending announcement: the older timer is cancelled and never fires; the newer lands (N28)', async () => {
    vi.mocked(updateRetentionSettings).mockResolvedValue({
      session_days: 30,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })
    await renderSettings('?tab=data')

    // First outcome announces: its timer goes pending. The clock has not
    // moved since its schedule (no advance happened), so the pending premise
    // is exact, not a race.
    await saveRetention('30')
    const first = announceSchedule('only')
    expect(recorder?.cleared.has(first.id), 'BLOCKED: nothing may have cleared the first announce yet').toBe(false)
    expect(recorder?.fired.has(first.id), 'BLOCKED: the first announce must still be pending here').toBe(false)
    expect(regionText(), 'FR-018: the region stays empty while the announcement is pending').toBe('')

    // A second outcome announces while the first is pending (both due at the
    // same frozen now + 30ms). The newer must cancel the older — under the
    // mutant (N28) the older handle is never cleared, so it fires with the
    // newer at the deadline.
    await saveRetention('31')
    const second = announceSchedule('last')
    expect(second.id, 'BLOCKED: the second outcome must schedule its own announce').not.toBe(first.id)
    expect(recorder?.cleared.has(first.id),
      'RED (R3-07/N28): the newer announcement must clear the older pending one — the region would speak a stale outcome').toBe(true)

    // Past the (shared) deadline: the newer announcement lands at its own
    // deadline (the mechanism works — this is what makes the cancellation
    // meaningful), and the superseded one never fires at all.
    act(() => { vi.advanceTimersByTime(31) })
    expect(recorder?.fired.has(second.id), 'BLOCKED: the newer announce must fire at its deadline').toBe(true)
    expect(regionText(), 'FR-018: the newer announcement is what the region speaks').toBe('Changes saved.')
    expect(recorder?.fired.has(first.id),
      'RED (R3-07/N28): the superseded announcement must never fire').toBe(false)
  })

  it('a pending announcement never fires after the screen is gone (N27)', async () => {
    const { view } = await renderSettings('?tab=data')

    // The invalid draft announces the fix (the spoken FR-018 correction) and
    // sends nothing — the pending announce timer is the flow's only 30ms
    // schedule, with no saved-at twin anywhere.
    await clickSaveWithInvalidDraft()
    expect(vi.mocked(updateRetentionSettings), 'BLOCKED: the invalid draft must send nothing').not.toHaveBeenCalled()
    const pending = announceSchedule('only')
    expect(recorder?.cleared.has(pending.id), 'BLOCKED: nothing may have cleared the pending announce yet').toBe(false)
    expect(recorder?.fired.has(pending.id), 'BLOCKED: the announce must still be pending here').toBe(false)
    expect(regionText(), 'FR-018: the region stays empty while the announcement is pending').toBe('')

    // The screen goes away with the announcement still pending. FR-018/FR-071
    // scoping: none may fire after the screen is gone.
    act(() => { view.unmount() })
    expect(recorder?.cleared.has(pending.id),
      'RED (R3-07/N27): unmounting must cancel the pending announce timer').toBe(true)

    // Past the pending deadline: the announcement must never have fired. The
    // region is gone with the screen, so the oracle is the recorder — the
    // announcement's own callback must not run (under N27 it does, calling
    // into the unmounted screen).
    act(() => { vi.advanceTimersByTime(31) })
    expect(recorder?.fired.has(pending.id),
      'RED (R3-07/N27): the pending announcement must never fire after the screen is gone').toBe(false)
    expect(screen.queryByTestId('settings-announcer'), 'BLOCKED: the screen (and its region) must be gone').not.toBeInTheDocument()
  })
})
