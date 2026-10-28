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

let server: Record<string, unknown>
let disk: Record<string, unknown>
let puts: Record<string, unknown>[]
let reads: number
let failRefresh: boolean
let clients: QueryClient[]
function json(body: unknown, status = 200) { return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }) }

beforeEach(() => {
  server = { ...oldFields, ...timerDefaults }; disk = { ...server }; puts = []; reads = 0; failRefresh = false; clients = []
  gate1Mode = 'platform'; gate1HoldPut = false; gate1ReleasePut = null; gate1ReauthCount = 0
  gate1PutStarted = new Promise<void>((resolve) => { gate1SignalPutStarted = resolve })
  useUiStore.setState({ toasts: [] })
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=c1-ui-synthetic-cookie')
  vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL, options?: RequestInit) => {
    const path = String(url).split('?')[0]
    if (path === '/api/v1/state') return json({ onboarding_complete: true, identity: { mode: gate1Mode, edition: gate1Mode === 'local' ? 'core' : 'hosted', signed_in: true } })
    if (path === '/api/v1/auth/reauth') { gate1ReauthCount += 1; return json({ verified: true, token: gate1Token, expires_in: 300 }) }
    if (path !== '/api/v1/performance') throw new Error('Unexpected C1 HTTP fixture request')
    if (options?.method !== 'PUT') { reads += 1; return json(server) }
    const patch = JSON.parse(String(options.body)) as Record<string, unknown>
    puts.push(patch); disk = { ...disk, ...patch }
    if (gate1HoldPut) { gate1HoldPut = false; await new Promise<void>((resolve) => { gate1ReleasePut = resolve; gate1SignalPutStarted() }) }
    if (failRefresh) {
      server = { ...server, pending_apply: { stage: 'refresh', changed_fields: Object.keys(patch) } }
      return json({ error: 'C1 synthetic saved but not applied', code: 'performance_reload_failed', details: { stage: 'refresh', changed_fields: Object.keys(patch), lowered_agents: [] } }, 500)
    }
    server = { ...server, ...patch }; return json(server)
  }))
})
afterEach(() => {
  gate1ReleasePut?.(); cleanup(); clients.forEach((client) => client.clear()); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); useUiStore.setState({ toasts: [] })
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



let gate1Mode: 'platform' | 'local'
let gate1HoldPut: boolean
let gate1ReleasePut: (() => void) | null
let gate1PutStarted: Promise<void>
let gate1SignalPutStarted: () => void
let gate1ReauthCount: number
const gate1Password = 'mypassword'
const gate1Token = 'reauth_tok'
async function gate1LocalConsent() {
  const input = await screen.findByTestId('reauth-password-input')
  fireEvent.change(input, { target: { value: gate1Password } })
  fireEvent.click(screen.getByTestId('reauth-confirm'))
  await waitFor(() => expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument())
}


describe('C1 combined-gate timer edit ownership', () => {
  it('CG-CR-1 discards a cancelled timer before a separately confirmed switch-only save', async () => {
    const view = await mount()
    await edit(timerControls().quiet, '120')
    expect(puts).toHaveLength(0)
    await confirmation(false)
    await waitFor(() => expect(timerControls().quiet).toHaveValue(60))
    expect(puts).toHaveLength(0)
    const initialReads = reads
    fireEvent.click(timerControls().stalled)
    await confirmation(true)
    await waitFor(() => expect(puts).toHaveLength(1))
    await waitFor(() => expect(reads).toBeGreaterThan(initialReads))
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect({ request: puts[0], savedQuiet: disk.goal_reminder_quiet_seconds, liveQuiet: server.goal_reminder_quiet_seconds, visibleQuiet: timerControls().quiet.value, enabled: server.goal_stalled_checkin_enabled }).toEqual({
      request: { goal_stalled_checkin_enabled: false }, savedQuiet: 60, liveQuiet: 60, visibleQuiet: '60', enabled: false,
    })
  })

  it('CG-CR-2 preserves newer local-password input when an older held PUT completes before its debounce', async () => {
    gate1Mode = 'local'
    const view = await mount()
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    gate1HoldPut = true
    await edit(timerControls().minutes, '45')
    expect(puts).toHaveLength(0)
    await gate1LocalConsent()
    await gate1PutStarted
    expect(gate1ReauthCount).toBe(1)
    expect(puts).toEqual([{ goal_stalled_checkin_minutes: 45 }])
    expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    const minutes = timerControls().minutes
    expect(minutes).toBeEnabled()
    const oldCompleted = new Promise<void>((resolve) => {
      const unsubscribe = view.client.getMutationCache().subscribe((event) => {
        if (event.type === 'updated' && event.action.type === 'success') { unsubscribe(); resolve() }
      })
    })
    vi.useFakeTimers()
    const beforeNewEdit = Date.now()
    fireEvent.change(minutes, { target: { value: '75' } })
    expect(minutes).toHaveValue(75)
    const release = gate1ReleasePut
    expect(release).not.toBeNull()
    if (release === null) throw new Error('BLOCKED: actual old PUT was not held at the HTTP edge')
    await act(async () => { release(); await oldCompleted; await vi.advanceTimersByTimeAsync(0) })
    expect(Date.now()).toBe(beforeNewEdit)
    expect(puts).toHaveLength(1)
    // A real accepted newer edit must survive A's completion before B queues.
    expect(timerControls().minutes).toHaveValue(75)
    await act(async () => { await vi.advanceTimersByTimeAsync(700) })
    vi.useRealTimers()
    expect(puts).toHaveLength(1)
    await gate1LocalConsent()
    await waitFor(() => expect(puts).toHaveLength(2))
    expect(puts[1]).toEqual({ goal_stalled_checkin_minutes: 75 })
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    expect(timerControls().minutes).toHaveValue(75)
    expect(server.goal_stalled_checkin_minutes).toBe(75)
  })
})


// Founder TM5: a newer valid edit settles during an older confirmed request;
// the queued intent must request fresh consent automatically after settlement.
describe('TM5 pending local timer edit resumes consent after an older save', () => {
  for (const olderFails of [false, true]) {
    it(`TM5 opens fresh re-auth for settled newer input after older ${olderFails ? 'error' : 'success'}`, async () => {
      gate1Mode = 'local'
      failRefresh = olderFails
      const view = await mount()
      await waitFor(() => expect(view.client.isFetching()).toBe(0))
      gate1HoldPut = true
      await edit(timerControls().minutes, '45')
      expect(puts).toHaveLength(0)
      await gate1LocalConsent()
      await gate1PutStarted
      expect(gate1ReauthCount).toBe(1)
      expect(puts).toEqual([{ goal_stalled_checkin_minutes: 45 }])
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
      const minutes = timerControls().minutes
      expect(minutes).toBeEnabled()
      const oldSettled = new Promise<void>((resolve) => {
        const unsubscribe = view.client.getMutationCache().subscribe((event) => {
          if (event.type === 'updated' && event.action.type === (olderFails ? 'error' : 'success')) { unsubscribe(); resolve() }
        })
      })
      vi.useFakeTimers()
      const beforeNewEdit = Date.now()
      fireEvent.change(minutes, { target: { value: '75' } })
      expect(minutes).toHaveValue(75)
      // The existing native helper uses a 700ms deterministic debounce flush.
      // Here it MUST finish while the actual first HTTP request is still held.
      await act(async () => { await vi.advanceTimersByTimeAsync(700) })
      expect(Date.now()).toBe(beforeNewEdit + 700)
      expect(timerControls().minutes).toHaveValue(75)
      expect(puts).toHaveLength(1)
      expect(view.client.isMutating()).toBe(1)
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
      const release = gate1ReleasePut
      expect(release).not.toBeNull()
      if (release === null) throw new Error('BLOCKED: actual older confirmed HTTP request is not held')
      await act(async () => { release(); await oldSettled; await vi.advanceTimersByTimeAsync(0) })
      expect(Date.now()).toBe(beforeNewEdit + 700)
      vi.useRealTimers()
      expect(puts).toHaveLength(1)
      // A correct automatic modal hides background controls from the role
      // tree; query the actual attached field with the same contract bounds.
      const liveMinutes = screen.getAllByRole('spinbutton', { hidden: true }).filter((input) => input.getAttribute('min') === '5' && input.getAttribute('max') === '1440')
      expect(liveMinutes).toHaveLength(1)
      expect(liveMinutes[0]).toBeInTheDocument()
      expect(liveMinutes[0]).toHaveValue(75)
      // No extra input gesture or edit is allowed to supply this prompt.
      const freshPrompt = await screen.findByTestId('reauth-password-input')
      expect(freshPrompt).toBeVisible()
      expect(puts).toHaveLength(1)
      expect(liveMinutes[0]).toBeInTheDocument()
      expect(liveMinutes[0]).toHaveValue(75)
      failRefresh = false
      await gate1LocalConsent()
      await waitFor(() => expect(puts).toHaveLength(2))
      expect(puts[1]).toEqual({ goal_stalled_checkin_minutes: 75 })
      await waitFor(() => expect(view.client.isFetching()).toBe(0))
      expect(timerControls().minutes).toHaveValue(75)
      expect(server.goal_stalled_checkin_minutes).toBe(75)
    })
  }
})
