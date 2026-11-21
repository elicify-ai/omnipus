/**
 * passwordOncePerSave.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 22 (D-11 password outcomes; FR-026–032;
 * BDD "Two password-marked changes need one password", "Wrong password",
 * "Too many password tries", "Cancelling the password prompt keeps every
 * change and any typed key", "Platform mode asks for a confirmation
 * instead", "A late change in the affected agents asks again", "A Save with
 * no password-marked value has no prompt", "The typed password is not kept").
 *
 * Oracles (spec, never code):
 *   - D-11 row 1 / SC-003: right password → exactly ONE POST /auth/reauth
 *     and ONE protected write; both values stored (FR-026/FR-027).
 *   - D-11 row 2 (FR-029): wrong password (401) → "The password is
 *     incorrect." under the field, prompt stays open, focus in the field,
 *     nothing stored.
 *   - D-11 row 3 (FR-029): empty submit → "Enter your password to
 *     continue." with NO request.
 *   - D-11 row 4 (FR-029): 429 → the prompt says too many tries were made
 *     and to wait a minute.
 *   - D-11 row 5 (FR-029): Cancel → nothing sent, every change kept.
 *   - D-11 rows 6+9 (FR-032): platform mode → a confirmation listing the
 *     settings, NO password field, NO POST /auth/reauth; Cancel there sends
 *     0 reauth + 0 writes.
 *   - D-11 row 7: a 73-character password is refused by the server
 *     (ReAuthRequest maxLength 72, bcrypt limit) — the server's message
 *     shows under the field.
 *   - FR-028: after the one POST /auth/reauth returns, the password is
 *     cleared; no later request carries it.
 *
 * The reconciled contract (part 9; the same contract saveBar.counting
 * observes): the REAL Save bar plans the Save; the one password prompt is
 * the shell's, driven by the REAL useSavePasswordPrompt — the real
 * ReAuthDialog in local mode, the real confirmation in platform mode —
 * supplied to the Save bar through SettingsShellContext; the section's
 * write goes out of the harness as a real fetch to the section's one
 * protected endpoint (D-8: Sandbox & network → PUT /security/sandbox-config)
 * carrying the consent token in the X-Reauth-Token header. Everything is
 * counted at the network edge (the fetch stub), never at a vi.fn() seam:
 * an onSave mock would never show a request, and the token/password
 * behaviour (FR-027/FR-028) is a wire fact.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

async function loadHarnessModules() {
  try {
    const [saveBar, shell, prompt] = await Promise.all([
      vi.importActual<any>('@/components/settings/SaveBar'),
      vi.importActual<any>('@/components/settings/SettingsShellContext'),
      vi.importActual<any>('@/components/settings/useSavePasswordPrompt'),
    ])
    return { SaveBar: saveBar.SaveBar ?? saveBar.default, SettingsShellContext: shell.SettingsShellContext, useSavePasswordPrompt: prompt.useSavePasswordPrompt }
  } catch {
    throw new Error(
      'BLOCKED RED (FR-026–FR-032, D-11, order 22): the Settings Save bar / shell context / password prompt ' +
        'do not exist yet — required by FR-026/FR-027 (one password prompt and one protected write per Save)',
    )
  }
}

// C-4 table: Sandbox & network's protected endpoint is PUT /security/sandbox-config.
const PROTECTED_ENDPOINT = '/api/v1/security/sandbox-config'
// Two password-marked changes in Sandbox & network (BDD: Filesystem model and
// Shell workspace limit; FR-025 lists both as Password rows).
const FS_MODEL_FIELD = {
  key: 'sandbox.filesystem_model',
  label: 'Filesystem model',
  value: 'confined',
  kind: 'select',
  options: ['confined', 'open'],
  password: true,
  restart: true,
}
const SHELL_LIMIT_FIELD = {
  key: 'sandbox.workspace_path_guard',
  label: 'Shell workspace limit',
  value: false,
  kind: 'switch',
  password: true,
  restart: true,
}
// A NON-password change in the same section (prompt-guard is unprotected per C-4).
const PLAIN_FIELD = {
  key: 'sandbox.allow_network_outbound',
  label: 'Allow outbound network',
  value: false,
  kind: 'switch',
}

type Call = { url: string; init: RequestInit }
type StubOptions = {
  mode?: 'local' | 'platform'
  reauth?: (url: string) => { status: number; body: unknown }
  write?: (attempt: number, url: string) => { status: number; body: unknown }
}

function stubFetch({ mode = 'local', reauth, write }: StubOptions = {}) {
  const calls: Call[] = []
  let protectedWrites = 0
  const fetchMock = vi.fn(async (input: any, init?: RequestInit) => {
    const url = String(input)
    calls.push({ url, init: init ?? {} })
    // Content-Type matters: ApiError.fromResponse reads the body only for a
    // JSON response, and the D-11 rows assert the SERVER's message.
    // text() must exist too: fromResponse reads the body with res.text()
    // (api-error.ts::fromResponse) — a fake reply carrying only json() loses
    // the body, and the server's message can never show. That missing member
    // is what made D-11 row 7 look like a display-priority defect (fe
    // r6-d11-row7-harness.diff: with text() added, 11/11 pass).
    const respond = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: async () => body,
      text: async () => JSON.stringify(body),
    })
    if (url.includes('/state')) {
      return respond(200, { onboarding_complete: true, identity: { mode, edition: 'hosted', signed_in: true } })
    }
    if (url.includes('/auth/reauth')) {
      const r = reauth?.(url) ?? { status: 200, body: { verified: true, token: 'rt', expires_in: 300 } }
      return respond(r.status, r.body)
    }
    if (url.includes(PROTECTED_ENDPOINT)) {
      protectedWrites += 1
      const w = write?.(protectedWrites, url) ?? { status: 200, body: {} }
      return respond(w.status, w.body)
    }
    return respond(200, {})
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}

const callsTo = (calls: Call[], fragment: string) => calls.filter((c) => c.url.includes(fragment))

/**
 * Renders the REAL Save bar with the REAL prompt machinery at the shell
 * boundary; the section write leaves as a real PUT with the consent token
 * in X-Reauth-Token (FR-027) — counted at the fetch stub.
 */
