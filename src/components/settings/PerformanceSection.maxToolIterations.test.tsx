/**
 * PerformanceSection.maxToolIterations.test.tsx — #904 RED (test plan rows
 * 13d and 15): the global "Max tool calls per turn" control in Settings →
 * Performance, the D11 lowering confirm dialog, the D16 drift reload, and
 * the D13 saved-value warning.
 *
 * Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) — User
 * Stories 1 and 6, "UI Screens and States" (Performance row + D11 dialog
 * row), "User Journey" step 2, "Accessibility and Keyboard" (toast 10,000 ms
 * + persistent role=status summary; drift notice role=alert), Contract rows
 * PerformanceSettings / PerformanceSettingsUpdate / preview path /
 * MaxToolIterationsLoweringConflict. Founder decisions D3, D8, D11, D13, D16.
 *
 * Mock boundary: the NETWORK (global fetch) — not the API module. The spec
 * pins the wire (paths, bodies, 409 envelope) but not the SPA function that
 * fetches the lowering preview, so routing by URL keeps the real API layer
 * (request(), Zod parsing, the typed 409 error) under test. Only the
 * app-state probe and the password re-auth call are module-mocked, exactly
 * as PerformanceSection.password.test.tsx does. Local mode (identity.mode
 * 'local') pins the step-up to the password dialog, which is the flow the
 * spec describes: preview → D11 dialog → password → PUT.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
    reAuth: vi.fn(),
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'

const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

const LABEL = 'Max tool calls per turn'
const DRIFT_NOTICE = 'The list of affected agents changed — review and confirm again.'

function settings(overrides: Record<string, unknown> = {}) {
  return {
    max_parallel_agents: 4,
    effective_max_parallel_agents: 4,
    max_parallel_agents_configured: true,
    tools_on_demand: true,
    goal_max_rounds: 20,
    max_tool_iterations: 200,
    max_tool_iterations_saved_state: 'ok',
    ...overrides,
  }
}

interface Call { url: string; method: string; body: unknown }

type Handler = (url: URL, init: RequestInit | undefined) => { status: number; body: unknown }

let calls: Call[]
let perfGet: () => unknown
let previewHandler: Handler
let putHandlers: Handler[]

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = new URL(String(input), 'http://localhost')
  const method = (init?.method ?? 'GET').toUpperCase()
  calls.push({ url: url.pathname + url.search, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
  if (url.pathname === '/api/v1/performance/max-tool-iterations/preview' && method === 'GET') {
    const r = previewHandler(url, init)
    return json(r.status, r.body)
  }
  if (url.pathname === '/api/v1/performance' && method === 'GET') return json(200, perfGet())
  if (url.pathname === '/api/v1/performance' && method === 'PUT') {
    const h = putHandlers.shift()
    if (!h) return json(500, { error: 'test: unexpected extra PUT' })
    const r = h(url, init)
    return json(r.status, r.body)
  }
  return json(404, { error: `test: unrouted ${method} ${url.pathname}` })
})

const puts = () => calls.filter((c) => c.method === 'PUT' && c.url === '/api/v1/performance')
const previews = () => calls.filter((c) => c.url.startsWith('/api/v1/performance/max-tool-iterations/preview'))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

/** Type a value into the global limit field and let the autosave debounce settle. */
async function typeLimit(value: string) {
  await waitFor(() => screen.getByLabelText(LABEL))
  vi.useFakeTimers()
  fireEvent.change(screen.getByLabelText(LABEL), { target: { value } })
  await act(async () => { vi.advanceTimersByTime(700) })
  vi.useRealTimers()
}

/** The D11 lowering dialog is a ConfirmDialog (role alertdialog). */
async function findLoweringDialog() {
  return screen.findByRole('alertdialog')
}

/** ConfirmDialog's confirm button = the one that is not its Cancel. */
function confirmButton(dialog: HTMLElement) {
  const btn = within(dialog).getAllByRole('button').find((b) => !b.hasAttribute('data-confirm-dialog-cancel'))
  if (!btn) throw new Error('no confirm button in the lowering dialog')
  return btn
}

