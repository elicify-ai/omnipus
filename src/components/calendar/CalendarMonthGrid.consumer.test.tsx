/**
 * Real Calendar panel Month interactions — PANEL-CALENDAR-MONTH-SUCCESSOR-1849.
 * Oracles derived before execution:
 * - Side-panel shell spec, §10 Wave 3 / SP-39: Week default; Day/Week/Month
 *   stay in-panel, no Agenda. This mounts the actual registered content,
 *   CalendarScreen, with real FullCalendar, toolbar, Month grid and editor.
 * - Calendar recurrence redesign, US-1 / asserted-defaults BDD: a real day
 *   click supplies that date at 09:00; Does not repeat saves a once trigger.
 * - Calendar user guide, What it is / How to schedule: at most THREE visible
 *   status dots per date, first-three hover titles, +N more after the cap.
 * - Design-system definition D4 (2026-09-22 amendment): the current canonical
 *   six-state palette supersedes the old FullCalendar spec's colour table.
 * - Task / TaskCreateRequest contracts: POST stores a task in inbox; GET
 *   reads the stored task. The fake HTTP edge stores the ACTUAL submitted
 *   payload, never a precomputed expected date or a fabricated UI event.
 * Boundary: only Date and fetch are replaced. No component, callback, hook,
 * API helper, mapping function, store or calendar engine is mocked/spied.
 * Limit: jsdom establishes DOM visibility, not pixel layout; persistence is
 * simulated at HTTP, not a real gateway or scheduled-agent execution.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CalendarScreen } from '@/components/screens/CalendarScreen'
import { fetchTask, type Task, type WorkspaceDelegation } from '@/lib/api'
import { Task as TaskSchema, TaskCreateRequest as TaskCreateSchema } from '@/lib/api/generated/schemas'
import { makeAgent } from '@/test/factories'

const WORKSPACE = 'month-consumer-workspace'
const CREATED_ID = 'month-consumer-created'
// July 14 is deliberately NOT the clicked date and is outside July 20's Week.
const NOW = new Date('2026-07-14T12:00:00Z')
const CLICKED_INSTANT = Date.parse('2026-07-20T09:00:00Z')
const CREATED_TITLE = 'Month click appointment'
const clients: QueryClient[] = []
const unexpectedRequests: string[] = []

// Exact current D4 hexes expressed in browser-normalized RGB, NOT read from
// STATUS_STYLE, eventMapping, statusContract, or rendered implementation output.
const STATUS_CASES: { status: Task['status']; day: string; rgb: string }[] = [
  { status: 'inbox', day: '2026-07-06', rgb: 'rgb(156, 163, 175)' },
  { status: 'next', day: '2026-07-07', rgb: 'rgb(96, 165, 250)' },
  { status: 'in_progress', day: '2026-07-08', rgb: 'rgb(212, 175, 55)' },
  { status: 'blocked', day: '2026-07-09', rgb: 'rgb(249, 115, 22)' },
  { status: 'done', day: '2026-07-10', rgb: 'rgb(16, 185, 129)' },
  { status: 'failed', day: '2026-07-11', rgb: 'rgb(248, 113, 113)' },
]

function seedTask(id: string, overrides: Partial<Task> = {}): Task {
  const task: Task = {
    id, title: id, action: 'llm', status: 'inbox', priority: 3,
    workspace_id: WORKSPACE, surface: 'user', owner: 'calendar-test',
    created_by: 'calendar-test', created_at: NOW.toISOString(), updated_at: NOW.toISOString(),
    ...overrides,
  }
  TaskSchema.parse(task)
  return task
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function installHttpEdge(seed: Task[] = [], refuseCreate = false) {
  // not-wire-format: local fake-server storage; every wire payload is checked
  // with the generated schema, rather than a parallel hand-written wire type.
  const records = new Map<string, unknown>(seed.map((task) => [task.id, structuredClone(task)]))
  const creates: ReturnType<typeof TaskCreateSchema.parse>[] = []
  const detailReads: string[] = []
  const delegation: WorkspaceDelegation = {
    revision: '0'.repeat(64), workspace_id: WORKSPACE, edges: [], default_depth: 3, team: ['mia'],
  }
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const url = new URL(raw, 'http://calendar.test')
    const method = init?.method ?? 'GET'
    if (method === 'GET' && url.pathname === '/api/v1/agents') {
      return jsonResponse([makeAgent({ id: 'mia', name: 'Mia' })])
    }
    if (method === 'GET' && url.pathname === `/api/v1/workspaces/${WORKSPACE}/delegation`) {
      return jsonResponse(delegation)
    }
    if (method === 'GET' && url.pathname === '/api/v1/tasks/occurrences') {
      // No repeating triggers are seeded in this narrow test; the real hook
      // still makes/validates its own HTTP request and the real mapper runs.
      return jsonResponse([])
    }
    if (method === 'GET' && url.pathname === '/api/v1/tasks') {
      const tasks = Array.from(records.values()).map((row) => TaskSchema.parse(row))
      return jsonResponse(tasks.filter((task) => task.workspace_id === url.searchParams.get('workspace_id')))
    }
    if (method === 'POST' && url.pathname === '/api/v1/tasks') {
      const body = TaskCreateSchema.parse(JSON.parse(String(init?.body)))
      creates.push(structuredClone(body))
      if (refuseCreate) return jsonResponse({ error: 'Calendar test gateway refused creation.' }, 400)
      // The response defaults come from TaskCreateRequest's inbox landing rule.
      // Date/trigger/criteria are copied from the received POST, not this test's
      // expected CLICKED_INSTANT. A bad prefill therefore persists a bad date.
      const stored = TaskSchema.parse({
        ...body, id: CREATED_ID, status: 'inbox', owner: 'calendar-test', created_by: 'calendar-test',
        created_at: NOW.toISOString(), updated_at: NOW.toISOString(),
        criteria: body.criteria?.map((item) => ({ ...item, kind: item.kind ?? 'prose', judgment: item.judgment ?? 'boolean' })),
        dod: body.dod?.map((item) => ({ ...item, kind: item.kind ?? 'prose', judgment: item.judgment ?? 'boolean' })),
      })
      records.set(stored.id, structuredClone(stored))
      return jsonResponse(stored, 201)
    }
    if (method === 'GET' && url.pathname === `/api/v1/tasks/${CREATED_ID}`) {
      detailReads.push(url.pathname)
      const stored = records.get(CREATED_ID)
      return stored ? jsonResponse(structuredClone(stored)) : jsonResponse({ error: 'Task not found' }, 404)
    }
    unexpectedRequests.push(`${method} ${url.pathname}`)
    return jsonResponse({ error: `Unexpected Calendar test request: ${method} ${url.pathname}` }, 501)
  }))
  return { creates, detailReads, records }
}

function mountCalendar() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  return render(<QueryClientProvider client={client}><CalendarScreen workspaceId={WORKSPACE} /></QueryClientProvider>)
}

async function selectMonth() {
  fireEvent.click(await screen.findByRole('button', { name: /^Month$/ }))
  return screen.findByTestId('calendar-month-grid')
}

function monthDay(day: string): HTMLElement {
  return within(screen.getByTestId('calendar-month-grid')).getByTestId(`calendar-month-day-${day}`)
}

function statusDots(day: string): HTMLElement[] {
  // Select the existing real visual marker shape, not a new production ID.
  return Array.from(monthDay(day).querySelectorAll<HTMLElement>('span.rounded-full'))
}

async function openDay(day = '2026-07-20') {
  fireEvent.click(monthDay(day))
  return screen.findByRole('dialog', { name: 'New event' })
}

function addCriterion(label: RegExp, text: string) {
  const input = screen.getByLabelText(label)
  fireEvent.change(input, { target: { value: text } })
  const draft = input.parentElement!
  fireEvent.click(within(draft).getByRole('button', { name: /^Add criterion$/ }))
}

async function fillRequiredForm() {
  fireEvent.change(screen.getByRole('textbox', { name: /^Title/ }), { target: { value: CREATED_TITLE } })
  const agent = screen.getByRole('combobox', { name: /^Agent$/ })
  await waitFor(() => expect(agent).toBeEnabled())
  fireEvent.click(agent)
  const mia = await screen.findByRole('option', { name: /^Mia$/ })
  fireEvent.pointerDown(mia, { pointerId: 1, button: 0 })
  fireEvent.click(mia)
  fireEvent.change(screen.getByRole('textbox', { name: /^Instruction/ }), { target: { value: 'Prepare the selected-day report.' } })
  addCriterion(/what must be true when this is done\?/i, 'The selected-day report is ready.')
  addCriterion(/definition of done item/i, 'The report is reviewed.')
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
  unexpectedRequests.length = 0
  // Synthetic CSRF cookie in this isolated jsdom, not a real user session.
  document.cookie = 'csrf=calendar-consumer-test; path=/'
  if (!Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
})

afterEach(async () => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  document.cookie = 'csrf=; Max-Age=0; path=/'
  vi.useRealTimers()
  vi.unstubAllGlobals()
  expect(unexpectedRequests, 'No unlisted network request may hide behind an empty fake response').toEqual([])
})

describe('real Calendar panel Month consumer', () => {
  it('defaults to Week, exposes only Day/Week/Month, and changing view alone never creates an event', async () => {
    const edge = installHttpEdge()
    const mounted = mountCalendar()
    const views = await screen.findByRole('group', { name: 'Calendar view' })
    expect(within(views).getAllByRole('button').map((button) => button.getAttribute('aria-label')).sort())
      .toEqual(['Day', 'Month', 'Week'])
    expect(within(views).getByRole('button', { name: /^Week$/ })).toHaveAttribute('aria-pressed', 'true')
    expect(mounted.container.querySelector('.fc-timeGridWeek-view')).toBeInTheDocument()
    expect(screen.queryByTestId('calendar-month-grid')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'New event' })).not.toBeInTheDocument()
    const grid = await selectMonth()
    expect(grid).toBeVisible()
    expect(screen.queryByRole('dialog', { name: 'New event' })).not.toBeInTheDocument()
    expect(edge.creates).toEqual([])
  })

  it.each([
    ['2026-07-01', 'Jul 1, 2026, 09:00 AM'],
    ['2026-07-20', 'Jul 20, 2026, 09:00 AM'],
    ['2026-07-31', 'Jul 31, 2026, 09:00 AM'],
  ])('clicking the real Month cell %s opens creation at that date and 09:00', async (day, expectedDisplay) => {
    const edge = installHttpEdge()
    mountCalendar()
    await selectMonth()
    const cell = monthDay(day)
    expect(cell).toBeVisible()
    expect(statusDots(day)).toHaveLength(0)
    expect(screen.queryByRole('dialog', { name: 'New event' })).not.toBeInTheDocument()
    const dialog = await openDay(day)
    expect(dialog).toBeVisible()
    const picker = within(dialog).getByRole('button', { name: /^Date and time$/ })
    expect(picker.textContent?.replace(/\s+/g, ' ').trim()).toBe(expectedDisplay)
    expect(within(dialog).getByRole('combobox', { name: /^Repeat$/ })).toHaveTextContent('Does not repeat')
    expect(within(dialog).getByTestId('recurrence-time-label')).toHaveTextContent('UTC')
    fireEvent.click(picker)
    expect(screen.getByRole('combobox', { name: /^Hour$/ })).toHaveTextContent('09')
    expect(screen.getByRole('combobox', { name: /^Minute$/ })).toHaveTextContent('00')
    expect(edge.creates).toEqual([])
  })

  it('saves a real Month date click at 09:00, independently reads the stored record, and renders it after a fresh mount', async () => {
    const edge = installHttpEdge()
    const mounted = mountCalendar()
    await selectMonth()
    await openDay('2026-07-20')
    expect(screen.getByRole('button', { name: /^Date and time$/ }).textContent?.replace(/\s+/g, ' ').trim())
      .toBe('Jul 20, 2026, 09:00 AM')
    await fillRequiredForm()
    const create = screen.getByRole('button', { name: /^Create$/ })
    expect(create).toBeEnabled()
    fireEvent.click(create)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'New event' })).not.toBeInTheDocument())
    expect(edge.creates).toHaveLength(1)
    expect(edge.creates[0].trigger).toEqual({ type: 'once', config: { at_ms: CLICKED_INSTANT } })
    expect(edge.creates[0].workspace_id).toBe(WORKSPACE)
    expect(edge.creates[0].title).toBe(CREATED_TITLE)
    // Fresh GET uses the REAL API helper and generated response validation,
    // independent of the mutation response and the Calendar's query cache.
    const saved = await fetchTask(CREATED_ID)
    expect(edge.detailReads).toEqual([`/api/v1/tasks/${CREATED_ID}`])
    expect({ id: saved.id, title: saved.title, status: saved.status, trigger: saved.trigger,
      workspace: saved.workspace_id, agent: saved.agent_id, priority: saved.priority, surface: saved.surface })
      .toEqual({ id: CREATED_ID, title: CREATED_TITLE, status: 'inbox', trigger: { type: 'once', config: { at_ms: CLICKED_INSTANT } },
        workspace: WORKSPACE, agent: 'mia', priority: 3, surface: 'user' })
    expect(saved.criteria?.map((item) => item.text)).toEqual(['The selected-day report is ready.'])
    expect(saved.dod?.map((item) => item.text)).toEqual(['The report is reviewed.'])
    mounted.unmount()
    mountCalendar() // New QueryClient; no data carried over from the first mount.
    await selectMonth()
    await waitFor(() => expect(statusDots('2026-07-20')).toHaveLength(1))
    expect(statusDots('2026-07-20')[0]).toBeVisible()
    expect(statusDots('2026-07-20')[0].style.backgroundColor).toBe('rgb(156, 163, 175)')
    expect(monthDay('2026-07-20')).toHaveAttribute('title', '7/20/2026 — Month click appointment')
    expect(statusDots('2026-07-19')).toHaveLength(0)
    expect(statusDots('2026-07-21')).toHaveLength(0)
    expect(edge.creates).toHaveLength(1)
  })

  it('cancelling a Month edge date writes nothing and reselecting the other edge replaces the prefill', async () => {
    const edge = installHttpEdge()
    mountCalendar()
    await selectMonth()
    await openDay('2026-06-29') // Monday before July; visible in the real six-week range.
    expect(screen.getByRole('button', { name: /^Date and time$/ }).textContent?.replace(/\s+/g, ' ').trim())
      .toBe('Jun 29, 2026, 09:00 AM')
    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'New event' })).not.toBeInTheDocument())
    await openDay('2026-08-09') // Sunday after July; last cell of this six-week range.
    expect(screen.getByRole('button', { name: /^Date and time$/ }).textContent?.replace(/\s+/g, ' ').trim())
      .toBe('Aug 9, 2026, 09:00 AM')
    expect(edge.creates).toEqual([])
    expect(edge.records.size).toBe(0)
  })

  it('a real date click with incomplete required fields cannot save or fabricate an event dot', async () => {
    const edge = installHttpEdge()
    mountCalendar()
    await selectMonth()
    await openDay()
    fireEvent.change(screen.getByRole('textbox', { name: /^Title/ }), { target: { value: CREATED_TITLE } })
    const create = screen.getByRole('button', { name: /^Create$/ })
    expect(create).toBeDisabled()
    fireEvent.click(create)
    expect(edge.creates).toEqual([])
    expect(edge.records.size).toBe(0)
    expect(screen.getByRole('dialog', { name: 'New event' })).toBeVisible()
    expect(screen.getByRole('button', { name: /^Date and time$/ }).textContent?.replace(/\s+/g, ' ').trim())
      .toBe('Jul 20, 2026, 09:00 AM')
    expect(statusDots('2026-07-20')).toHaveLength(0)
  })

  it('a refused Month-click save retains its selected date and inline error, with no stored task or false event dot', async () => {
    const edge = installHttpEdge([], true)
    mountCalendar()
    await selectMonth()
    await openDay()
    await fillRequiredForm()
    fireEvent.click(screen.getByRole('button', { name: /^Create$/ }))
    expect(await screen.findByText('Calendar test gateway refused creation.', { exact: true })).toBeVisible()
    expect(screen.getByRole('dialog', { name: 'New event' })).toBeVisible()
    expect(screen.getByRole('button', { name: /^Date and time$/ }).textContent?.replace(/\s+/g, ' ').trim())
      .toBe('Jul 20, 2026, 09:00 AM')
    expect(edge.creates).toHaveLength(1)
    expect(edge.creates[0].trigger).toEqual({ type: 'once', config: { at_ms: CLICKED_INSTANT } })
    expect(edge.records.size).toBe(0)
    expect(statusDots('2026-07-20')).toHaveLength(0)
  })

  it('renders visible status dots on every seeded event day with the canonical status colour and none on adjacent empty days', async () => {
    installHttpEdge(STATUS_CASES.map(({ status, day }) => seedTask(`Status ${status}`, { status, due: `${day}T09:00:00Z` })))
    mountCalendar()
    const grid = await selectMonth()
    for (const { status, day, rgb } of STATUS_CASES) {
      await waitFor(() => expect(statusDots(day), `${status}: exactly one rendered marker on ${day}`).toHaveLength(1))
      const dot = statusDots(day)[0]
      expect(dot, `${status}: marker must be DOM-visible on ${day}`).toBeVisible()
      expect(dot.style.backgroundColor, `${status}: D4 status treatment`).toBe(rgb)
      expect(monthDay(day)).toHaveAttribute('title', `${Number(day.slice(5, 7))}/${Number(day.slice(8))}/2026 — Status ${status}`)
    }
    expect(grid.querySelectorAll('span.rounded-full')).toHaveLength(6)
    expect(statusDots('2026-07-05')).toHaveLength(0)
    expect(statusDots('2026-07-12')).toHaveLength(0)
  })

  it.each([0, 1, 2, 3, 4])('renders %i same-day events at the three-dot boundary with truthful overflow', async (count) => {
    installHttpEdge(Array.from({ length: count }, (_, index) => seedTask(`Overflow ${index + 1}`, { due: '2026-07-20T09:00:00Z' })))
    mountCalendar()
    await selectMonth()
    // Explicit boundary oracle: no dot for zero; 1/2/3 markers below/at cap;
    // FOUR events still produce THREE dots, not zero, two, or four.
    const expectedCounts = [0, 1, 2, 3, 3]
    await waitFor(() => expect(statusDots('2026-07-20')).toHaveLength(expectedCounts[count]))
    for (const dot of statusDots('2026-07-20')) {
      expect(dot).toBeVisible()
      expect(dot.style.backgroundColor).toBe('rgb(156, 163, 175)')
    }
    if (count === 0) {
      expect(monthDay('2026-07-20')).not.toHaveAttribute('title')
    } else {
      const titles = ['Overflow 1', 'Overflow 2', 'Overflow 3'].slice(0, expectedCounts[count]).join(', ')
      expect(monthDay('2026-07-20')).toHaveAttribute('title', `7/20/2026 — ${titles}${count === 4 ? ', +1 more' : ''}`)
    }
    expect(statusDots('2026-07-19')).toHaveLength(0)
    expect(statusDots('2026-07-21')).toHaveLength(0)
  })

  it('does not turn a heartbeat task into a Month event dot while a user task on the next day remains visible', async () => {
    installHttpEdge([
      seedTask('Heartbeat only', { surface: 'heartbeat', due: '2026-07-20T09:00:00Z' }),
      seedTask('User task control', { surface: 'user', due: '2026-07-21T09:00:00Z' }),
    ])
    mountCalendar()
    await selectMonth()
    await waitFor(() => expect(statusDots('2026-07-21')).toHaveLength(1))
    expect(statusDots('2026-07-21')[0]).toBeVisible()
    expect(statusDots('2026-07-20')).toHaveLength(0)
    expect(monthDay('2026-07-20')).not.toHaveAttribute('title')
    expect(monthDay('2026-07-21')).toHaveAttribute('title', '7/21/2026 — User task control')
  })
})
