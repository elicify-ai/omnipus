/** T11-B §4 / FR-6, FR-7; Settings redesign #1519 (FR-010, FR-011, FR-013, FR-015, FR-018, FR-020, FR-022).
 * The section's own "Save" button and its completion toast are replaced by
 * the one Save bar: drafts count on the bar ("N unsaved change(s)"), the
 * button is "Save changes", the saved signal is FR-018's "Saved at HH:MM",
 * and the write outcomes follow FR-020 (the saved_not_refreshed case now
 * shows the spec's not-in-use message instead of echoing the server
 * sentence). The whole-section PUT oracle is FR-022 and is unchanged.
 * Real Section, HTTP client, query cache and toast store. Only fetch is fake.
 * Observe toast publication at the store boundary; host role mapping is a
 * separate, UNVERIFIED frontend-owner gap (team-lead ruling 2026-10-31).
 * No invented client-function names and no swallowed component import errors.
 * Missing implementation fails every case loudly; it is not behavioural proof.
 */
import { beforeAll, beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import type { ComponentType } from 'react'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider, QueryObserver } from '@tanstack/react-query'
import type {
  CliDetect, CliValidateRequest, CliValidateResponse, ErrorResponse,
  ThirdPartyAgentSettings, ThirdPartyAgentSettingsView,
} from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'

let Section: ComponentType = () => {
  throw new Error('BLOCKED: ThirdPartyAgentsSection not implemented — required by T11 §4 / FR-6, FR-7')
}
const sectionModules = import.meta.glob<{ ThirdPartyAgentsSection: ComponentType }>('./ThirdPartyAgentsSection.tsx')
beforeAll(async () => {
  const load = sectionModules['./ThirdPartyAgentsSection.tsx']
  if (load) Section = (await load()).ThirdPartyAgentsSection
})

type Row = ThirdPartyAgentSettingsView['clis']['codex']
function row(overrides: Partial<Row> = {}): Row {
  return { path: '', preset_args: '', detected: { installed: false, path: null, link_path: null, source: null }, ...overrides }
}
const CODEX_DETECT: Row['detected'] = {
  installed: true, path: '/opt/homebrew/Cellar/codex/1.2.3/bin/codex',
  link_path: '/opt/homebrew/bin/codex', source: 'path',
}
function view(): ThirdPartyAgentSettingsView {
  return { clis: {
    'claude-code': row({ path: '/Users/me/bin/claude', preset_args: '--max-turns 40', detected: {
      installed: true, path: '/usr/local/bin/claude', link_path: '/usr/local/bin/claude', source: 'path',
    } }),
    codex: row({ detected: { ...CODEX_DETECT } }), opencode: row(),
  } }
}
const PRESET = '--dangerously-bypass-approvals-and-sandbox --model gpt-5-codex'
const REFUSAL = 'The program path for Codex is not allowed: it is inside a workspace.'
const REFUSAL_BODY: ErrorResponse = {
  error: REFUSAL, code: 'cli_path_invalid', field: 'clis.codex.path',
  details: { cli: 'codex', check: 'agent_writable_location' },
}
function expectedSave(codexPath = '/opt/homebrew/bin/codex', codexPreset = ''): ThirdPartyAgentSettings {
  return { clis: {
    'claude-code': { path: '/Users/me/bin/claude', preset_args: '--max-turns 40' },
    codex: { path: codexPath, preset_args: codexPreset }, opencode: { path: '', preset_args: '' },
  } }
}
function json(body: ThirdPartyAgentSettingsView | CliValidateResponse | ErrorResponse | CliDetect, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((yes) => { resolve = yes })
  return { promise, resolve }
}