async function passReAuth() {
  await waitFor(() => expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument())
  fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'pw' } })
  fireEvent.click(screen.getByTestId('reauth-confirm'))
}

const ALPHA = { agent_id: 'a1', agent_name: 'Alpha', old_value: 250, new_value: 200 }
const BETA = { agent_id: 'b1', agent_name: 'Beta', old_value: 280, new_value: 200 }

beforeEach(() => {
  vi.clearAllMocks()
  calls = []
  perfGet = () => settings()
  previewHandler = (url) => ({ status: 200, body: { value: Number(url.searchParams.get('value')), agents: [] } })
  putHandlers = []
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test')
  vi.mocked(api.fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
  vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('harness instrument check', () => {
  // Proves the fetch-level stub drives the REAL API layer into the section:
  // an existing control renders the stubbed GET value. If this fails, every
  // red below is a harness artefact, not a missing feature.
  it('renders the existing "Tries per goal" control from the stubbed GET /performance', async () => {
    perfGet = () => settings({ goal_max_rounds: 17 })
    renderSection()
    await waitFor(() => expect(screen.getByLabelText('Tries per goal')).toHaveValue(17))
    expect(calls.some((c) => c.method === 'GET' && c.url === '/api/v1/performance')).toBe(true)
  })
})

describe('PerformanceSection — Max tool calls per turn (US-1, D3, D13)', () => {
  it('US-1 AS-1: a fresh install shows the server global 200 beside "Tries per goal"', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(200))
    expect(screen.getByLabelText('Tries per goal')).toBeInTheDocument()
  })

  it('renders the server value, never a literal: global 350 shows 350', async () => {
    perfGet = () => settings({ max_tool_iterations: 350 })
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(350))
  })

  it('D13: above_max shows a warning naming the saved 5000 and the 1000 in force', async () => {
    perfGet = () => settings({ max_tool_iterations: 1000, max_tool_iterations_saved_state: 'above_max', max_tool_iterations_saved_raw: 5000 })
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(1000))
    const warning = await screen.findByText((_, el) =>
      !!el && el.children.length === 0 && /5000/.test(el.textContent ?? '') && /1000/.test(el.textContent ?? ''))
    expect(warning).toBeInTheDocument()
  })

  it('D13: a missing saved value shows a warning that says so and names the 200 in force', async () => {
    perfGet = () => settings({ max_tool_iterations: 200, max_tool_iterations_saved_state: 'missing' })
    renderSection()
    const warning = await screen.findByText((_, el) =>
      !!el && el.children.length === 0 && /missing/i.test(el.textContent ?? '') && /200/.test(el.textContent ?? ''))
    expect(warning).toBeInTheDocument()
  })

  it.each(['0', '1001'])('US-1 AS-3: %s is refused inline with the bound message and nothing is sent', async (v) => {
    renderSection()
    await typeLimit(v)
    expect(await screen.findByText(/must be between 1 and 1000/)).toBeInTheDocument()
    expect(puts()).toHaveLength(0)
    expect(previews()).toHaveLength(0)
  })

  it('US-1 AS-4: cancelling the password prompt writes nothing and the field returns to the saved 200', async () => {
    renderSection()
    await typeLimit('150')
    await waitFor(() => expect(previews()).toHaveLength(1))
    expect(previews()[0].url).toBe('/api/v1/performance/max-tool-iterations/preview?value=150')
    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(200))
    expect(puts()).toHaveLength(0)
  })

  it('US-6 AS-4: lowering with no affected agents shows no dialog and PUTs only the limit', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    putHandlers = [() => ({ status: 200, body: settings({ max_tool_iterations: 150 }) })]
    renderSection()
    await typeLimit('150')
    await passReAuth()
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    const body = puts()[0].body as Record<string, unknown>
    expect(body.max_tool_iterations).toBe(150)
    // confirmed_lowering is optional; absent and [] both mean "none" (contract).
    expect(body.confirmed_lowering === undefined || (Array.isArray(body.confirmed_lowering) && body.confirmed_lowering.length === 0)).toBe(true)
    // Only the limit (and its consent snapshot) — never the other Performance fields.
    expect(Object.keys(body).sort().filter((k) => k !== 'confirmed_lowering')).toEqual(['max_tool_iterations'])
  })
})

