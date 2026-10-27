// C1 independent spec oracles. Actual component/query/API/generated validators
// and catalogued controls; only the HTTP process edge is replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PerformanceSection } from './PerformanceSection'
import { ToastContainer } from '@/components/ui/toast-container'
import { useUiStore } from '@/store/ui'

const oldFields = { max_parallel_agents: 4, effective_max_parallel_agents: 4, max_parallel_agents_configured: true, tools_on_demand: true, goal_max_rounds: 20, max_tool_iterations: 1000 }
const timerDefaults = { goal_reminder_quiet_seconds: 60, goal_stalled_checkin_enabled: true, goal_stalled_checkin_minutes: 30 }
const timerKeys = Object.keys(timerDefaults)
let server: Record<string, unknown>
let disk: Record<string, unknown>
let puts: Record<string, unknown>[]
let reads: number
let failRefresh: boolean
let clients: QueryClient[]
function json(body: unknown, status = 200) { return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }) }

beforeEach(() => {
  server = { ...oldFields, ...timerDefaults }; disk = { ...server }; puts = []; reads = 0; failRefresh = false; clients = []
  useUiStore.setState({ toasts: [] })
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=c1-ui-synthetic-cookie')
  vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL, options?: RequestInit) => {
    const path = String(url).split('?')[0]
    if (path === '/api/v1/state') return json({ onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } })
    if (path !== '/api/v1/performance') throw new Error('Unexpected C1 HTTP fixture request')
    if (options?.method !== 'PUT') { reads += 1; return json(server) }
    const patch = JSON.parse(String(options.body)) as Record<string, unknown>
    puts.push(patch); disk = { ...disk, ...patch }
    if (failRefresh) {
      server = { ...server, pending_apply: { stage: 'refresh', changed_fields: Object.keys(patch) } }
      return json({ error: 'C1 synthetic saved but not applied', code: 'performance_reload_failed', details: { stage: 'refresh', changed_fields: Object.keys(patch), lowered_agents: [] } }, 500)
    }
    server = { ...server, ...patch }; return json(server)
  }))
})
afterEach(() => {
  cleanup(); clients.forEach((client) => client.clear()); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); useUiStore.setState({ toasts: [] })
})

async function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
  clients.push(client)
  const view = render(<QueryClientProvider client={client}><PerformanceSection /><ToastContainer /></QueryClientProvider>)
  await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Tries per goal' })).toHaveValue(20))
  expect(reads).toBeGreaterThan(0)
  return { ...view, client }
}
function timerControls() {
  // New product copy is NOT prescribed. Spec bounds identify the two numeric
  // controls independently; both must still have real accessible labels.
  const numbers = screen.getAllByRole('spinbutton') as HTMLInputElement[]
  const quiet = numbers.filter((input) => input.min === '30' && input.max === '3600')
  const minutes = numbers.filter((input) => input.min === '5' && input.max === '1440')
  expect(quiet, 'C1 quiet numeric control with specified bounds').toHaveLength(1)
  expect(minutes, 'C1 stalled minutes numeric control with specified bounds').toHaveLength(1)
  expect(quiet[0]).toHaveAccessibleName(); expect(minutes[0]).toHaveAccessibleName()
  expect(quiet[0].step || '1').toBe('1'); expect(minutes[0].step || '1').toBe('1')
  const all = screen.getAllByRole('switch')
  const oldSwitch = screen.getByRole('switch', { name: 'Tool loading' })
  const stalled = all.filter((control) => control !== oldSwitch)
  expect(stalled).toHaveLength(1)
  expect(all).toHaveLength(2) // Existing tool-loading switch + stalled switch; NO quiet-off switch.
  expect(stalled[0]).toHaveAccessibleName(); expect(stalled[0]).toHaveAttribute('data-ds-action', '')
  return { quiet: quiet[0], minutes: minutes[0], stalled: stalled[0] }
}
async function edit(input: HTMLElement, value: string) {
  vi.useFakeTimers()
  try { fireEvent.change(input, { target: { value } }); await act(async () => { await vi.advanceTimersByTimeAsync(700) }) }
  finally { vi.useRealTimers() }
}
async function confirmation(accept: boolean) {
  const dialog = await screen.findByRole('alertdialog')
  const target = dialog.querySelector<HTMLElement>(accept ? '[data-confirm-dialog-action]' : '[data-confirm-dialog-cancel]')
  expect(target).not.toBeNull()
  if (target === null) throw new Error('Catalogued confirmation action missing')
  fireEvent.click(target)
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
}
function assertTimerPatch(actual: Record<string, unknown>, required: Record<string, unknown>, before: Record<string, unknown>) {
  expect(Object.keys(actual).length).toBeGreaterThan(0)
  expect(actual).toMatchObject(required)
  const expectedValues = { ...before, ...required }
  for (const key of Object.keys(actual)) { expect(timerKeys).toContain(key); expect(actual[key]).toEqual(expectedValues[key]) }
}
async function saveEdit(input: HTMLElement, value: string, required: Record<string, unknown>) {
  const before = { ...server }; const count = puts.length; const priorReads = reads
  await edit(input, value)
  expect(puts).toHaveLength(count)
  await confirmation(true)
  await waitFor(() => expect(puts).toHaveLength(count + 1))
  assertTimerPatch(puts[count], required, before)
  await waitFor(() => expect(reads).toBeGreaterThan(priorReads))
}