// not-wire-format: captured transport calls used only by this test harness.
type Call = { method: string; path: string; body: string | undefined }
let calls: Call[]
let stored: ThirdPartyAgentSettingsView
let getSettings: () => Promise<Response>
let putSettings: (body: ThirdPartyAgentSettings) => Promise<Response>
let checkCLI: (body: CliValidateRequest) => Promise<Response>
let client: QueryClient
const settingsURL = '/api/v1/settings/third-party-agents'
const checkURL = '/api/v1/system/cli-validate'
const getCalls = () => calls.filter((c) => c.method === 'GET' && c.path === settingsURL)
const putCalls = () => calls.filter((c) => c.method === 'PUT' && c.path === settingsURL)
const checkCalls = () => calls.filter((c) => c.method === 'POST' && c.path === checkURL)
const codexPathInput = () => screen.getByRole('textbox', { name: 'Codex program path' }) as HTMLInputElement
const codexParams = () => screen.getByRole('textbox', { name: 'Codex parameters' }) as HTMLInputElement
const saveButton = () => screen.getByRole('button', { name: /save changes/i })

function renderSection() {
  return render(<QueryClientProvider client={client}><Section /></QueryClientProvider>)
}
async function loaded() {
  await screen.findByRole('textbox', { name: 'Codex program path' })
}
async function expectSavedSignal() {
  // FR-018: the bar's "Saved at HH:MM" signal replaces the section's
  // completion toast; nothing announces success before it appears.
  await screen.findByText(/Saved at/)
}
function expectCheckToast(exactResult: string, variants: string[]) {
  const toasts = useUiStore.getState().toasts
  expect(toasts).toHaveLength(1)
  expect(toasts[0].message).toContain(exactResult)
  expect(variants).toContain(toasts[0].variant)
}
function publishedMessages() {
  return useUiStore.getState().toasts.map((toast) => toast.message)
}
function expectFailureNotice(pattern: RegExp) {
  // Errors may be inline or published for the application toast host. Never
  // render a test-side host and pretend that proves the production mapping.
  const inline = screen.queryAllByText(pattern).map((element) => element.textContent ?? '')
  expect([...inline, ...publishedMessages()].some((message) => pattern.test(message))).toBe(true)
}

beforeEach(() => {
  calls = []
  stored = view()
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
  useUiStore.setState({ toasts: [] })
  // The real client requires its CSRF cookie for state-changing requests.
  document.cookie = 'csrf=t11b-person-session; path=/'
  getSettings = async () => json(stored)
  putSettings = async (body) => {
    const previous = stored
    stored = { clis: {
      'claude-code': { ...previous.clis['claude-code'], ...(body.clis['claude-code'] ?? { path: '', preset_args: '' }) },
      codex: { ...previous.clis.codex, ...(body.clis.codex ?? { path: '', preset_args: '' }) },
      opencode: { ...previous.clis.opencode, ...(body.clis.opencode ?? { path: '', preset_args: '' }) },
    } }
    return json(stored)
  }
  checkCLI = async () => json({ ok: true, reason: 'ok', resolved_path: '/opt/homebrew/bin/codex', version: '1.2.3', detail: 'OK' })
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const rawURL = input instanceof Request ? input.url : String(input)
    const path = new URL(rawURL, window.location.origin).pathname
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase()
    const body = typeof init?.body === 'string' ? init.body : undefined
    calls.push({ method, path, body })
    if (path === settingsURL && method === 'GET') return getSettings()
    if (path === settingsURL && method === 'PUT') return putSettings(JSON.parse(body ?? '') as ThirdPartyAgentSettings)
    if (path === checkURL && method === 'POST') return checkCLI(JSON.parse(body ?? '') as CliValidateRequest)
    // The spec also permits the existing filesystem-only detection endpoint.
    if (path === '/api/v1/system/cli-detect' && method === 'GET') {
      const detect: CliDetect = { claude: stored.clis['claude-code'].detected, codex: stored.clis.codex.detected, opencode: stored.clis.opencode.detected }
      return json(detect)
    }
    // C-6 saved-state check seam (FR-020 outcome 4, spec :246): after an
    // unknown result the screen reads GET /config/pending-restart through
    // the raw fetch edge (saveOutcome.ts::readPendingRestartFresh). An empty
    // list leaves the written keys unlisted, so the saved value equals the
    // running read — what lets the check conclude at all. The reply mirrors
    // readPendingRestartFresh's expectations (ok + a JSON array body).
    if (path === '/api/v1/config/pending-restart' && method === 'GET') {
      return { ok: true, status: 200, json: async () => [] as unknown[] }
    }
    throw new Error(`Unexpected HTTP boundary call: ${method} ${path}`)
  }))
})
afterEach(() => {
  client.clear()
  useUiStore.setState({ toasts: [] })
  document.cookie = 'csrf=; path=/; max-age=0'
  vi.unstubAllGlobals()
})