describe('PerformanceSection — D11 lowering dialog and D16 drift (US-6)', () => {
  it('US-6 AS-1: the dialog lists "Alpha: 250 → 200" BEFORE any password prompt or write', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    previewHandler = () => ({ status: 200, body: { value: 200, agents: [ALPHA] } })
    renderSection()
    await typeLimit('200')
    const dialog = await findLoweringDialog()
    expect(dialog.textContent).toMatch(/Alpha[\s\S]*250[\s\S]*200/)
    expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    expect(puts()).toHaveLength(0)
  })

  it('US-6 AS-2: Cancel in the dialog writes nothing and asks for no password', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    previewHandler = () => ({ status: 200, body: { value: 200, agents: [ALPHA] } })
    renderSection()
    await typeLimit('200')
    const dialog = await findLoweringDialog()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    expect(puts()).toHaveLength(0)
  })

  it('US-6 AS-3: confirm → password → PUT carries the confirmed snapshot; toast (10 s) and a persistent role=status summary name the lowered agents', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    previewHandler = () => ({ status: 200, body: { value: 200, agents: [ALPHA, BETA] } })
    putHandlers = [() => ({
      status: 200,
      body: settings({ max_tool_iterations: 200, max_tool_iterations_lowered_agents: [ALPHA, BETA] }),
    })]
    renderSection()
    await typeLimit('200')
    fireEvent.click(confirmButton(await findLoweringDialog()))
    await passReAuth()
    await waitFor(() => expect(puts()).toHaveLength(1))
    const body = puts()[0].body as { max_tool_iterations: number; confirmed_lowering: { agent_id: string; old_value: number }[] }
    expect(body.max_tool_iterations).toBe(200)
    expect([...body.confirmed_lowering].sort((a, b) => a.agent_id.localeCompare(b.agent_id))).toEqual([
      { agent_id: 'a1', old_value: 250 },
      { agent_id: 'b1', old_value: 280 },
    ])
    expect(vi.mocked(api.reAuth)).toHaveBeenCalledWith('pw')

    const summary = 'Lowered 2 agents: Alpha 250 → 200, Beta 280 → 200'
    await waitFor(() => expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ message: summary, duration: 10000 })))
    const statuses = screen.getAllByRole('status')
    expect(statuses.some((s) => s.textContent?.includes(summary))).toBe(true)
  })

  it('US-6 AS-6 (D16): a 409 drift reloads the dialog from the 409 preview with the notice, and requires a fresh confirm', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    previewHandler = () => ({ status: 200, body: { value: 200, agents: [ALPHA] } })
    const fresh = { value: 200, agents: [ALPHA, { agent_id: 'b1', agent_name: 'Beta', old_value: 220, new_value: 200 }] }
    putHandlers = [() => ({
      status: 409,
      body: {
        error: 'the agents affected by lowering the limit to 200 changed since the preview; review the updated list and confirm again',
        code: 'max_tool_iterations_lowering_drift',
        preview: fresh,
      },
    })]
    renderSection()
    await typeLimit('200')
    fireEvent.click(confirmButton(await findLoweringDialog()))
    await passReAuth()
    await waitFor(() => expect(puts()).toHaveLength(1))

    const dialog = await findLoweringDialog()
    await waitFor(() => expect(dialog.textContent).toMatch(/Beta[\s\S]*220[\s\S]*200/))
    expect(dialog.textContent).toMatch(/Alpha[\s\S]*250[\s\S]*200/)
    const alert = within(dialog).getByRole('alert')
    expect(alert).toHaveTextContent(DRIFT_NOTICE)
    // No automatic retry: nothing more is sent until the admin confirms again.
    expect(puts()).toHaveLength(1)
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringMatching(/^Lowered/) }))
  })
})

