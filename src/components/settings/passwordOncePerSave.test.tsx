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
 *   - D-11 row 1: right password → exactly ONE POST /auth/reauth and ONE
 *     protected write; both values stored (FR-026/FR-027).
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
 *   - D-11 row 8 (FR-031): a protected write answering 409 with a CHANGED
 *     affected-agents list → the new list is shown, the password is asked
 *     again (a second POST /auth/reauth), and no other write of the Save
 *     was sent before the second consent.
 *   - FR-028: after the one POST /auth/reauth returns, the password is
 *     cleared; no later request carries it.
 *
 * The SaveBar (which owns the per-Save password flow) does not exist yet —
 * BLOCKED-import pattern of rows 20/21. Requests are counted at the fetch
 * stub (the network edge); the password-marked fields carry `password: true`
 * (same field contract as saveBar.counting.test.tsx).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

async function loadSaveBar(): Promise<any> {
  try {
    const mod: any = await vi.importActual('@/components/settings/SaveBar')
    return mod.SaveBar ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-026–FR-032, D-11, order 22): the Settings SaveBar component does not exist yet — ' +
        'required by FR-026/FR-027 (one password prompt and one protected write per Save)',
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
function stubFetch() {
  const calls: Call[] = []
  const fetchMock = vi.fn(async (input: any, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} })
    const url = String(input)
    if (url.includes('/auth/reauth')) {
      return { ok: true, status: 200, json: async () => ({ token: 'rt' }) }
    }
    return { ok: true, status: 200, json: async () => ({}) }
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}

const callsTo = (calls: Call[], fragment: string) => calls.filter((c) => c.url.includes(fragment))

async function renderAndSave(fields: any[], props: Record<string, unknown> = {}) {
  const SaveBar = await loadSaveBar()
  render(<SaveBar section="sandbox" fields={fields} onSave={vi.fn()} onDiscard={vi.fn()} {...props} />)
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
})

describe('D-11 — one password prompt per Save', () => {
  it('two password-marked changes: exactly one prompt listing both labels, one POST /auth/reauth, one protected write', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD])
    // The prompt lists both settings by visible label (FR-026).
    expect(await screen.findByText('Filesystem model')).toBeInTheDocument()
    expect(screen.getByText('Shell workspace limit')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText(/password/i), 'correct horse')
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(1)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1)
    expect(screen.queryByText(/password is incorrect/i)).not.toBeInTheDocument()
  })

  it('a wrong password keeps the prompt open with the exact message, focus in the field, nothing stored', async () => {
    const calls = stubFetch()
    vi.stubGlobal('fetch', vi.fn(async (input: any, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} })
      if (String(input).includes('/auth/reauth')) {
        return { ok: false, status: 401, json: async () => ({ error: 'invalid password' }) }
      }
      return { ok: true, status: 200, json: async () => ({}) }
    }))
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD])
    await userEvent.type(screen.getByLabelText(/password/i), 'wrong')
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    expect(await screen.findByText('The password is incorrect.')).toBeInTheDocument()
    expect(screen.getByLabelText(/password/i)).toHaveFocus()
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('an empty submit says "Enter your password to continue." and sends no request', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    expect(await screen.findByText('Enter your password to continue.')).toBeInTheDocument()
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
  })

  it('a rate-limited reauth (429) says too many tries were made and to wait a minute', async () => {
    const calls = stubFetch()
    vi.stubGlobal('fetch', vi.fn(async (input: any, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} })
      if (String(input).includes('/auth/reauth')) {
        return { ok: false, status: 429, json: async () => ({ error: 'rate limited' }) }
      }
      return { ok: true, status: 200, json: async () => ({}) }
    }))
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByLabelText(/password/i), 'whatever')
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    expect(await screen.findByText(/too many tries/i)).toBeInTheDocument()
    expect(screen.getByText(/wait/i)).toBeInTheDocument()
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('Cancel stores nothing and keeps every change (D-11 row 5)', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD])
    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
    // The drafts stay: the fields still hold their changed values.
    expect(screen.getByLabelText('Filesystem model')).toHaveValue('open')
    expect(screen.getByRole('switch', { name: 'Shell workspace limit' })).toBeChecked()
  })

  it('platform mode shows a confirmation with no password field and sends no reauth (FR-032)', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD, SHELL_LIMIT_FIELD], { platformMode: true })
    expect(await screen.findByText('Filesystem model')).toBeInTheDocument()
    expect(screen.getByText('Shell workspace limit')).toBeInTheDocument()
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1)
  })

  it('platform mode Cancel sends 0 reauth and 0 writes (D-11 row 9)', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD], { platformMode: true })
    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('a 73-character password shows the server refusal (ReAuthRequest maxLength 72)', async () => {
    const calls = stubFetch()
    vi.stubGlobal('fetch', vi.fn(async (input: any, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} })
      if (String(input).includes('/auth/reauth')) {
        return {
          ok: false,
          status: 400,
          json: async () => ({ error: 'password: Must be at most 72 characters long' }),
        }
      }
      return { ok: true, status: 200, json: async () => ({}) }
    }))
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByLabelText(/password/i), 'x'.repeat(73))
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    expect(await screen.findByText(/at most 72 characters/i)).toBeInTheDocument()
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(0)
  })

  it('a Save with no password-marked value opens no prompt and sends no reauth (FR-026 negative)', async () => {
    const calls = stubFetch()
    await renderAndSave([PLAIN_FIELD])
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(0)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1) // the plain write went out unprotected
  })

  it('after the reauth returns, no later request carries the password (FR-028)', async () => {
    const calls = stubFetch()
    await renderAndSave([FS_MODEL_FIELD])
    await userEvent.type(screen.getByLabelText(/password/i), 'correct horse')
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(1)
    const bodies = callsTo(calls, PROTECTED_ENDPOINT).map((c) => String(c.init?.body ?? ''))
    expect(bodies.join(' ')).not.toContain('correct horse')
    // The prompt closed and its field is gone or emptied.
    const field = screen.queryByLabelText(/password/i)
    expect(field === null || (field as HTMLInputElement).value === '').toBe(true)
  })
})

describe('D-11 row 8 / FR-031 — a late change in the affected agents asks again', () => {
  it('a 409 with a changed affected-agents list shows the new list and asks for the password again, with no other write first', async () => {
    const calls = stubFetch()
    let protectedWrites = 0
    vi.stubGlobal('fetch', vi.fn(async (input: any, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} })
      const url = String(input)
      if (url.includes('/auth/reauth')) {
        return { ok: true, status: 200, json: async () => ({ token: 'rt' }) }
      }
      if (url.includes(PROTECTED_ENDPOINT)) {
        protectedWrites += 1
        if (protectedWrites === 1) {
          return {
            ok: false,
            status: 409,
            json: async () => ({
              error: 'affected agents changed',
              details: { agents: [{ id: 'mia', name: 'Mia' }, { id: 'gp', name: 'General Purpose' }] },
            }),
          }
        }
      }
      return { ok: true, status: 200, json: async () => ({}) }
    }))
    await renderAndSave([SHELL_LIMIT_FIELD])
    // First consent happened; the protected write answered 409 with the new list.
    await screen.findByText(/Mia/).catch(() => {
      throw new Error(
        'BLOCKED RED (FR-031, D-11 row 8): the changed affected-agents list must be shown for confirmation',
      )
    })
    expect(screen.getByText(/General Purpose/)).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText(/password/i), 'correct horse')
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))
    // Second consent: one more reauth, one more protected write.
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(2)
    expect(callsTo(calls, PROTECTED_ENDPOINT)).toHaveLength(2)
  })
})