describe('C1 actual PerformanceSection goal timer controls', () => {
  it('existing goal edit traverses actual API, confirmation and refetch instrument', async () => {
    await mount()
    await edit(screen.getByRole('spinbutton', { name: 'Tries per goal' }), '21')
    expect(puts).toHaveLength(0)
    await confirmation(true)
    await waitFor(() => expect(puts).toEqual([{ goal_max_rounds: 21 }]))
    await waitFor(() => expect(server.goal_max_rounds).toBe(21))
    await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Tries per goal' })).toHaveValue(21))
    expect(reads).toBeGreaterThanOrEqual(2)
  })
  it.each([
    { ...timerDefaults },
    { goal_reminder_quiet_seconds: 120, goal_stalled_checkin_enabled: false, goal_stalled_checkin_minutes: 45 },
  ])('renders resolved defaults/nondefaults and explicit false without losing retained value %#', async (fields) => {
    server = { ...oldFields, ...fields }; disk = { ...server }
    await mount(); const { quiet, minutes, stalled } = timerControls()
    expect(quiet).toHaveValue(fields.goal_reminder_quiet_seconds)
    expect(minutes).toHaveValue(fields.goal_stalled_checkin_minutes)
    expect(stalled).toHaveAttribute('aria-checked', String(fields.goal_stalled_checkin_enabled))
    if (fields.goal_stalled_checkin_enabled) expect(minutes).toBeEnabled(); else expect(minutes).toBeDisabled()
    expect(puts).toEqual([])
  })
  it('OFF retains edited minutes across unrelated quiet edit, refetch/remount and ON', async () => {
    let view = await mount(); let controls = timerControls()
    await saveEdit(controls.minutes, '45', { goal_stalled_checkin_minutes: 45 })
    const count = puts.length; const before = { ...server }
    fireEvent.click(controls.stalled); expect(puts).toHaveLength(count); await confirmation(true)
    await waitFor(() => expect(puts).toHaveLength(count + 1))
    assertTimerPatch(puts[count], { goal_stalled_checkin_enabled: false }, before)
    await waitFor(() => expect(controls.minutes).toBeDisabled()); expect(controls.minutes).toHaveValue(45)
    await saveEdit(controls.quiet, '120', { goal_reminder_quiet_seconds: 120 })
    expect(server.goal_stalled_checkin_enabled).toBe(false); expect(server.goal_stalled_checkin_minutes).toBe(45)
    view.unmount(); view = await mount(); controls = timerControls()
    expect(controls.quiet).toHaveValue(120); expect(controls.minutes).toHaveValue(45); expect(controls.minutes).toBeDisabled(); expect(controls.stalled).toHaveAttribute('aria-checked', 'false')
    const onCount = puts.length; fireEvent.click(controls.stalled); await confirmation(true)
    await waitFor(() => expect(puts).toHaveLength(onCount + 1))
    expect(server.goal_stalled_checkin_enabled).toBe(true); expect(server.goal_stalled_checkin_minutes).toBe(45)
    await waitFor(() => expect(controls.minutes).toBeEnabled()); expect(controls.minutes).toHaveValue(45)
    expect(server.goal_max_rounds).toBe(20); expect(server.max_parallel_agents).toBe(4); expect(server.tools_on_demand).toBe(true)
  })
  for (const [field, values] of [
    ['goal_reminder_quiet_seconds', [30, 3600]], ['goal_stalled_checkin_minutes', [5, 1440]],
  ] as const) {
    it.each(values)(`accepts inclusive ${field} boundary %i through real save/refetch`, async (value) => {
      const view = await mount(); const controls = timerControls()
      await saveEdit(field === 'goal_reminder_quiet_seconds' ? controls.quiet : controls.minutes, String(value), { [field]: value })
      view.unmount(); await mount(); const reloaded = timerControls()
      expect(field === 'goal_reminder_quiet_seconds' ? reloaded.quiet : reloaded.minutes).toHaveValue(value)
    })
  }
  for (const [field, values] of [
    ['goal_reminder_quiet_seconds', ['29', '3601', '0', '-1', '30.5', '']],
    ['goal_stalled_checkin_minutes', ['4', '1441', '0', '-1', '5.5', '']],
  ] as const) {
    it.each(values)(`rejects ${field} invalid input case %# visibly without save`, async (value) => {
      await mount(); const controls = timerControls(); const before = { ...server }
      await edit(field === 'goal_reminder_quiet_seconds' ? controls.quiet : controls.minutes, value)
      expect(puts).toEqual([]); expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      const errors = screen.getAllByRole('alert')
      expect(errors.length).toBeGreaterThan(0)
      expect(errors.some((error) => /whole|between|least|invalid|required|number|must/i.test(error.textContent ?? ''))).toBe(true)
      expect(server).toEqual(before)
    })
  }
  it('cancelled timer confirmation never persists and restores the server value', async () => {
    await mount(); const controls = timerControls()
    await edit(controls.quiet, '120'); expect(puts).toEqual([]); await confirmation(false)
    expect(puts).toEqual([]); expect(server.goal_reminder_quiet_seconds).toBe(60)
    await waitFor(() => expect(controls.quiet).toHaveValue(60))
  })
  it('saved-but-unapplied timer value survives the stale live GET rather than snapping back', async () => {
    await mount(); const controls = timerControls(); failRefresh = true
    await edit(controls.minutes, '45'); await confirmation(true)
    await waitFor(() => expect(puts).toHaveLength(1)); expect(disk.goal_stalled_checkin_minutes).toBe(45)
    expect(server.goal_stalled_checkin_minutes).toBe(30)
    await waitFor(() => expect(reads).toBeGreaterThanOrEqual(2))
    expect(controls.minutes).toHaveValue(45)
    expect(screen.getAllByText(/saved.*not applied/i).length).toBeGreaterThan(0)
    expect(controls.quiet).toHaveValue(60); expect(controls.stalled).toHaveAttribute('aria-checked', 'true')
  })
})


