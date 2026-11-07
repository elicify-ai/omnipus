/** T11-B §4 / FR-6, FR-7. Oracles are the spec and generated T8 contract.
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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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
  details: { cli: 'codex', check: 'agent_writable_location', at: '/Users/me/ws/codex/fake' },
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
const saveButton = () => screen.getByRole('button', { name: /^save$/i })

function renderSection() {
  return render(<QueryClientProvider client={client}><Section /></QueryClientProvider>)
}
async function loaded() {
  await screen.findByRole('textbox', { name: 'Codex program path' })
}
function expectSavedToast() {
  // §4 calls the state "Saved". No longer sentence is specified. The
  // dispatcher fixed this observation boundary and non-error polarity.
  const toasts = useUiStore.getState().toasts
  expect(toasts).toHaveLength(1)
  expect(toasts[0].message).toContain('Saved')
  expect(toasts[0].variant).not.toBe('error')
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
  it('renders exactly three CLI-qualified path/parameter/Check rows and one Save', async () => {
    renderSection(); await loaded()
    for (const name of ['Claude Code', 'Codex', 'opencode']) {
      expect(screen.getByRole('textbox', { name: `${name} program path` })).toBeEnabled()
      expect(screen.getByRole('textbox', { name: `${name} parameters` })).toBeEnabled()
      expect(screen.getByRole('button', { name: `Check ${name} CLI` })).toBeEnabled()
    }
    expect(screen.getAllByRole('button', { name: /^save$/i })).toHaveLength(1)
    expect(screen.getAllByRole('textbox')).toHaveLength(6)
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
  it('sends one PUT containing all three rows exactly as typed', async () => {
    renderSection(); await loaded()
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    fireEvent.click(saveButton())
    await waitFor(() => expect(putCalls()).toHaveLength(1))
    expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(expectedSave('/opt/homebrew/bin/codex', PRESET))
    expect(checkCalls()).toEqual([])
  })
  it('puts refusal under the matching field, preserves the draft and moves focus there', async () => {
    putSettings = async () => json(REFUSAL_BODY, 400)
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/ws/codex/fake' } })
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    fireEvent.click(saveButton())
    expect(await screen.findByText(REFUSAL)).toBeInTheDocument()
    expect(codexPathInput()).toHaveValue('/Users/me/ws/codex/fake')
    expect(codexParams()).toHaveValue(PRESET)
    expect(codexPathInput()).toHaveAccessibleDescription(REFUSAL)
    expect(screen.getByRole('textbox', { name: 'Claude Code program path' })).not.toHaveAccessibleDescription(REFUSAL)
    await waitFor(() => expect(codexPathInput()).toHaveFocus())
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
    fireEvent.click(saveButton())
    await screen.findByText(REFUSAL)
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/bin/codex' } })
    fireEvent.click(saveButton())
    await waitFor(() => expect(putCalls()).toHaveLength(2))
    expect(JSON.parse(putCalls()[1].body ?? '')).toEqual(expectedSave('/Users/me/bin/codex', PRESET))
    await waitFor(expectSavedToast)
  })
  it('keeps the draft and says sign in again when Save returns 401', async () => {
    putSettings = async () => json({ error: 'Session expired' }, 401)
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/bin/unsaved-codex' } })
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    fireEvent.click(saveButton())
    await waitFor(() => expectFailureNotice(/sign in again/i))
    expect(codexPathInput()).toHaveValue('/Users/me/bin/unsaved-codex')
    expect(codexParams()).toHaveValue(PRESET)
    expect(putCalls()).toHaveLength(1)
  })
  it('shows the exact saved-but-not-applied server notice for a saved_not_refreshed 500', async () => {
    // Round-3 dispatch: display the server sentence exactly, without falsely
    // saying the durable save failed. This is response INPUT, not UI output
    // copied into an oracle; exact input-to-visible-message identity is required.
    const serverSentence = 'third-party agent settings were saved, but they are not applied yet; ' +
      'save again or restart Omnipus to apply them'
    const response: ErrorResponse = { error: serverSentence, code: 'saved_not_refreshed' }
    putSettings = async () => json(response, 500)
    renderSection(); await loaded()
    fireEvent.change(codexPathInput(), { target: { value: '/Users/me/bin/saved-codex' } })
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    fireEvent.click(saveButton())

    const notice = await screen.findByRole('alert')
    expect(notice).toBeVisible()
    expect(notice.textContent).toBe(serverSentence)
    expect(notice).not.toHaveTextContent('Could not save:')
    expect(codexPathInput()).toHaveValue('/Users/me/bin/saved-codex')
    expect(codexParams()).toHaveValue(PRESET)
    expect(putCalls()).toHaveLength(1)
    expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(expectedSave('/Users/me/bin/saved-codex', PRESET))
    expect(publishedMessages()).toEqual([])
    expect(saveButton()).toBeEnabled()
  })
  it('shows a failed save rather than announcing success and keeps the draft', async () => {
    putSettings = async () => json({ error: 'The server is unavailable. Please try again in a moment.' }, 503)
    renderSection(); await loaded()
    fireEvent.change(codexParams(), { target: { value: PRESET } })
    fireEvent.click(saveButton())
    await waitFor(() => expectFailureNotice(/unavailable/i))
    expect(codexParams()).toHaveValue(PRESET)
    expect(publishedMessages().some((message) => /saved/i.test(message))).toBe(false)
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
  it('keeps Save busy while a deferred PUT is in flight and publishes a non-error completion toast', async () => {
    const pending = deferred<Response>()
    putSettings = () => pending.promise
    renderSection(); await loaded()
    fireEvent.click(saveButton())
    await waitFor(() => expect(putCalls()).toHaveLength(1))
    expect(saveButton()).toBeDisabled()
    expect(saveButton()).toHaveAttribute('aria-busy', 'true')
    await act(async () => { pending.resolve(json(stored)) })
    await waitFor(expectSavedToast)
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
    saveButton().focus()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(putCalls()).toHaveLength(1))
    expect(JSON.parse(putCalls()[0].body ?? '')).toEqual(expectedSave())
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
    fireEvent.click(saveButton())
    await waitFor(expectSavedToast)
    const mutations = calls.filter((c) => c.method !== 'GET')
    expect(mutations.map(({ method, path }) => ({ method, path }))).toEqual([{ method: 'PUT', path: settingsURL }])
    expect(JSON.parse(mutations[0].body ?? '')).toEqual(expectedSave())
  })
})
