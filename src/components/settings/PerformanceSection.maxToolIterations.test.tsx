/**
 * PerformanceSection.maxToolIterations.test.tsx — #904 global
 * "Max tool calls per turn" under the Settings redesign #1519 Save bar
 * (settings-redesign-spec FR-010/FR-013/FR-016; the #904 flow is kept by
 * the spec's regression table: "Max tool calls lowering flow (#904) —
 * must stay green under the Save bar", with US-2 AS9: the affected-agents
 * list shows before the password prompt and before any write).
 *
 * The old file drove the RETIRED autosave model (700 ms debounce fired the
 * preview and opened the prompt while typing). FR-010/FR-016 re-sequence
 * it: the typed value is a draft on the bar; pressing Save changes runs
 * the lowering confirm FIRST (preview → D11 dialog), then the one password
 * prompt (FR-026), then the one PUT carrying the confirmed snapshot.
 * Replaced assertions cite the FR that replaces them:
 *   - debounce-triggered preview/prompt → preview and prompt at Save
 *     changes (FR-010, FR-016);
 *   - the #904 inline bound message → Dataset D-10 row 8's exact message
 *     "Enter a whole number from 1 to 1,000." (FR-013; the redesign owns
 *     this page's field-validation wording);
 *   - prompt-cancel reverting the field to the saved value → FR-029/D-11
 *     row 5: Cancel stores nothing and KEEPS every change;
 *   - the 500 performance_reload_failed wire shape → the C-6 write-outcome
 *     table's implemented signal for PUT /performance, a 200
     * `PerformanceSettings` with `pending_apply`: saved, not in use
 *     (FR-020 outcome 1).
 *
 * Kept, unchanged in substance (regression table): the lowering dialog
 * lists old → new before any prompt or write (US-6 AS-1), Cancel there
 * writes nothing (US-6 AS-2), the PUT carries the exact confirmed
 * snapshot (D11), a drift 409 reloads the dialog with the fresh list +
 * notice and needs a fresh confirm (US-6 AS-6/D16), the F4 result is the
 * 10 s toast plus persistent role=status summary, D13/D17 saved-value
 * warnings, and D20: a raise never previews, never opens the dialog and
 * sends no confirmed_lowering.
 *
 * Mock boundary: the NETWORK (global fetch) — the wire (paths, bodies,
 * 409 envelope) is spec-pinned; the real API layer (request(), Zod
 * parsing, the typed 409 error) stays under test. The password prompt is
 * the shell's, supplied through SettingsShellContext as a recorder
 * (FR-026/FR-027); the reauth wire itself is passwordOncePerSave.test's
 * subject.
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
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import type { SettingsShellApi } from './SettingsShellContext'
import { SettingsShellContext } from './SettingsShellContext'
import { PerformanceSection } from './PerformanceSection'

const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

const LABEL = 'Max tool calls per turn'
const DRIFT_NOTICE = 'The list of affected agents changed — review and confirm again.'
// D-10 row 8: "Max tool calls per turn 0 / 1001 | Out of range | Rejected:
// 'Enter a whole number from 1 to 1,000.'"
const BOUND_MESSAGE = 'Enter a whole number from 1 to 1,000.'

/** A prompt recorder whose outcome the test resolves by hand. */
function manualPrompt() {
  const prompts: string[][] = []
  let resolvePrompt: (token: string | null) => void = () => {}
  const requestPassword = vi.fn(
    (labels: string[]) =>
      new Promise<string | null>((resolve) => {
        prompts.push(labels)
        resolvePrompt = resolve
      }),
  )
  return {
    prompts,
    requestPassword,
    resolveWith: (token: string | null) =>
      act(async () => {
        resolvePrompt(token)
      }),
  }
}

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

function renderSectionWithShell(requestPassword: (labels: string[]) => Promise<string | null>) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SettingsShellContext.Provider
        value={
          {
            announce: () => {},
            setDraftCount: () => {},
            offerRestart: () => {},
            refreshPendingRestart: () => {},
            requestPassword,
            saveBarVisible: false,
            setSaveBarVisible: () => {},
          } as SettingsShellApi
        }
      >
        <PerformanceSection />
      </SettingsShellContext.Provider>
    </QueryClientProvider>,
  )
}

