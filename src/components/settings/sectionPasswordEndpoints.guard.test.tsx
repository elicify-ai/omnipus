/**
 * sectionPasswordEndpoints.guard.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 23 (FR-030, Dataset D-8; BDD "Each section
 * sends its password-marked values to at most one protected endpoint").
 *
 * Oracles (spec, never code):
 *   - D-8: with every password-marked value of a section changed, the Save
 *     sends EXACTLY ONE protected write, to the section's one protected
 *     endpoint — Providers: 1 × PUT /integrations/providers/{id}; Limits:
 *     1 × PUT /performance; Protection: 1 × PUT /security/sandbox-config;
 *     Sandbox & network: 1 × PUT /security/sandbox-config; Memory & recaps,
 *     System agents, Third-party agents, Keys & secrets, Gateway, Data &
 *     retention, About: 0 protected writes (their buttons act alone).
 *   - FR-030: the guard runs the REAL save planner and FAILS when any
 *     section's password-marked values reach more than one protected
 *     endpoint; an INJECTED second protected endpoint must make it fail
 *     (positive control — the guard must be able to fail).
 *
 * The save planner does not exist yet — BLOCKED-import pattern. Proposed
 * module contract (GREEN may adjust names, not oracles):
 * `@/components/settings/savePlanner` exports `planSectionSave(section,
 * fields)` → PlannedWrite[] ({ endpoint, method, password, body }) and
 * `assertOneProtectedEndpointPerSection(table)`.
 * Counts for the four password sections are ALSO taken at the network mock
 * while the real planner runs (D-8 note), through the SaveBar; sections with
 * no password values are counted at the planner level only (their screens
 * need full data mocks — see TEST-PLAN-PART2.md gaps).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

async function loadSavePlanner(): Promise<any> {
  try {
    return await vi.importActual('@/components/settings/savePlanner')
  } catch {
    throw new Error(
      'BLOCKED RED (FR-030, D-8, order 23): the Settings save planner does not exist yet — ' +
        'required by FR-030 (the guard runs the real save planner and counts protected endpoints per section)',
    )
  }
}

async function loadSaveBar(): Promise<any> {
  try {
    const mod: any = await vi.importActual('@/components/settings/SaveBar')
    return mod.SaveBar ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-030, D-8, order 23): the Settings SaveBar component does not exist yet — ' +
        'required by FR-030 (network-mock counting while the real planner runs)',
    )
  }
}

// D-8 sections and their password-marked rows (FR-025 badge list; C-4 table).
const D8_PASSWORD_SECTIONS: Array<{ section: string; endpoint: string; fields: any[] }> = [
  {
    section: 'providers',
    endpoint: '/api/v1/integrations/providers/',
    fields: [{ key: 'integrations.transcriber.openai_whisper', label: 'Active transcriber', value: 'groq', kind: 'select', options: ['openai_whisper', 'groq'], password: true }],
  },
  {
    section: 'limits',
    endpoint: '/api/v1/performance',
    fields: [
      { key: 'performance.max_parallel_agents', label: 'Max parallel agents', value: 4, kind: 'text', password: true },
      { key: 'agents.defaults.max_tool_iterations', label: 'Max tool calls per turn', value: 40, kind: 'text', password: true },
      { key: 'planning.goal_max_rounds', label: 'Tries per goal', value: 8, kind: 'text', password: true },
      { key: 'planning.goal_reminder_quiet_seconds', label: 'Idle reminder quiet window', value: 120, kind: 'text', password: true },
      { key: 'planning.goal_stalled_checkin_enabled', label: 'Check-in switch', value: true, kind: 'switch', password: true },
      { key: 'planning.goal_stalled_checkin_minutes', label: 'Check-in interval', value: 10, kind: 'text', password: true },
    ],
  },
  {
    section: 'protection',
    endpoint: '/api/v1/security/sandbox-config',
    fields: [{ key: 'sandbox.auto_approve', label: 'Auto-approve', value: true, kind: 'switch', password: true }],
  },
  {
    section: 'sandbox',
    endpoint: '/api/v1/security/sandbox-config',
    fields: [
      { key: 'sandbox.mode', label: 'Sandbox mode', value: 'enforce', kind: 'select', options: ['off', 'permissive', 'enforce'], password: true, restart: true },
      { key: 'sandbox.filesystem_model', label: 'Filesystem model', value: 'open', kind: 'select', options: ['confined', 'open'], password: true, restart: true },
      { key: 'sandbox.workspace_path_guard', label: 'Shell workspace limit', value: true, kind: 'switch', password: true, restart: true },
      { key: 'sandbox.allowed_paths', label: 'Allowed folders', value: '/srv/data', kind: 'text', password: true, restart: true },
      { key: 'sandbox.ssrf.allow_internal', label: 'Internal network addresses', value: '10.0.0.0/8', kind: 'text', password: true },
    ],
  },
]

// D-8 row 5: sections with no password values (their buttons act alone).
const D8_NO_PASSWORD_SECTIONS = [
  'memory',
  'system-agents',
  'third-party-agents',
  'keys',
  'gateway',
  'data',
  'about',
]

// Part 9 fix (the same contract passwordOncePerSave.test observes): the
// section write leaves the harness as a REAL fetch to the section's one
// protected endpoint carrying the consent token in X-Reauth-Token, and
// every count is taken at the network edge (this stub) — never at a vi.fn()
// seam. The old onSave={vi.fn()} harness could never show a request, so its
// count of 1 could only have come from the reauth stub. Content-Type
// matters: ApiError.fromResponse reads the body only for a JSON response.
type Call = { url: string; init: RequestInit }
function stubFetch() {
  const calls: Call[] = []
  const fetchMock = vi.fn(async (input: any, init?: RequestInit) => {
    const url = String(input)
    calls.push({ url, init: init ?? {} })
    const respond = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: async () => body,
    })
    if (url.includes('/state')) {
      return respond(200, { onboarding_complete: true, identity: { mode: 'local', edition: 'core', signed_in: true } })
    }
    if (url.includes('/auth/reauth')) {
      return respond(200, { verified: true, token: 'rt', expires_in: 300 })
    }
    return respond(200, {})
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}

const callsTo = (calls: Call[], fragment: string) => calls.filter((c) => c.url.includes(fragment))

async function loadHarnessModules(): Promise<any> {
  try {
    const [saveBar, shell, prompt] = await Promise.all([
      vi.importActual<any>('@/components/settings/SaveBar'),
      vi.importActual<any>('@/components/settings/SettingsShellContext'),
      vi.importActual<any>('@/components/settings/useSavePasswordPrompt'),
    ])
    return { SaveBar: saveBar.SaveBar ?? saveBar.default, SettingsShellContext: shell.SettingsShellContext, useSavePasswordPrompt: prompt.useSavePasswordPrompt }
  } catch {
    throw new Error(
      'BLOCKED RED (FR-030, D-8, order 23): the Settings Save bar / shell context / password prompt do not ' +
        'exist yet — required for the network-edge counting while the real planner runs',
    )
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  // The reauth POST and the protected PUT are state-changing: the client-side
  // CSRF gate needs the session cookie planted (ProvidersSection.customModels
  // precedent), else request() throws before fetch and nothing is countable.
  document.cookie = 'csrf=guard-d8-test; Path=/'
})

afterEach(() => {
  document.cookie = 'csrf=; Path=/; Max-Age=0'
  vi.unstubAllGlobals()
})

describe('D-8 — the real save planner: at most one protected endpoint per section', () => {
  it.each(D8_PASSWORD_SECTIONS.map((s) => [s.section, s] as const))(
    '%s: exactly one protected write to its one endpoint',
    async (_name, spec) => {
      const planner: any = await loadSavePlanner()
      const writes: any[] = planner.planSectionSave(spec.section, spec.fields)
      const protectedWrites = writes.filter((w) => w.password)
      expect(protectedWrites.length, `BLOCKED RED (D-8): ${spec.section} must send exactly one protected write`).toBe(1)
      expect(protectedWrites[0].endpoint).toContain(spec.endpoint)
    },
  )

  it.each(D8_NO_PASSWORD_SECTIONS.map((s) => [s] as const))(
    '%s: no protected write at all (D-8 row 5)',
    async (section) => {
      const planner: any = await loadSavePlanner()
      const writes: any[] = planner.planSectionSave(section, [
        { key: `${section}.example`, label: 'Example', value: 'changed', kind: 'text' },
      ])
      expect(writes.filter((w) => w.password).length,
        `BLOCKED RED (D-8 row 5): ${section} carries no password values`).toBe(0)
    },
  )

  it('the whole planner table passes the guard (every section has at most one protected endpoint)', async () => {
    const planner: any = await loadSavePlanner()
    expect(() => {
      for (const spec of D8_PASSWORD_SECTIONS) {
        planner.assertOneProtectedEndpointPerSection({ [spec.section]: [spec.endpoint] })
      }
      for (const section of D8_NO_PASSWORD_SECTIONS) {
        planner.assertOneProtectedEndpointPerSection({ [section]: [] })
      }
    }).not.toThrow()
  })
})

describe('FR-030 positive control — an injected second protected endpoint must fail the guard', () => {
  it('a section with two protected endpoints makes the guard fail', async () => {
    const planner: any = await loadSavePlanner()
    const injected = { gateway: ['/api/v1/config', '/api/v1/security/sandbox-config'] }
    expect(() => planner.assertOneProtectedEndpointPerSection(injected)).toThrow(
      /more than one protected endpoint|at most one protected endpoint/i,
    )
  })
})

describe('D-8 note — counts taken at the network mock while the real planner runs', () => {
  // Renders the REAL Save bar with the REAL prompt machinery at the shell
  // boundary; the section write leaves as a real PUT to the section's D-8
  // endpoint with the consent token (FR-027), counted at the fetch stub.
  // The FE SaveBar has no 'paths' field kind (part 6/9 harness facts), so
  // the path-list fixtures use the text kind above; the D-8 oracle —
  // exactly ONE protected write per section — does not depend on the kind.
  async function renderSaveAndCount(spec: (typeof D8_PASSWORD_SECTIONS)[number], calls: Call[]) {
    const { SaveBar, SettingsShellContext, useSavePasswordPrompt } = await loadHarnessModules()
    const onSave = vi.fn(async (body: Record<string, unknown>, ctx: { reauthToken?: string }) => {
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (ctx.reauthToken) headers['X-Reauth-Token'] = ctx.reauthToken
      const resp = await fetch(spec.endpoint, { method: 'PUT', headers, body: JSON.stringify(body) })
      const respBody = await resp.json().catch(() => ({}))
      return { ok: resp.status >= 200 && resp.status < 300, status: resp.status, headers: new Headers({ 'Content-Type': 'application/json' }), json: async () => respBody }
    })
    const Harness = () => {
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
          <SaveBar section={spec.section} fields={spec.fields} onSave={onSave} onDiscard={vi.fn()} />
          {dialog}
        </SettingsShellContext.Provider>
      )
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <Harness />
      </QueryClientProvider>,
    )
    for (const f of spec.fields) {
      if (f.kind === 'select') {
        // Pick a value that actually DIFFERS from the field's saved value —
        // for providers the fixture's saved value IS options[1], and
        // selecting it again is a no-op that drafts nothing (FR-010: the bar
        // counts changes, not selections).
        const next = f.options.find((o: unknown) => String(o) !== String(f.value)) ?? f.options[1]
        await userEvent.selectOptions(screen.getByLabelText(f.label), String(next))
      } else if (f.kind === 'switch') {
        await userEvent.click(screen.getByRole('switch', { name: f.label }))
      } else {
        await userEvent.type(screen.getByLabelText(f.label), '9')
      }
    }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // The real ReAuthDialog exposes stable testids (passwordOncePerSave's
    // D-11 rows); a /password/i text query matches several elements.
    await userEvent.type(screen.getByTestId('reauth-password-input'), 'correct horse')
    await userEvent.click(screen.getByTestId('reauth-confirm'))
    return callsTo(calls, spec.endpoint)
  }

  it.each(D8_PASSWORD_SECTIONS.map((s) => [s.section, s] as const))(
    '%s: saving every password-marked change sends exactly one protected request',
    async (_name, spec) => {
      const calls = stubFetch()
      const protectedCalls = await renderSaveAndCount(spec, calls)
      expect(protectedCalls,
        `BLOCKED RED (D-8 note): ${spec.section} must reach ${spec.endpoint} exactly once`).toHaveLength(1)
    },
  )
})
