// Order 48 / CHECK F19. S-44 and C-22 row 2: the retired switch is absent,
// and a Max parallel agents change survives confirmation, sends only the
// remaining field, stays unsaved while the reply is held, then shows Saved.
// Only the HTTP edge and debounce clock are controlled; real section,
// step-up dialog, mutation, HTTP validation and UI store stay in the chain.
// GREEN and mutation/proof-of-failability remain deferred to independent CHECK.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, PerformanceSettings, PerformanceSettingsUpdate } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { PerformanceSection } from './PerformanceSection'

// Post-removal read shape from S-44/C-22: no tools_on_demand. Distinct retained
// values let a follow-up GET show the acknowledged cap without changing peers.
const SETTINGS: PerformanceSettings = {
  max_parallel_agents: 4, effective_max_parallel_agents: 4, max_parallel_agents_configured: true,
  goal_max_rounds: 20, goal_reminder_quiet_seconds: 60,
  goal_stalled_checkin_enabled: true, goal_stalled_checkin_minutes: 30,
  max_tool_iterations: 200, max_tool_iterations_saved_state: 'ok',
}
const PLATFORM_APP_STATE: AppState = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
}

let client: QueryClient
let stored: PerformanceSettings
let writes: PerformanceSettingsUpdate[]
let releaseSave: (value: Response) => void
let pendingSave: Promise<Response>
let fetchEdge: ReturnType<typeof vi.fn<typeof fetch>>
const reply = (body: AppState | PerformanceSettings) => new Response(JSON.stringify(body), {
  status: 200, headers: { 'Content-Type': 'application/json' },
})

const tick = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
beforeEach(() => {
  vi.useFakeTimers()
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
  stored = { ...SETTINGS }
  writes = []
  pendingSave = new Promise<Response>((resolve) => { releaseSave = resolve })
  document.cookie = 'csrf=f19-limits; path=/'
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  fetchEdge = vi.fn<typeof fetch>(async (input, init) => {
    const path = new URL(input instanceof Request ? input.url : String(input), 'http://localhost').pathname
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    if (path === '/api/v1/state' && method === 'GET') return reply(PLATFORM_APP_STATE)
    if (path === '/api/v1/performance' && method === 'GET') return reply(stored)
    if (path === '/api/v1/performance' && method === 'PUT') {
      writes.push(JSON.parse(String(init?.body)) as PerformanceSettingsUpdate)
      return pendingSave
    }
    throw new Error(`fixture: unexpected ${method} ${path}`)
  })
  vi.stubGlobal('fetch', fetchEdge)
})
afterEach(async () => {
  // Do not strand the actual mutation if any assertion fails while it is held.
  releaseSave(reply(stored))
  await act(async () => { await pendingSave })
  cleanup()
  client.clear()
  // The existing success indicator schedules a 2s reset without an unmount
  // cancellation. Drain that real callback on the controlled external clock,
  // rather than leaving a real timer alive past this file's teardown.
  await tick(2000)
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})
async function renderSection() {
  const view = render(<QueryClientProvider client={client}><PerformanceSection /></QueryClientProvider>)
  await tick()
  expect(screen.getByLabelText('Max parallel agents')).toHaveValue(4)
  return view
}

describe('PerformanceSection — no tools-on-demand control after the removal (order 48)', () => {
  it('S-44: Limits loads the real retained control and shows no Load tools on demand switch', async () => {
    await renderSection()
    expect(screen.queryByText(/load tools on demand/i)).not.toBeInTheDocument()
  })

  it('S-44: confirms the exact retained-field save, waits for its ACK, then shows Saved and the confirmed value', async () => {
    await renderSection()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    // Existing Limits debounce is 600 ms; this is not the C-16 queue's timing.
    await tick(700)
    const dialog = screen.getByRole('alertdialog')
    expect(dialog).toHaveTextContent('Change the performance settings?')
    expect(writes).toEqual([])
    fireEvent.click(screen.getByRole('button', { name: 'Change performance settings' }))
    await tick()
    expect(writes).toHaveLength(1)
    // Soft only so the same real save can finish and independently prove the
    // ACK/success path even while today's retired extra field is still RED.
    expect.soft(writes).toEqual([{ max_parallel_agents: 8 }])
    expect(screen.queryByText('Saved')).not.toBeInTheDocument()
    stored = { ...SETTINGS, max_parallel_agents: 8, effective_max_parallel_agents: 8 }
    releaseSave(reply(stored))
    await tick()
    expect(screen.getByText('Saved')).toBeInTheDocument()
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue(8)
    expect(writes).toHaveLength(1)
    expect(useUiStore.getState().toasts.filter((toast) => toast.variant === 'error')).toEqual([])
  })
})