async function renderAndSave(fields: any[]) {
  const { SaveBar, SettingsShellContext, useSavePasswordPrompt } = await loadHarnessModules()
  const Harness = ({ onSave }: { onSave: any }) => {
    const { requestPassword, dialog } = useSavePasswordPrompt()
    return (
      <SettingsShellContext.Provider
        value={{
          announce: () => {},
          setDraftCount: () => {},
          offerRestart: () => {},
          refreshPendingRestart: () => {},
          requestPassword,
          saveBarVisible: false,
          setSaveBarVisible: () => {},
        }}
      >
        <SaveBar section="sandbox" fields={fields} onSave={onSave} onDiscard={vi.fn()} />
        {dialog}
      </SettingsShellContext.Provider>
    )
  }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const onSave = vi.fn(async (body: Record<string, unknown>, ctx: { reauthToken?: string }) => {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (ctx.reauthToken) headers['X-Reauth-Token'] = ctx.reauthToken
    const resp = await fetch(PROTECTED_ENDPOINT, { method: 'PUT', headers, body: JSON.stringify(body) })
    const status = resp.status
    // Forward the response BODY: the refusal message (FR-020 outcome 3) is
    // the server's, and discarding it would hide the very line the Save bar
    // must show under the field.
    const respBody = await resp.json().catch(() => ({}))
    return { ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), json: async () => respBody }
  })
  render(
    <QueryClientProvider client={client}>
      <Harness onSave={onSave} />
    </QueryClientProvider>,
  )
  for (const f of fields) {
    if (f.kind === 'select') {
      await userEvent.selectOptions(screen.getByLabelText(f.label), String(f.options[1]))
    } else {
      await userEvent.click(screen.getByRole('switch', { name: f.label }))
    }
  }
  await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  // The reauth POST and the protected PUT are state-changing: the client-side
  // CSRF gate needs the session cookie planted (ProvidersSection.customModels
  // precedent), else request() throws before fetch and nothing is countable.
  document.cookie = 'csrf=password-once-test; Path=/'
})

afterEach(() => {
  document.cookie = 'csrf=; Path=/; Max-Age=0'
  vi.unstubAllGlobals()
})