/** The row's error: the alert the field's aria-describedby names (FR-013). */
function fieldAlertText(input: HTMLElement): string {
  const ids = (input.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
  const alert = ids.map((id) => document.getElementById(id)).find((el) => el?.getAttribute('role') === 'alert')
  expect(alert, 'the field must show its error as the alert its aria-describedby names').not.toBeNull()
  return alert?.textContent ?? ''
}

/** A typed value is a draft on the bar — no preview, no prompt, no PUT (FR-010, FR-016). */
async function typeDraft(value: string) {
  await waitFor(() => screen.getByLabelText(LABEL))
  const count = puts().length
  const previewCount = previews().length
  fireEvent.change(screen.getByLabelText(LABEL), { target: { value } })
  expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
  expect(previews()).toHaveLength(previewCount)
  expect(puts()).toHaveLength(count)
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

  it.each(['0', '1001'])('US-1 AS-3: %s is refused inline with the D-10 bound message and nothing is sent (FR-013, D-10 row 8)', async (v) => {
    renderSection()
    await typeDraft(v)
    const input = screen.getByLabelText(LABEL)
    await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(input)).toBe(BOUND_MESSAGE)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    expect(saveBtn).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(saveBtn)
    expect(input).toHaveFocus()
    expect(puts()).toHaveLength(0)
    expect(previews()).toHaveLength(0)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('US-1 AS-4: cancelling the prompt at Save writes nothing and keeps the change (FR-029, D-11 row 5; the retired flow reverted the field)', async () => {
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '150' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // FR-016: the preview runs at Save changes, before any prompt or write.
    await waitFor(() => expect(previews()).toHaveLength(1))
    expect(previews()[0].url).toBe('/api/v1/performance/max-tool-iterations/preview?value=150')
    // No affected agents → no lowering dialog; the one prompt is asked.
    await waitFor(() => expect(p.prompts.length).toBe(1))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await p.resolveWith(null)
    // FR-029/D-11 row 5: Cancel stores nothing and keeps every change —
    // the draft stays in the field, the bar still counts it.
    await waitFor(() => {
      expect(screen.getByLabelText(LABEL)).toHaveValue(150)
      expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    })
    expect(puts()).toHaveLength(0)
  })

  it('US-6 AS-4: lowering with no affected agents shows no dialog and PUTs only the limit (FR-016 sequencing)', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    putHandlers = [() => ({ status: 200, body: settings({ max_tool_iterations: 150 }) })]
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '150' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // Preview at Save (FR-016); no agents → no dialog; the prompt runs
    // (FR-026) and its token rides the one PUT.
    await waitFor(() => expect(previews()).toHaveLength(1))
    await p.resolveWith('reauth_tok')
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
  it('US-6 AS-1: the dialog lists "Alpha: 250 → 200" BEFORE any password prompt or write (FR-016)', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    previewHandler = () => ({ status: 200, body: { value: 200, agents: [ALPHA] } })
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))

    const dialog = await findLoweringDialog()
    expect(dialog.textContent).toMatch(/Alpha[\s\S]*250[\s\S]*200/)
    // FR-016: the confirm precedes the password prompt — none is open yet,
    // and nothing has been written.
    expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    expect(p.prompts.length).toBe(0)
    expect(puts()).toHaveLength(0)
  })

  it('US-6 AS-2: Cancel in the dialog writes nothing and asks for no password (FR-016)', async () => {
    // Spec Given: the global in force is 300 (US-6 scenarios) — the typed
    // value must differ from the saved one or no change event fires.
    perfGet = () => settings({ max_tool_iterations: 300 })
    previewHandler = () => ({ status: 200, body: { value: 200, agents: [ALPHA] } })
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    const dialog = await findLoweringDialog()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    expect(p.prompts.length).toBe(0)
    expect(puts()).toHaveLength(0)
    // FR-016: Cancel keeps the changes — the draft stays on the bar.
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
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
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))

    // FR-016 order: the D11 dialog first…
    fireEvent.click(confirmButton(await findLoweringDialog()))
    // …then the one prompt (FR-026) resolves and the PUT goes out.
    await waitFor(() => expect(p.prompts.length).toBe(1))
    expect(p.prompts[0]).toEqual(['Max tool calls per turn'])
    await p.resolveWith('reauth_tok')
    await waitFor(() => expect(puts()).toHaveLength(1))
    const body = puts()[0].body as { max_tool_iterations: number; confirmed_lowering: { agent_id: string; old_value: number }[] }
    expect(body.max_tool_iterations).toBe(200)
    expect([...body.confirmed_lowering].sort((a, b) => a.agent_id.localeCompare(b.agent_id))).toEqual([
      { agent_id: 'a1', old_value: 250 },
      { agent_id: 'b1', old_value: 280 },
    ])

    const summary = 'Lowered 2 agents: Alpha 250 → 200, Beta 280 → 200'
    await waitFor(() => expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ message: summary, duration: 10000 })))
    const statuses = screen.getAllByRole('status')
    expect(statuses.some((s) => s.textContent?.includes(summary))).toBe(true)
  })

  it('US-6 AS-6 (D16): a 409 drift reloads the dialog from the 409 preview with the notice, and requires a fresh confirm (FR-031)', async () => {
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
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    fireEvent.click(confirmButton(await findLoweringDialog()))
    await waitFor(() => expect(p.prompts.length).toBe(1))
    await p.resolveWith('reauth_tok')
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

describe('PerformanceSection — raise (D20) and saved-but-not-applied (FR-020)', () => {
  const CAPPED = { agent_id: 'c1', agent_name: 'Capped', old_value: 500, new_value: 300 }

  it('D20: raising the global opens no lowering dialog and sends no confirmed_lowering (FR-010, FR-015)', async () => {
    // D20: a raise never rewrites an agent, so the preview/confirm step applies
    // only below the current global. The preview stub deliberately lists a
    // capped agent (stored 500 > new 300) so the test proves the SPA itself
    // does not open the dialog on a raise, whatever a preview would say.
    previewHandler = () => ({ status: 200, body: { value: 300, agents: [CAPPED] } })
    putHandlers = [() => ({ status: 200, body: settings({ max_tool_iterations: 300 }) })]
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '300' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // The prompt is requested only after the async save chain starts; wait
    // for it before resolving, else the resolve lands on a stale no-op.
    await waitFor(() => expect(p.prompts.length).toBe(1))
    await p.resolveWith('reauth_tok')
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    const body = puts()[0].body as Record<string, unknown>
    expect(body.max_tool_iterations).toBe(300)
    expect(body.confirmed_lowering === undefined || (Array.isArray(body.confirmed_lowering) && body.confirmed_lowering.length === 0)).toBe(true)
  })

  it('a pending apply says the value was saved but not in use — no error toast, no refusal pinned on the field (FR-020 outcome 1; C-6: PUT /performance answers 200 with pending_apply)', async () => {
    // The old file drove a 500 performance_reload_failed wire shape; the
    // approved C-6 write-outcome table implements this stage as a 200
    // `PerformanceSettings` carrying `pending_apply` — recognized as
    // "saved, not in use" whatever the status would have said. The server
    // reports the pending apply on every GET too, until the reload lands.
    let pendingApply: { stage: string; changed_fields: string[] } | null = null
    putHandlers = [() => {
      pendingApply = { stage: 'refresh', changed_fields: ['max_tool_iterations'] }
      return { status: 200, body: settings({ pending_apply: pendingApply }) }
    }]
    perfGet = () => (pendingApply ? settings({ pending_apply: pendingApply }) : settings())
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '300' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // The prompt is requested only after the async save chain starts; wait
    // for it before resolving, else the resolve lands on a stale no-op.
    await waitFor(() => expect(p.prompts.length).toBe(1))
    await p.resolveWith('reauth_tok')
    await waitFor(() => expect(puts()).toHaveLength(1))
    // The value IS saved: the field keeps it and no error is pinned on it.
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
    expect(screen.queryAllByRole('alert').filter((a) => /failed|could not be saved/i.test(a.textContent ?? ''))).toEqual([])
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
    // The saved-not-in-use state announces itself (the notice whose copy
    // PerformanceSection.pendingApply.test pins).
    const notice = await screen.findByTestId('performance-unapplied-notice')
    expect(notice).toHaveAttribute('role', 'status')
  })

  it('stage "refresh": the saved value stays shown even though GET /performance still answers the old value (FR-020 outcome 1)', async () => {
    // After the pending save, every GET still reports the OLD in-memory 200
    // plus the server's pending-apply state (C-6 stage refresh: config.json
    // holds the new value, the running config was not swapped). The field
    // must keep showing the SAVED 300 — never snap back to the stale 200.
    putHandlers = [() => ({
      status: 200,
      body: settings({ pending_apply: { stage: 'refresh', changed_fields: ['max_tool_iterations'] } }),
    })]
    perfGet = () => settings() // stale in-memory answer: still 200
    const p = manualPrompt()
    renderSectionWithShell(p.requestPassword)
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '300' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // The prompt is requested only after the async save chain starts; wait
    // for it before resolving, else the resolve lands on a stale no-op.
    await waitFor(() => expect(p.prompts.length).toBe(1))
    await p.resolveWith('reauth_tok')
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0].body as Record<string, unknown>).max_tool_iterations, 'instrument: 300 was the saved value').toBe(300)
    // Let any refetch the outcome triggers settle before reading the field
    // (a fix may refetch or not; either way the field must end at 300).
    await act(async () => { await new Promise((r) => setTimeout(r, 100)) })
    expect((screen.getByLabelText(LABEL) as HTMLInputElement).value).toBe('300')
  })
})