describe('FR-6: rows, pre-fill and free parameters', () => {
  it('renders exactly three CLI-qualified path/parameter/Check rows; the Save bar appears on the first draft (FR-010, FR-011)', async () => {
    renderSection(); await loaded()
    for (const name of ['Claude Code', 'Codex', 'opencode']) {
      expect(screen.getByRole('textbox', { name: `${name} program path` })).toBeEnabled()
      expect(screen.getByRole('textbox', { name: `${name} parameters` })).toBeEnabled()
      expect(screen.getByRole('button', { name: `Check ${name} CLI` })).toBeEnabled()
    }
    expect(screen.getAllByRole('textbox')).toHaveLength(6)
    // FR-011: with zero drafts the bar is hidden; the old per-section "Save"
    // button is replaced by the bar's "Save changes".
    expect(screen.queryByRole('button', { name: /save changes/i })).not.toBeInTheDocument()
    expect(putCalls()).toEqual([])

    fireEvent.change(codexParams(), { target: { value: PRESET } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument()
    expect(putCalls()).toEqual([])
  })
  it('pre-fills link_path, displays the found path, and does not save or check it', async () => {
    renderSection(); await loaded()
    expect(codexPathInput()).toHaveValue('/opt/homebrew/bin/codex')
    expect(screen.getByText(/found at.*\/opt\/homebrew\/bin\/codex/i)).toBeInTheDocument()
    expect(putCalls()).toEqual([])
    expect(checkCalls()).toEqual([])
    expect(calls.every((c) => c.method === 'GET')).toBe(true)
  })
  it('falls back to detected path when link_path is absent', async () => {
    stored.clis.codex.detected = { ...CODEX_DETECT, link_path: null }
    renderSection(); await loaded()
    expect(codexPathInput()).toHaveValue(CODEX_DETECT.path)
  })
  it('keeps the saved path instead of overwriting it with detection', async () => {
    renderSection(); await loaded()
    expect(screen.getByRole('textbox', { name: 'Claude Code program path' })).toHaveValue('/Users/me/bin/claude')
    expect(screen.getByRole('textbox', { name: 'Claude Code parameters' })).toHaveValue('--max-turns 40')
    expect(screen.getByRole('textbox', { name: 'opencode program path' })).toHaveValue('')
  })
  it.each(['', '/Users/me/bin/typed-codex'])('does not overwrite touched draft %j on detection refresh', async (draft) => {
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: draft } })
    stored = { ...stored, clis: { ...stored.clis, codex: row({ detected: { ...CODEX_DETECT, link_path: '/new/bin/codex' } }) } }
    await act(async () => { await client.invalidateQueries() })
    await waitFor(() => expect(getCalls().length).toBeGreaterThanOrEqual(2))
    expect(codexPathInput()).toHaveValue(draft)
    expect(putCalls()).toEqual([])
  })
  it('shows any preset in full without warning, including the accepted bypass flag', async () => {
    stored.clis.codex.preset_args = PRESET
    renderSection(); await loaded()
    expect(codexParams()).toHaveValue(PRESET)
    expect(screen.queryByText(/warning|unsafe|dangerous|not allowed|unsupported parameter/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('FR-7: whole-section save and failures', () => {
  // §4 permits detection pre-fill only for an UNTOUCHED field. U-6's
  // intentional clear must survive later whole-section saves too.
  it.each(['detected only', 'saved and detected'])('keeps a cleared %s program empty when another CLI row is saved', async (initial) => {
    if (initial === 'saved and detected') stored.clis.codex.path = '/opt/homebrew/bin/codex'
    renderSection(); await loaded()
    expect(codexPathInput()).toHaveValue('/opt/homebrew/bin/codex')
    fireEvent.change(codexPathInput(), { target: { value: '' } })
    // FR-011 typed-back rule: clearing a detected-only pre-fill returns the
    // field to its saved (empty) value, so it is not a draft and the bar
    // stays hidden; a saved-and-detected clear is one real draft.
    if (initial === 'detected only') {
      expect(screen.queryByRole('button', { name: /save changes/i })).not.toBeInTheDocument()
    } else {
      expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    }

    // FR-022: the whole-section Save carries every row, so the cleared codex
    // path is stored empty together with the other row's change.
    fireEvent.change(screen.getByRole('textbox', { name: 'Claude Code parameters' }), {
      target: { value: '--max-turns 41' },
    })
    await screen.findByText(initial === 'detected only' ? /1 unsaved change/ : /2 unsaved changes/)
    fireEvent.click(saveButton())
    await expectSavedSignal()
    expect(putCalls()).toHaveLength(1)
    const wholeSave: ThirdPartyAgentSettings = { clis: {
      ...expectedSave('').clis,
      'claude-code': { path: '/Users/me/bin/claude', preset_args: '--max-turns 41' },
    } }
    expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(wholeSave)
    expect(stored.clis.codex.path).toBe('')
    expect(stored.clis.codex.detected).toEqual(CODEX_DETECT)
    expect(checkCalls()).toEqual([])
  })
  it('refreshes existing worker and agent-list caches after saving the program and parameters (FR-8)', async () => {
    // §4 / FR-8: existing Runtime consumers must receive current Settings.
    // Read fixtures project the mock server's saved row; they are outside the
    // Section. QueryObserver, cache invalidation and refetching remain real.
    const before: Pick<Row, 'path' | 'preset_args'> = { path: '/Users/me/bin/codex-before-save', preset_args: '--model before-save' }
    const after: Pick<Row, 'path' | 'preset_args'> = { path: '/Users/me/bin/codex-after-save', preset_args: '--model after-save' }
    stored.clis.codex = { ...stored.clis.codex, ...before }
    const readSavedRow = (): Pick<Row, 'path' | 'preset_args'> => ({
      path: stored.clis.codex.path, preset_args: stored.clis.codex.preset_args,
    })
    const readWorker = vi.fn(async () => readSavedRow())
    const readAgents = vi.fn(async () => [readSavedRow()])
    const workerKey = ['agent', 'settings-save-worker'] as const
    const agentsKey = ['agents', 'settings-save-workspace'] as const
    // Fresh initial data prevents a mount/staleness refetch from passing this
    // test without the successful Settings save refreshing both query families.
    const worker = new QueryObserver(client, {
      queryKey: workerKey, queryFn: readWorker, initialData: before, staleTime: Infinity,
    })
    const agents = new QueryObserver(client, {
      queryKey: agentsKey, queryFn: readAgents, initialData: [before], staleTime: Infinity,
    })
    const stopWorker = worker.subscribe(() => {})
    const stopAgents = agents.subscribe(() => {})
    try {
      renderSection(); await loaded()
      expect(client.getQueryData(workerKey)).toEqual(before)
      expect(client.getQueryData(agentsKey)).toEqual([before])
      expect(readWorker).not.toHaveBeenCalled()
      expect(readAgents).not.toHaveBeenCalled()

      fireEvent.change(codexPathInput(), { target: { value: after.path } })
      fireEvent.change(codexParams(), { target: { value: after.preset_args } })
      await screen.findByText(/2 unsaved changes/)
      fireEvent.click(saveButton())
      await expectSavedSignal()
      expect(putCalls()).toHaveLength(1)
      expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(expectedSave(after.path, after.preset_args))
      // Do not manually invalidate/refetch or spy on invalidateQueries: both
      // real consumers must obtain the saved values because the Section saved.
      await waitFor(() => {
        expect(client.getQueryData(workerKey)).toEqual(after)
        expect(client.getQueryData(agentsKey)).toEqual([after])
      })
      expect(readWorker).toHaveBeenCalledTimes(1)
      expect(readAgents).toHaveBeenCalledTimes(1)
      expect(checkCalls()).toEqual([])
    } finally {
      stopWorker()
      stopAgents()
    }
  })
  it('sends one PUT containing all three rows exactly as typed', async () => {
    renderSection(); await loaded()
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/1 unsaved change/)
    fireEvent.click(saveButton())
    await waitFor(() => expect(putCalls()).toHaveLength(1))
    expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(expectedSave('/opt/homebrew/bin/codex', PRESET))
    expect(checkCalls()).toEqual([])
  })
  it('puts the refusal under the matching field and preserves the draft (FR-015)', async () => {
    putSettings = async () => json(REFUSAL_BODY, 400)
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/ws/codex/fake' } })
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/2 unsaved changes/)
    fireEvent.click(saveButton())
    // The refusal text renders twice (the field's describedby alert and the
    // outcome line), so assert presence without uniqueness.
    expect((await screen.findAllByText(REFUSAL)).length).toBeGreaterThan(0)
    // FR-015: the server's message under its field, both drafts kept.
    expect(codexPathInput()).toHaveValue('/Users/me/ws/codex/fake')
    expect(codexParams()).toHaveValue(PRESET)
    expect(codexPathInput()).toHaveAccessibleDescription(REFUSAL)
    expect(screen.getByRole('textbox', { name: 'Claude Code program path' })).not.toHaveAccessibleDescription(REFUSAL)
    // FR-015: the bar counts BOTH refused drafts ("2 changes could not be
    // saved") and the reason shows under each setting.
    expect(await screen.findByText(/2 changes could not be saved/)).toBeInTheDocument()
    expect(putCalls()).toHaveLength(1)
    // Backend storage atomicity is proved in the Go test, not by this mock.
  })
  it('allows a corrected save after a refusal without discarding the other draft fields', async () => {
    const successfulPut = putSettings
    let refused = false
    putSettings = async (body) => {
      if (!refused) { refused = true; return json(REFUSAL_BODY, 400) }
      return successfulPut(body)
    }
    renderSection(); await loaded()
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/ws/codex/fake' } })
    await screen.findByText(/2 unsaved changes/)
    fireEvent.click(saveButton())
    expect((await screen.findAllByText(REFUSAL)).length).toBeGreaterThan(0)
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/bin/codex' } })
    fireEvent.click(saveButton())
    await waitFor(() => expect(putCalls()).toHaveLength(2))
    expect(JSON.parse(putCalls()[1].body ?? '')).toEqual(expectedSave('/Users/me/bin/codex', PRESET))
    await expectSavedSignal()
  })
  it('keeps the draft, shows the server message and says the change could not be saved on 401 (FR-015, FR-020 outcome 3)', async () => {
    putSettings = async () => json({ error: 'Session expired' }, 401)
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/bin/unsaved-codex' } })
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/2 unsaved changes/)
    fireEvent.click(saveButton())
    // FR-015: the notice is the server's own message, never invented client
    // copy ("log in again" was the retired section's own wording).
    await waitFor(() => expectFailureNotice(/session expired/i))
    expect(await screen.findByText(/2 changes could not be saved/)).toBeInTheDocument()
    expect(codexPathInput()).toHaveValue('/Users/me/bin/unsaved-codex')
    expect(codexParams()).toHaveValue(PRESET)
    expect(putCalls()).toHaveLength(1)
  })
  it('says saved but not in use for a saved_not_refreshed 500, showing the rows it sent (FR-020 outcome 1, FR-022)', async () => {
    // FR-020 outcome (1) replaces the old echoed-server-sentence oracle: a
    // recognized saved_not_refreshed signal is shown as saved with the spec's
    // message, and FR-022 keeps the rows it sent visible (never a re-read).
    const serverSentence = 'third-party agent settings were saved, but they are not applied yet; ' +
      'save again or restart Omnipus to apply them'
    const response: ErrorResponse = { error: serverSentence, code: 'saved_not_refreshed' }
    putSettings = async () => json(response, 500)
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/bin/saved-codex' } })
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/2 unsaved changes/)
    fireEvent.click(saveButton())

    expect(await screen.findByText(/Saved, but not in use yet\. Restart the gateway to apply it\./)).toBeInTheDocument()
    // FR-018: the bar shows the saved signal (the outcome counts as saved).
    expect(await screen.findByText(/Saved at/)).toBeInTheDocument()
    expect(codexPathInput()).toHaveValue('/Users/me/bin/saved-codex')
    expect(codexParams()).toHaveValue(PRESET)
    expect(putCalls()).toHaveLength(1)
    expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(expectedSave('/Users/me/bin/saved-codex', PRESET))
    expect(publishedMessages()).toEqual([])
    // FR-011: the drafts cleared on the saved outcome, so the bar keeps only
    // the "Saved at" signal — there is no Save changes button left to enable.
    expect(screen.queryByRole('button', { name: /save changes/i })).not.toBeInTheDocument()
  })
  it('a connection-lost Save reads back, says not saved rather than announcing success, and keeps the draft (FR-020 outcome 4)', async () => {
    putSettings = async () => json({ error: 'The server is unavailable. Please try again in a moment.' }, 503)
    renderSection(); await loaded()
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/1 unsaved change/)
    fireEvent.click(saveButton())
    // The unknown result runs the saved-state check; the stored value is
    // unchanged, so the outcome is not saved.
    expect(await screen.findByText(/These changes were not saved\. Save them again\./)).toBeInTheDocument()
    expect(await screen.findByText(/1 change could not be saved/)).toBeInTheDocument()
    expect(codexParams()).toHaveValue(PRESET)
    expect(publishedMessages().some((message) => /saved, but not in use|Saved at/i.test(message))).toBe(false)
    expect(saveButton()).toBeEnabled()
  })
})