describe('D-11 — one password prompt per Save', () => {
  it('two password-marked changes: exactly one prompt listing both labels, one POST /auth/reauth, one protected write', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD])
    // The prompt lists both settings by visible label (FR-026); the list is
    // scoped to the dialog's items box because the section's own row labels
    // say the same words (locator fix; oracle unchanged).
    const items = await screen.findByTestId('reauth-items')
    expect(within(items).getByText('Filesystem model')).toBeInTheDocument()
    expect(within(items).getByText('Shell workspace limit')).toBeInTheDocument()
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'correct horse')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(1)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1)
    // The consent token rides the one protected write (FR-027).
    expect(callsTo(calls, PROTECTED_ENDPOINT)[0].init.headers).toHaveProperty('X-Reauth-Token', 'rt')
    expect(screen.queryByText(/password is incorrect/i)).not.toBeInTheDocument()
  })

  it('a wrong password keeps the prompt open with the exact message, focus in the field, nothing stored', async () => {
    const calls = stubFetch({ reauth: () => ({ status: 401, body: { error: 'invalid password' } }) })
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'wrong')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(await screen.findByText('The password is incorrect.')).toBeInTheDocument()
    // FR-029: focus is in the field. The dialog's accessible NAME also
    // matches /password/i, so the field is addressed by its testid.
    expect(screen.getByTestId('reauth-password-input')).toHaveFocus()
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('an empty submit says "Enter your password to continue." and sends no request', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(await screen.findByText('Enter your password to continue.')).toBeInTheDocument()
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
  })

  it('a rate-limited reauth (429) says too many tries were made and to wait a minute', async () => {
    const calls = stubFetch({ reauth: () => ({ status: 429, body: { error: 'rate limited' } }) })
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'whatever')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(await screen.findByText(/too many tries/i)).toBeInTheDocument()
    expect(screen.getByText(/wait a minute/i)).toBeInTheDocument()
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('Cancel stores nothing and keeps every change (D-11 row 5)', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD])
    await userEvent.click(screen.getByTestId('reauth-cancel'))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
    // The drafts stay: the fields still hold their changed values.
    expect(screen.getByLabelText('Filesystem model')).toHaveValue('open')
    expect(screen.getByRole('switch', { name: 'Shell workspace limit' })).toBeChecked()
  })

  it('platform mode shows a confirmation with no password field and sends no reauth (FR-032)', async () => {
    const calls = stubFetch({ mode: 'platform' })
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD])
    // The platform confirmation is a different dialog ("Save these settings?");
    // waiting for it proves the mode resolved before the no-password-field
    // assertion (fail-toward-stronger-control otherwise renders the dialog).
    expect(await screen.findByText('Save these settings?')).toBeInTheDocument()
    expect(screen.getByText(/This saves: Filesystem model, Shell workspace limit\./)).toBeInTheDocument()
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
    // Role-name matching with a plain string is already full-string equality,
// so 'Save' cannot hit the bar's 'Save changes'.
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1)
  })

  it('platform mode Cancel sends 0 reauth and 0 writes (D-11 row 9)', async () => {
    const calls = stubFetch({ mode: 'platform' })
    await renderAndSave([FS_MODEL_FIELD])
    expect(await screen.findByText('Save these settings?')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('a 73-character password shows the server refusal (ReAuthRequest maxLength 72)', async () => {
    const calls = stubFetch({ reauth: () => ({ status: 400, body: { error: 'password: Must be at most 72 characters long' } }) })
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'x'.repeat(73))
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(await screen.findByText(/at most 72 characters/i)).toBeInTheDocument()
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('a Save with no password-marked value opens no prompt and sends no reauth (FR-026 negative)', async () => {
    const calls = stubFetch()
    await renderAndSave([PLAIN_FIELD])
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1) // the plain write went out unprotected
    expect(callsTo(calls, PROTECTED_ENDPOINT)[0].init.headers).not.toHaveProperty('X-Reauth-Token')
  })

  it('after the reauth returns, no later request carries the password (FR-028)', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'correct horse')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1)
    // The typed password travels only in the reauth request; the protected
    // write carries the TOKEN, never the password (FR-028/FR-027).
    const reauthBodies = callsTo(calls, '/auth/reauth').map((c) => String(c.init?.body ?? ''))
    expect(reauthBodies.join(' ')).toContain('correct horse')
    const bodies = callsTo(calls, PROTECTED_ENDPOINT).map((c) => String(c.init?.body ?? ''))
    expect(bodies.join(' ')).not.toContain('correct horse')
    // The prompt closed and its field is gone.
    expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
  })
})