describe('C1 saved minutes while OFF confirmation is pending', () => {
  it('keeps saved minutes in the live disabled field before OFF confirmation or persistence', async () => {
    // C1 OFF preserves the visible retained value; 75 is an arranged valid
    // nondefault saved value, not an observation of the implementation.
    const retainedMinutes = 75
    const view = await mount()
    const controls = timerControls()
    await saveEdit(controls.minutes, String(retainedMinutes), { goal_stalled_checkin_minutes: retainedMinutes })
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect(puts).toHaveLength(1)
    expect(timerControls().minutes).toHaveValue(retainedMinutes)
    const savedPutCount = puts.length
    const savedReadCount = reads
    fireEvent.click(timerControls().stalled)
    const pendingDialog = await screen.findByRole('alertdialog')
    expect(pendingDialog).toBeVisible()
    expect(puts).toHaveLength(savedPutCount)
    expect(reads).toBe(savedReadCount)
    // The real modal hides background controls from the accessibility tree.
    // Re-query the current DOM with the same contract bounds as timerControls.
    const pendingMinutes = screen.getAllByRole('spinbutton', { hidden: true }).filter((input) => input.getAttribute('min') === '5' && input.getAttribute('max') === '1440')
    expect(pendingMinutes).toHaveLength(1)
    expect(pendingMinutes[0]).toBeInTheDocument()
    expect(pendingMinutes[0]).toBeDisabled()
    expect(pendingMinutes[0]).toHaveValue(retainedMinutes)
    expect(screen.getByRole('alertdialog')).toBeVisible()
    expect(puts).toHaveLength(savedPutCount)
    expect(reads).toBe(savedReadCount)
  })
})
