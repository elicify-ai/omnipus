import { useEffect } from 'react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { EventClickArg, EventContentArg } from '@fullcalendar/core'
import type { FullCalendarViewProps } from './types'
import type { Task, TaskRun } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { CalendarScreen } from '@/components/screens/CalendarScreen'

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  Link: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
}))

// jsdom has no FullCalendar pointer geometry. Replace ONLY that layout boundary:
// production mapping, EventChip, screen click handler, panels, queries, schemas,
// run eligibility, history filtering, and HTTP client stay real. Tests click DOM
// chips rather than calling captured screen callbacks or supplying panel props.
vi.mock('@/components/calendar/FullCalendarView', async original => {
  const { EventChip } = await original<typeof import('./FullCalendarView')>()
  return { FullCalendarView: (props: FullCalendarViewProps) => {
    useEffect(() => { props.onDatesSet?.('T10 server range', 'timeGridWeek', new Date(0), new Date(4102444800000)) }, [props.onDatesSet])
    return <div data-testid="calendar-layout-boundary">{props.events.map(event => {
      const apiEvent = { ...event, start: new Date(String(event.start)), extendedProps: event.extendedProps }
      return <Button key={String(event.id)} aria-label={`Calendar chip ${event.title}`} onClick={click => props.onEventClick?.({ event: apiEvent, jsEvent: click.nativeEvent, el: click.currentTarget } as unknown as EventClickArg)}>
        <EventChip arg={{ event: apiEvent, timeText: '' } as unknown as EventContentArg} />
      </Button>
    })}</div>
  } }
})

