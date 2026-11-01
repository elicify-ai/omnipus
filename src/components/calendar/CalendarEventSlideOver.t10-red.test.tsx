import { beforeAll, beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CalendarEventSlideOver } from './CalendarEventSlideOver'
import { TaskOccurrenceSet as OccurrenceSchema } from '@/lib/api/generated/schemas'
import type { Task } from '@/lib/api'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/lib/api', async (original) => {
  const actual = await original<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core', status: 'idle', soul: '', locked: true }]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('unscoped test roster')),
    fetchTaskRuns: vi.fn().mockResolvedValue([]),
    setTaskTodos: vi.fn().mockResolvedValue({}),
  }
})
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/store/ui', () => ({
  useUiStore: (select?: (state: { addToast: typeof toast }) => unknown) => select ? select({ addToast: toast }) : { addToast: toast },
}))

const future = Date.now() + 48 * 60 * 60 * 1000
const past = Date.now() - 48 * 60 * 60 * 1000
const day = 24 * 60 * 60 * 1000
const calls: { url: string; method: string; body: unknown }[] = []
let armed: number | null = future
let refuseRun = false
const task: Task = {
  id: 't10-calendar-task', title: 'T10 report', prompt: 'Produce the full report', agent_id: 'mia',
  action: 'llm', status: 'inbox', priority: 3, workspace_id: 'ws-1', owner: 'alice', created_by: 'alice',
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', surface: 'user',
  criteria: [{ kind: 'prose', judgment: 'boolean', text: 'Report exists', author: { kind: 'user', id: 'alice' }, status: 'pending' }],
  dod: [{ kind: 'prose', judgment: 'boolean', text: 'Report checked', author: { kind: 'user', id: 'alice' }, status: 'pending' }],
  trigger: { type: 'recurring', config: { rrule: 'FREQ=DAILY;COUNT=5', dtstart_ms: future, tz: 'UTC' } },
}
function occurrenceSet() {
  return { task_id: task.id, title: task.title, occurrences_ms: [past, future, future + day], day_buckets: [], truncated: false, armed_occurrence_ms: armed }
}
function open(occurrence: number | null, bucket?: { startMs: number; endMs: number }, overrides: Partial<Task> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><CalendarEventSlideOver open onOpenChange={vi.fn()} workspaceId="ws-1" task={{ ...task, ...overrides }} selectedOccurrenceMs={occurrence} selectedBucketDayRange={bucket} /></QueryClientProvider>)
  return client
}

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  window.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})
beforeEach(() => {
  calls.length = 0
  armed = future
  refuseRun = false
  toast.mockClear()
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=t10-synthetic-csrf-fixture')
  // Mock transport, NOT useOccurrences or an eligibility prop: the actual hook,
  // generated schema, query and production runTaskNow request all execute.
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    calls.push({ url, method, body: typeof init?.body === 'string' ? JSON.parse(init.body) : null })
    if (url.includes('/tasks/occurrences')) return new Response(JSON.stringify([occurrenceSet()]), { status: 200, headers: { 'Content-Type': 'application/json' } })
    if (url.endsWith(`/tasks/${task.id}`) && method === 'PATCH') {
      const body = JSON.parse(String(init?.body))
      return new Response(JSON.stringify({ ...task, ...body }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    if (url.endsWith('/runs') && method === 'POST') {
      if (refuseRun) return new Response(JSON.stringify({ error: 'This occurrence is no longer armed' }), { status: 409, headers: { 'Content-Type': 'application/json' } })
      armed = future + day
      return new Response(JSON.stringify({ session_id: 'real-transport-fixture-session' }), { status: 202, headers: { 'Content-Type': 'application/json' } })
    }
    throw new Error(`Unexpected HTTP request: ${method} ${url}`)
  }))
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('T10 #1243 editor save request (transport characterization, not backend proof)', () => {
  // Traces to: schedules-t10-spec-r6.md lines 1066–1076, 1273–1280; TDD 24, D-9.1–4.
  it.each([
    { ends: 'After N', running: true }, { ends: 'After N', running: false },
    { ends: 'On date', running: true }, { ends: 'On date', running: false },
    { ends: 'Does not repeat', running: true }, { ends: 'Does not repeat', running: false },
  ])('$ends save while running=$running sends the actual editable trigger', async ({ ends, running }) => {
    const rrule = ends === 'On date' ? 'FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE;UNTIL=20291231T235959Z' : 'FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE;COUNT=3'
    open(null, undefined, { status: running ? 'in_progress' : 'inbox', trigger: { type: 'recurring', config: { rrule, dtstart_ms: future, tz: 'UTC' } } })
    await screen.findByLabelText(/title/i)
    if (ends === 'After N') {
      fireEvent.click(await screen.findByRole('button', { name: /^After$/ }))
      fireEvent.change(screen.getByLabelText('Number of occurrences'), { target: { value: '7' } })
    } else if (ends === 'Does not repeat') {
      fireEvent.click(screen.getByRole('combobox', { name: 'Repeat' }))
      fireEvent.click(await screen.findByRole('option', { name: /^Does not repeat$/ }))
    }
    const save = screen.getByRole('button', { name: /^Save$/i })
    await waitFor(() => expect(save).not.toBeDisabled())
    fireEvent.click(save)
    await waitFor(() => expect(calls.filter(c => c.method === 'PATCH')).toHaveLength(1))
    const body = calls.find(c => c.method === 'PATCH')!.body as { trigger: { type: string; config: { rrule?: string; at_ms?: number } }; status?: string }
    expect(body.status).toBeUndefined()
    if (ends === 'After N') expect(body.trigger.config.rrule).toContain('COUNT=7')
    if (ends === 'On date') expect(body.trigger.config.rrule).toBe(rrule)
    if (ends === 'Does not repeat') { expect(body.trigger.type).toBe('once'); expect(body.trigger.config.at_ms).toBeGreaterThan(0) }
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'success', message: 'Event updated' })))
    expect(screen.queryByText(/stale view|reload.*view|view.*out.of.date/i)).not.toBeInTheDocument()
  })
})