describe('§4: loading, saving and accessible interaction', () => {
  it('shows a loading skeleton until the deferred GET resolves', async () => {
    const pending = deferred<Response>()
    getSettings = () => pending.promise
    renderSection()
    expect(screen.getByTestId('third-party-agents-loading')).toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: 'Codex program path' })).not.toBeInTheDocument()
    await act(async () => { pending.resolve(json(stored)) })
    await loaded()
    expect(screen.queryByTestId('third-party-agents-loading')).not.toBeInTheDocument()
  })
  it('shows a load error and Retry reissues the GET', async () => {
    let attempt = 0
    getSettings = async () => ++attempt === 1 ? json({ error: 'Server unavailable' }, 500) : json(stored)
    renderSection()
    const retry = await screen.findByRole('button', { name: /retry/i })
    expect(screen.getByText(/unavailable/i)).toBeInTheDocument()
    fireEvent.click(retry)
    await loaded()
    expect(getCalls()).toHaveLength(2)
  })
  it('keeps Save changes aria-disabled while a deferred PUT is in flight, still focusable, and signals Saved at (FR-013, FR-018)', async () => {
    const pending = deferred<Response>()
    putSettings = () => pending.promise
    renderSection(); await loaded()
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/1 unsaved change/)
    fireEvent.click(saveButton())
    await waitFor(() => expect(putCalls()).toHaveLength(1))
    // FR-013: the bar's Save is aria-disabled while saving but stays in the
    // tab order (the old section button's disabled + aria-busy pair is
    // retired with the section-owned button itself).
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true')
    expect(saveButton()).not.toBeDisabled()
    await act(async () => { pending.resolve(json(stored)) })
    await expectSavedSignal()
  })
  it('makes every path, parameter, Check and Save keyboard reachable and Enter saves', async () => {
    const user = userEvent.setup()
    renderSection(); await loaded()
    const controls = [...screen.getAllByRole('textbox'), ...screen.getAllByRole('button')]
    const seen = new Set<Element>()
    // A bounded keyboard traversal with an assertion for EVERY control: no
    // silent continue or source-level tabIndex check can manufacture green.
    for (let i = 0; i < controls.length * 2; i++) {
      await user.tab()
      if (document.activeElement) seen.add(document.activeElement)
    }
    for (const control of controls) expect(seen.has(control), control.getAttribute('aria-label') ?? control.textContent ?? '').toBe(true)
    // FR-010: the bar (and its Save changes button) appears with the first
    // draft; Enter on the focused button saves.
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/1 unsaved change/)
    saveButton().focus()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(putCalls()).toHaveLength(1))
    expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(expectedSave('/opt/homebrew/bin/codex', PRESET))
    await expectSavedSignal()
  })
})