// FR-028 (spec:1933): "The typed password MUST be cleared from the prompt
// and from page state AS SOON AS the one POST /auth/reauth returns, whatever
// its result." CHECK F02/M10: removing the success wipe left these tests
// green, because none of them observed the prompt's own state — only the
// wire and the closed field. The prompt component stays mounted, so the
// observables are: the reopened prompt's field after a 200, and the field
// of the still-open prompt after a 401 and a 429.
describe('FR-028 — the typed password is cleared whatever the reauth returns', () => {
  it('after a 200: the password is gone from page state — a second Save reopens an EMPTY prompt, and its fresh consent writes once (FR-028, FR-026)', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'correct horse')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    await waitFor(() => expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1))
    expect(callsTo(calls, PROTECTED_ENDPOINT)[0].init.headers).toHaveProperty('X-Reauth-Token', 'rt')

    // A second, independent Save: its prompt is the SAME still-mounted
    // dialog component. A retained password would sit in its field. (The
    // second change drafts on the already-saved field: 'open' was just
    // stored, so 'confined' is a new change against it.)
    await userEvent.selectOptions(screen.getByLabelText('Filesystem model'), 'confined')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    const reopened = await screen.findByTestId('reauth-password-input')
    expect(reopened).toHaveValue('')
    // The second consent is spent on its own write: still one prompt per
    // Save, one token per write, and no request carries the first password.
    await userEvent.type(reopened, 'second horse')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    await waitFor(() => expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(2))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(2)
    const bodies = callsTo(calls, PROTECTED_ENDPOINT).map((c) => String(c.init?.body ?? ''))
    expect(bodies.join(' ')).not.toContain('correct horse')
    expect(bodies.join(' ')).not.toContain('second horse')
  })

  it('after a 401: the prompt stays open with its message and focus, and the field it refocuses is EMPTY (FR-028, FR-029)', async () => {
    const calls = stubFetch({ reauth: () => ({ status: 401, body: { error: 'invalid password' } }) })
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'wrong')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(await screen.findByText('The password is incorrect.')).toBeInTheDocument()
    expect(screen.getByTestId('reauth-password-input')).toHaveFocus()
    // FR-028 "whatever its result": the refusal clears the typed password —
    // the person retypes, the old attempt is not left in the field.
    expect(screen.getByTestId('reauth-password-input')).toHaveValue('')
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
    // The empty field is still usable: retyping the right password goes
    // through (FR-029's retry is compatible with a cleared field).
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'correct horse')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    await waitFor(() => expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1))
    expect(callsTo(calls, PROTECTED_ENDPOINT)[0].init.headers).toHaveProperty('X-Reauth-Token', 'rt')
  })

  it('after a 429: the prompt says to wait, keeps focus, and the field is EMPTY (FR-028, FR-029)', async () => {
    const calls = stubFetch({ reauth: () => ({ status: 429, body: { error: 'rate limited' } }) })
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'whatever')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    expect(await screen.findByText(/too many tries/i)).toBeInTheDocument()
    expect(screen.getByText(/wait a minute/i)).toBeInTheDocument()
    // FR-028 "whatever its result": the rate-limit refusal clears the field too.
    expect(screen.getByTestId('reauth-password-input')).toHaveValue('')
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })
})

describe('D-11 row 8 / FR-031 — a late change in the affected agents asks again', () => {
  it('a protected write answered 409 is a refusal: the draft stays, the server message shows, and no second consent happens without a fresh Save (FR-020 outcome 3)', async () => {
    // The affected-agents re-ask flow itself (409 drift → new list shown → a
    // second POST /auth/reauth + a second protected write) belongs to the
    // lowering flow that owns that contract — the spec re-homes it there
    // (US-6 AS-6/D16, pinned by PerformanceSection.maxToolIterations.test and
    // PerformanceSection.toolIterations.test); the generic Save bar has no
    // affected-agents contract of its own. What THIS file pins at the prompt
    // boundary: a 409 without a new consent is one refusal — exactly one
    // reauth, one write, no silent second attempt.
    const calls = stubFetch({ write: (attempt) => (attempt === 1 ? { status: 409, body: { error: 'affected agents changed' } } : { status: 200, body: {} }) })
    await renderAndSave([SHELL_LIMIT_FIELD])
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'correct horse')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    // Exactly one consent was spent; the 409 refusal keeps the draft and
    // surfaces the server's message instead of asking or writing again.
    expect(await screen.findByText(/affected agents changed/i)).toBeInTheDocument()
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(1)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1)
    expect(screen.getByRole('switch', { name: 'Shell workspace limit' })).toBeChecked()
  })
})
