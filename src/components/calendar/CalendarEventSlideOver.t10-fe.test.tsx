// T10 frontend scenarios not covered by the RED pack (schedules-t10-spec-r6.md):
// the armed read when the clicked occurrence or bucket lies outside the
// slide-over's 30-day preview window, the bucket action's truthful refusal
// (S-H.15 screen side, server refusal mocked at the transport) and the
// overdue-but-owned armed instant (E1 past). Transport is mocked; the real
// hook, generated schema and request helpers run.
import { beforeAll, beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CalendarEventSlideOver } from './CalendarEventSlideOver'
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

const day = 24 * 60 * 60 * 1000
const far = Date.now() + 45 * day // outside the 30-day preview window
const calls: { url: string; method: string; body: unknown }[] = []
let armed: number | null = far
let runStatus = 202
let runError = ''
const task: Task = {
  id: 't10-fe-task', title: 'T10 monthly report', prompt: 'Produce the monthly report', agent_id: 'mia',
  action: 'llm', status: 'inbox', priority: 3, workspace_id: 'ws-1', owner: 'alice', created_by: 'alice',
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', surface: 'user',
  criteria: [{ kind: 'prose', judgment: 'boolean', text: 'Report exists', author: { kind: 'user', id: 'alice' }, status: 'pending' }],
  dod: [{ kind: 'prose', judgment: 'boolean', text: 'Report checked', author: { kind: 'user', id: 'alice' }, status: 'pending' }],
  trigger: { type: 'recurring', config: { rrule: 'FREQ=MONTHLY;COUNT=5', dtstart_ms: far, tz: 'UTC' } },
}
function json(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }) }
function open(occurrence: number | null, bucket?: { startMs: number; endMs: number }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><CalendarEventSlideOver open onOpenChange={vi.fn()} workspaceId="ws-1" task={task} selectedOccurrenceMs={occurrence} selectedBucketDayRange={bucket} /></QueryClientProvider>)
}
const occurrenceReads = () => calls.filter(c => c.url.includes('/tasks/occurrences'))
const posts = () => calls.filter(c => c.method === 'POST')

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  window.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})
beforeEach(() => {
  calls.length = 0
  armed = far
  runStatus = 202
  runError = ''
  toast.mockClear()
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=t10-fe-synthetic-csrf')
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    calls.push({ url, method, body: typeof init?.body === 'string' ? JSON.parse(init.body) : null })
    if (url.includes('/tasks/occurrences')) {
      // Mirrors the server: a task's set is returned only when it has an
      // occurrence inside [from_ms, to_ms); armed_occurrence_ms rides the set.
      const q = new URL(url, 'http://localhost').searchParams
      const from = Number(q.get('from_ms')), to = Number(q.get('to_ms'))
      const occ = [far].filter(ms => ms >= from && ms < to)
      return json(occ.length ? [{ task_id: task.id, title: task.title, occurrences_ms: occ, day_buckets: [], truncated: false, armed_occurrence_ms: armed }] : [])
    }
    if (url.endsWith('/runs') && method === 'POST') {
      return runStatus === 202 ? new Response(null, { status: 202 }) : json({ error: runError }, runStatus)
    }
    throw new Error(`Unexpected HTTP request: ${method} ${url}`)
  }))
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('T10 armed read beyond the preview window (S-H.12, E1)', () => {
  it('offers Run now on an armed occurrence 45 days out and posts that occurrence', async () => {
    open(far)
    fireEvent.click(await screen.findByRole('button', { name: /^Run now$/i }))
    await waitFor(() => expect(posts()).toEqual([{ url: `/api/v1/tasks/${task.id}/runs`, method: 'POST', body: { occurrence_ms: far } }]))
    expect(calls.some(c => c.method === 'PATCH')).toBe(false)
  })

  it('keeps Run now hidden on the same far instant when the task is de-armed', async () => {
    armed = null
    open(far)
    await screen.findByText('Scheduled.')
    await waitFor(() => expect(occurrenceReads().some(c => Number(new URL(c.url, 'http://localhost').searchParams.get('to_ms')) > far)).toBe(true))
    expect(screen.queryByRole('button', { name: /^Run now$/i })).not.toBeInTheDocument()
  })

  it('offers the bucket action for a far bucket containing the armed instant', async () => {
    open(null, { startMs: far - 1000, endMs: far - 1000 + day })
    fireEvent.click(await screen.findByRole('button', { name: 'Run next armed occurrence' }))
    await waitFor(() => expect(posts()).toEqual([{ url: `/api/v1/tasks/${task.id}/runs`, method: 'POST', body: {} }]))
  })

  it('treats the bucket end as exclusive: an armed instant at endMs gets no action', async () => {
    open(null, { startMs: far - day, endMs: far })
    await waitFor(() => expect(occurrenceReads().length).toBeGreaterThan(0))
    await screen.findByText(/run history|no runs/i)
    expect(screen.queryByRole('button', { name: 'Run next armed occurrence' })).not.toBeInTheDocument()
  })
})

describe('T10 bucket action refusal (S-H.15 screen side; server refusal mocked)', () => {
  it('shows the server reason and re-reads occurrence truth after a refusal', async () => {
    runStatus = 409
    runError = 'An automatic run of this task is already queued; it will run once.'
    open(null, { startMs: far - 1000, endMs: far - 1000 + day })
    const action = await screen.findByRole('button', { name: 'Run next armed occurrence' })
    const before = occurrenceReads().length
    fireEvent.click(action)
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: runError, variant: 'error' }))
    await waitFor(() => expect(occurrenceReads().length).toBeGreaterThan(before))
  })
})