describe('§4: optional Check CLI', () => {
  it('checks the typed path, publishes its exact version with success/default polarity, and saves nothing', async () => {
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/bin/typed-codex' } })
    fireEvent.click(screen.getByRole('button', { name: 'Check Codex CLI' }))
    await waitFor(() => expect(checkCalls()).toHaveLength(1))
    const expected: CliValidateRequest = { cli: 'codex', cli_path: '/Users/me/bin/typed-codex' }
    expect(JSON.parse(checkCalls()[0].body ?? '')).toEqual(expected)
    await waitFor(() => expectCheckToast('1.2.3', ['success', 'default']))
    expect(putCalls()).toEqual([])
  })
  it('publishes a refused check detail with non-error polarity and preserves the unsaved typed path', async () => {
    // path-not-allowed arrives with Slice A's contract; use the already
    // generated missing-binary refusal to exercise this non-ok state.
    checkCLI = async () => json({ ok: false, reason: 'missing-binary', resolved_path: null, version: null, detail: 'Program not found' })
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/missing/codex' } })
    fireEvent.click(screen.getByRole('button', { name: 'Check Codex CLI' }))
    await waitFor(() => expectCheckToast('Program not found', ['default', 'warning', 'success']))
    expect(codexPathInput()).toHaveValue('/missing/codex')
    expect(putCalls()).toEqual([])
  })
  it('shows a person-gate Check refusal as an error without a transient or retry suggestion', async () => {
    // §3.2 / SF-4 intent: a 403 is a lasting person-only refusal. The spec and
    // user-facing Settings docs prescribe no exact 403 UI sentence, so pin
    // the error class, not implementation copy or the toast host's role mapping.
    const refusal: ErrorResponse = { error: 'A signed-in person is required to check a CLI.', code: 'person_session_required' }
    checkCLI = async () => json(refusal, 403)
    const typedPath = '/Users/me/bin/person-check-codex'
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: typedPath } })
    fireEvent.click(screen.getByRole('button', { name: 'Check Codex CLI' }))
    await waitFor(() => {
      const notices = useUiStore.getState().toasts
      expect(notices).toHaveLength(1)
      expect(notices[0].variant).toBe('error')
      expect(notices[0].message).toMatch(/\S/)
      expect(notices[0].message).not.toMatch(/\b(?:retry\w*|try again|temporar\w*|transient)\b|could(?:n['’]t| not) verify/i)
    })
    expect(checkCalls()).toHaveLength(1)
    const expected: CliValidateRequest = { cli: 'codex', cli_path: typedPath }
    expect(JSON.parse(checkCalls()[0].body ?? '')).toEqual(expected)
    expect(codexPathInput()).toHaveValue(typedPath)
    expect(putCalls()).toEqual([])
  })
  it('shows a failed Check request instead of a version or saved announcement', async () => {
    checkCLI = async () => json({ error: 'The server is unavailable. Please try again in a moment.' }, 503)
    renderSection(); await loaded()
    fireEvent.click(screen.getByRole('button', { name: 'Check Codex CLI' }))
    await waitFor(() => expectFailureNotice(/unavailable/i))
    expect(publishedMessages().some((message) => /1\.2\.3|saved/i.test(message))).toBe(false)
    expect(putCalls()).toEqual([])
  })
  it('load and Save make no spawn-capable HTTP request', async () => {
    renderSection(); await loaded()
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    await screen.findByText(/1 unsaved change/)
    fireEvent.click(saveButton())
    await expectSavedSignal()
    const mutations = calls.filter((c) => c.method !== 'GET')
    expect(mutations.map(({ method, path }) => ({ method, path }))).toEqual([{ method: 'PUT', path: settingsURL }])
    expect(JSON.parse(mutations[0].body ?? '')).toEqual(expectedSave('/opt/homebrew/bin/codex', PRESET))
  })
})