describe('T10 authoritative Calendar armed occurrence', () => {
  // Traces to: schedules-t10-spec-r6.md lines 950–957, 1301; TDD 25, D-11.6.
  it.each([future, null])('generated runtime schema preserves armed instant %s', value => {
    armed = value
    const parsed = OccurrenceSchema.parse(occurrenceSet())
    expect(parsed).toHaveProperty('armed_occurrence_ms', value)
  })

  // Traces to: schedules-t10-spec-r6.md lines 950–957, 1296, 1303; D-11.1/4/8.
  it.each([
    { name: 'E1 future armed', selected: future, live: future, offered: true },
    { name: 'de-armed twin, identical recurrence/history', selected: future, live: null, offered: false },
    { name: 'E2 settled past without run', selected: past, live: future, offered: true },
    { name: 'E3 other future, not nearest match', selected: future + day, live: future, offered: false },
  ])('$name follows server truth, not local recurrence', async ({ selected, live, offered }) => {
    armed = live
    open(selected)
    await waitFor(() => expect(calls.some(c => c.url.includes('/tasks/occurrences'))).toBe(true))
    if (offered) {
      fireEvent.click(await screen.findByRole('button', { name: /^Run now$/i }))
      await waitFor(() => expect(calls.filter(c => c.method === 'POST')).toEqual([{ url: `/api/v1/tasks/${task.id}/runs`, method: 'POST', body: { occurrence_ms: selected } }]))
      expect(calls.some(c => c.method === 'PATCH')).toBe(false)
    } else {
      await screen.findByText('Scheduled.')
      expect(screen.queryByRole('button', { name: /^Run now$/i })).not.toBeInTheDocument()
      expect(calls.some(c => c.method !== 'GET')).toBe(false)
    }
  })

  // Traces to: schedules-t10-spec-r6.md lines 967–974, 1297; TDD 21/25, D-11.2.
  it.each([true, false])('bucket action exists exactly when the armed instant is inside its day: %s', async inside => {
    const start = future - 1000
    armed = inside ? future : future + 2 * day
    open(null, { startMs: start, endMs: start + day })
    await waitFor(() => expect(calls.some(c => c.url.includes('/tasks/occurrences'))).toBe(true))
    expect(screen.queryByRole('button', { name: /^Run now$/i })).not.toBeInTheDocument()
    if (inside) {
      const actions = await screen.findAllByRole('button', { name: 'Run next armed occurrence' })
      expect(actions).toHaveLength(1)
      fireEvent.click(actions[0])
      await waitFor(() => expect(calls.filter(c => c.method === 'POST')).toEqual([{ url: `/api/v1/tasks/${task.id}/runs`, method: 'POST', body: {} }]))
    } else {
      expect(screen.queryByRole('button', { name: 'Run next armed occurrence' })).not.toBeInTheDocument()
    }
  })

  // Traces to: schedules-t10-spec-r6.md lines 950–957, 1300; TDD 25, D-11.5.
  it('re-reads occurrence truth after a stale click is refused, not only after success', async () => {
    // A past occurrence is actionable even on the base, so this exercises the
    // actual refusal refresh rather than stopping at the missing E1 control.
    open(past)
    const button = await screen.findByRole('button', { name: /^Run now$/i })
    await waitFor(() => expect(calls.some(c => c.url.includes('/tasks/occurrences'))).toBe(true))
    const before = calls.filter(c => c.url.includes('/tasks/occurrences')).length
    armed = null
    refuseRun = true
    fireEvent.click(button)
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'error', message: expect.stringMatching(/no longer armed|conflicts with the current state/i) })))
    await waitFor(() => expect(calls.filter(c => c.url.includes('/tasks/occurrences')).length).toBeGreaterThan(before))
  })
})