const future = Date.now() + 48 * 60 * 60 * 1000
// Server-supplied 23-hour bucket. The screen must pass its exclusive end verbatim.
const start = Date.UTC(2031, 2, 9, 5)
const end = start + 23 * 60 * 60 * 1000
let kind: 'once' | 'bucket' = 'once'
let inside = true
const calls: { url: string; method: string; body: unknown }[] = []
const base: Task = {
  id: 't10-chip-task', title: 'T10 chip report', prompt: 'Write the complete chip report', agent_id: 'mia',
  action: 'llm', status: 'inbox', priority: 3, workspace_id: 'ws-1', owner: 'alice', created_by: 'alice',
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', surface: 'user',
  criteria: [{ kind: 'prose', judgment: 'boolean', text: 'Report exists', author: { kind: 'user', id: 'alice' }, status: 'pending' }],
  dod: [{ kind: 'prose', judgment: 'boolean', text: 'Report checked', author: { kind: 'user', id: 'alice' }, status: 'pending' }],
}
function task(): Task {
  return { ...base, trigger: kind === 'once' ? { type: 'once', config: { at_ms: future } } : { type: 'recurring', config: { rrule: 'FREQ=HOURLY;COUNT=30', dtstart_ms: start, tz: 'UTC' } } }
}
function run(id: string, at: number, result: string): TaskRun {
  return { run_id: id, task_id: base.id, occurrence_ms: at, status: 'done', session_id: `session-${id}`, kind: 'scheduled', started_at: new Date(at).toISOString(), ended_at: new Date(at + 1000).toISOString(), result }
}
const runs = [run('inside', start + 1000, 'Inside bucket full result'), run('end-exclusive', end, 'Outside exclusive end result'), run('before', start - 1000, 'Previous day result')]
function json(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }) }
function open() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><CalendarScreen workspaceId="ws-1" /></QueryClientProvider>)
}
beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  window.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})
beforeEach(() => {
  kind = 'once'; inside = true; calls.length = 0; navigate.mockReset()
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('csrf=t10-synthetic-chip-csrf')
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input), method = init?.method ?? 'GET'
    calls.push({ url, method, body: typeof init?.body === 'string' ? JSON.parse(init.body) : {} })
    const path = new URL(url, 'http://localhost').pathname
    if (path.endsWith('/tasks/occurrences')) return json([{ task_id: base.id, title: base.title, occurrences_ms: [], day_buckets: kind === 'bucket' ? [{ day_start_ms: start, day_end_ms: end, count: 23, first_ms: start, interval_ms: 3600000 }] : [], truncated: false, armed_occurrence_ms: kind === 'once' ? future : inside ? start + 3600000 : end }])
    if (path === '/api/v1/tasks' && method === 'GET') return json(url.includes('parent_task_id') ? [] : [task()])
    if (path.endsWith(`/tasks/${base.id}/runs`)) return method === 'POST' ? new Response(null, { status: 202 }) : json(runs)
    if (path.endsWith(`/tasks/${base.id}`) && method === 'GET') return json(task())
    if (path.endsWith('/agents')) return json([{ revision: '0'.repeat(64), id: 'mia', name: 'Mia', type: 'core', figure: 'Omnipus', role: 'general', status: 'idle', soul: '', locked: true, max_tool_iterations: 40, max_tool_iterations_source: 'global', max_tool_iterations_override_ignored: false, needs_model: false }])
    if (path.endsWith('/workspaces/ws-1/delegation')) return json({ core_team: ['mia'], edges: [] })
    if (path.endsWith('/plans') || path.endsWith('/workspaces') || path.endsWith('/evidence') || path.endsWith('/verdicts') || path.endsWith('/subtasks')) return json([])
    throw new Error(`Unexpected chip HTTP request: ${method} ${url}`)
  }))
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('T10 real CalendarScreen chip entry (S-H.13/14, D-6.15, D-11.2/7)', () => {
  it('one-shot chip retains the real detail, result/history and chat entry', async () => {
    open()
    fireEvent.click(await screen.findByRole('button', { name: 'Calendar chip T10 chip report' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getAllByText(base.title).length).toBeGreaterThan(0)
    expect(within(dialog).getByText(base.prompt!)).toBeVisible()
    expect(await within(dialog).findByText('Inside bucket full result')).toBeVisible()
    expect(within(dialog).getByText('Run history')).toBeVisible()
    fireEvent.click(within(within(dialog).getAllByTestId('task-run-row')[0]).getByRole('button', { name: 'Open in Chat' }))
    expect(navigate).toHaveBeenCalledWith({ to: '/sessions/$sessionId', params: { sessionId: 'session-inside' } })
  })
  it('one-shot chip uses Run now POST without occurrence or PATCH activation', async () => {
    open()
    fireEvent.click(await screen.findByRole('button', { name: 'Calendar chip T10 chip report' }))
    const dialog = await screen.findByRole('dialog')
    expect.soft(within(dialog).queryByRole('button', { name: 'Start Task' })).not.toBeInTheDocument()
    fireEvent.click(await within(dialog).findByRole('button', { name: /^Run now$/i }))
    await waitFor(() => expect(calls.filter(c => c.method === 'POST')).toEqual([{ url: `/api/v1/tasks/${base.id}/runs`, method: 'POST', body: {} }]))
    expect(calls.filter(c => c.method === 'PATCH')).toEqual([])
  })
  it.each([true, false])('bucket chip keeps the server day-scoped history and exact action (inside=%s)', async inDay => {
    kind = 'bucket'; inside = inDay
    open()
    const chip = await screen.findByRole('button', { name: /Calendar chip T10 chip report/ })
    fireEvent.click(chip)
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByDisplayValue(base.title)).toBeVisible()
    expect(within(dialog).getByDisplayValue(base.prompt!)).toBeVisible()
    expect(within(dialog).getByRole('button', { name: 'Save' })).toBeVisible()
    expect(await within(dialog).findByText('Inside bucket full result')).toBeVisible()
    expect(within(dialog).getAllByTestId('task-run-row')).toHaveLength(1)
    expect(within(dialog).queryByText('Outside exclusive end result')).not.toBeInTheDocument()
    expect(within(dialog).queryByText('Previous day result')).not.toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Open in Chat' })).toBeVisible()
    if (inDay) {
      const actions = await within(dialog).findAllByRole('button', { name: 'Run next armed occurrence' })
      expect(actions).toHaveLength(1)
      fireEvent.click(actions[0])
      await waitFor(() => expect(calls.filter(c => c.method === 'POST')).toEqual([{ url: `/api/v1/tasks/${base.id}/runs`, method: 'POST', body: {} }]))
    } else {
      expect(within(dialog).queryByRole('button', { name: 'Run next armed occurrence' })).not.toBeInTheDocument()
      expect(calls.filter(c => c.method === 'POST')).toEqual([])
    }
    expect(calls.filter(c => c.method === 'PATCH')).toEqual([])
  })
})