describe('PerformanceSection — raise (D20) and reload failure (8-reviewer gate gaps)', () => {
  const CAPPED = { agent_id: 'c1', agent_name: 'Capped', old_value: 500, new_value: 300 }

  it('D20: raising the global opens no lowering dialog and sends no confirmed_lowering', async () => {
    // D20: a raise never rewrites an agent, so the preview/confirm step applies
    // only below the current global. The preview stub deliberately lists a
    // capped agent (stored 500 > new 300) so the test proves the SPA itself
    // does not open the dialog on a raise, whatever a preview would say.
    previewHandler = () => ({ status: 200, body: { value: 300, agents: [CAPPED] } })
    putHandlers = [() => ({ status: 200, body: settings({ max_tool_iterations: 300 }) })]
    renderSection()
    await typeLimit('300')
    await passReAuth()
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    const body = puts()[0].body as Record<string, unknown>
    expect(body.max_tool_iterations).toBe(300)
    expect(body.confirmed_lowering === undefined || (Array.isArray(body.confirmed_lowering) && body.confirmed_lowering.length === 0)).toBe(true)
  })

  it('a 500 performance_reload_failed says the value was saved but not applied — not a generic server error', async () => {
    putHandlers = [() => ({ status: 500, body: { error: 'reload failed: injected', code: 'performance_reload_failed' } })]
    renderSection()
    await typeLimit('300')
    await passReAuth()
    await waitFor(() => expect(puts()).toHaveLength(1))
    const shown = () => [...addToast.mock.calls.map((c) => String((c[0] as { message?: string })?.message ?? '')), document.body.textContent ?? '']
    await waitFor(() => expect(shown().some((t) => /saved,? but not (yet )?applied/i.test(t))).toBe(true))
    expect(shown().some((t) => /server (is )?unavailable/i.test(t))).toBe(false)
    // CHECK round 2, finding C: the TEXT alone does not tell "saved, not
    // applied" from a failed save (the generic error path would toast the same
    // userMessage). What distinguishes them (spec edge case "Reload fails after
    // the global is written": the value IS saved) is the treatment: a warning,
    // never an error toast, and no save error pinned on the limit field.
    const toasts = addToast.mock.calls.map((c) => c[0] as { variant?: string; message?: string })
    const savedNotApplied = toasts.filter((t) => /saved,? but not (yet )?applied/i.test(t.message ?? ''))
    expect(savedNotApplied, 'exactly one "saved, not applied" toast').toHaveLength(1)
    expect(savedNotApplied[0].variant, 'a committed save is a warning, not an error').toBe('warning')
    expect(toasts.filter((t) => t.variant === 'error'), 'no error toast for a committed save').toEqual([])
    expect(screen.queryAllByRole('alert').filter((a) => /saved,? but not (yet )?applied|failed/i.test(a.textContent ?? '')),
      'no save error pinned on the field').toEqual([])
  })

  // Gate round 2 (typed reload-failure details, backend-lead feature/904-fix2-be):
  // details.stage "refresh" means config.json holds the new value but the
  // gateway's in-memory config was NOT swapped, so GET /performance still
  // answers the OLD value until the next reload. The screen must keep showing
  // the value the operator saved (it IS saved) — never snap back to the stale
  // in-memory 200 as if the save had not happened. RED until frontend-lead
  // consumes details.stage.
  it('stage "refresh": the saved value stays shown even though GET /performance still answers the old value', async () => {
    putHandlers = [() => ({
      status: 500,
      body: {
        error: 'performance settings saved but the reload failed',
        code: 'performance_reload_failed',
        details: { stage: 'refresh', changed_fields: ['max_tool_iterations'] },
      },
    })]
    perfGet = () => settings() // stale in-memory answer: still 200
    renderSection()
    await typeLimit('300')
    await passReAuth()
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0].body as Record<string, unknown>).max_tool_iterations, 'instrument: 300 was the saved value').toBe(300)
    const shown = () => [...addToast.mock.calls.map((c) => String((c[0] as { message?: string })?.message ?? '')), document.body.textContent ?? '']
    await waitFor(() => expect(shown().some((t) => /saved,? but not (yet )?applied/i.test(t))).toBe(true))
    // Let any refetch the failure triggers settle before reading the field
    // (a fix may refetch or not; either way the field must end at 300).
    await act(async () => { await new Promise((r) => setTimeout(r, 100)) })
    expect((screen.getByLabelText(LABEL) as HTMLInputElement).value).toBe('300')
  })
})
