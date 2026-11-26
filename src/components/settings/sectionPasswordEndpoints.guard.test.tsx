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
 * while the real planner runs (D-8 note), through the SaveBar with a
 * HANDWRITTEN writer callback — that harness proves the planner and the
 * bar, not the real section forms (CHECK F08: a form hook that dropped the
 * token passed all of these). The real section form → API edge checks for
 * every password-protected writer live in the F08 describe below: the real
 * component, the real prompt chain and the real api client onto the wire
 * stub, asserting the X-Reauth-Token wire fact.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
// Type-only: the runtime modules load through the BLOCKED RED importActual
// helpers; the erased types give the fixtures and planner calls the real
// contracts (SaveBarField, PlannedWrite) without touching the wire.
import type { SaveBarField } from './useSaveBar'

// Reads are mocked per section below; the WRITE functions stay real so each
// real-form test can assert what actually leaves for the network. Local
// mode is the default so the shell prompt is the real ReAuthDialog.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({
      onboarding_complete: true,
      identity: { mode: 'local', edition: 'core', signed_in: true },
    })),
    fetchPerformanceSettings: vi.fn(),
    fetchMaxToolIterationsLoweringPreview: vi.fn(),
    fetchSandboxStatus: vi.fn(),
    fetchSandboxConfig: vi.fn(),
    fetchPromptGuardLevel: vi.fn(),
    fetchRateLimits: vi.fn(),
    fetchConfig: vi.fn(),
    fetchCredentials: vi.fn(),
    fetchBuiltinTools: vi.fn(),
    fetchIntegrationProviders: vi.fn(),
  }
})

import * as api from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'
import { SandboxSection } from './SandboxSection'
import { SecuritySection } from './SecuritySection'
import { IntegrationsSection } from './IntegrationsSection'

async function loadSavePlanner(): Promise<typeof import('@/components/settings/savePlanner')> {
  try {
    return await vi.importActual<typeof import('@/components/settings/savePlanner')>('@/components/settings/savePlanner')
  } catch {
    throw new Error(
      'BLOCKED RED (FR-030, D-8, order 23): the Settings save planner does not exist yet — ' +
        'required by FR-030 (the guard runs the real save planner and counts protected endpoints per section)',
    )
  }
}

// D-8 sections and their password-marked rows (FR-025 badge list; C-4 table).
const D8_PASSWORD_SECTIONS: Array<{ section: string; endpoint: string; fields: SaveBarField[] }> = [
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
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
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

async function loadHarnessModules(): Promise<{
    SaveBar: typeof import('@/components/settings/SaveBar')['SaveBar']
    SettingsShellContext: typeof import('@/components/settings/SettingsShellContext')['SettingsShellContext']
    useSavePasswordPrompt: typeof import('@/components/settings/useSavePasswordPrompt')['useSavePasswordPrompt']
  }> {
  try {
    const [saveBar, shell, prompt] = await Promise.all([
      vi.importActual<typeof import('@/components/settings/SaveBar')>('@/components/settings/SaveBar'),
      vi.importActual<typeof import('@/components/settings/SettingsShellContext')>('@/components/settings/SettingsShellContext'),
      vi.importActual<typeof import('@/components/settings/useSavePasswordPrompt')>('@/components/settings/useSavePasswordPrompt'),
    ])
    return { SaveBar: saveBar.SaveBar ?? saveBar.default, SettingsShellContext: shell.SettingsShellContext, useSavePasswordPrompt: prompt.useSavePasswordPrompt }
  } catch {
    throw new Error(
      'BLOCKED RED (FR-030, D-8, order 23): the Settings Save bar / shell context / password prompt do not ' +
        'exist yet — required for the network-edge counting while the real planner runs',
    )
  }
}

// Healthy read fixtures for the real-form tests (writes stay real; their
// responses come from the wire stub below and must satisfy the generated
// response schemas).
const PERFORMANCE_OK = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
  goal_max_rounds: 20,
  goal_reminder_quiet_seconds: 60,
  goal_stalled_checkin_enabled: true,
  goal_stalled_checkin_minutes: 30,
  max_tool_iterations: 1000,
}
const SANDBOX_OK = { mode: 'enforce', allowed_paths: ['/a'], ssrf: { allow_internal: [] }, requires_restart: false }
const INTEGRATIONS_OK = {
  search: [
    { id: 'brave', kind: 'search', display_name: 'Brave', configured: true, requires_key: true, usable: true, active: true },
  ],
  voice: [
    { id: 'groq', kind: 'voice', display_name: 'Groq', configured: true, requires_key: true, usable: true, active: true },
    { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs', configured: true, requires_key: true, usable: true, active: false },
  ],
  active_search: 'brave',
  active_voice: 'groq',
  default_search: 'brave',
  fallback_search: null,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(PERFORMANCE_OK as never)
  vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 8, agents: [] } as never)
  vi.mocked(api.fetchSandboxStatus).mockResolvedValue({ kernel_sandbox_active: true } as never)
  vi.mocked(api.fetchSandboxConfig).mockResolvedValue({ ...SANDBOX_OK, auto_approve: false } as never)
  vi.mocked(api.fetchPromptGuardLevel).mockResolvedValue({ level: 'medium' } as never)
  vi.mocked(api.fetchRateLimits).mockResolvedValue({ enabled: true, max_agent_llm_calls_per_hour: 0, max_agent_tool_calls_per_minute: 0 } as never)
  vi.mocked(api.fetchConfig).mockResolvedValue({ gateway: {}, data: { session_retention_days: 90 } } as never)
  vi.mocked(api.fetchCredentials).mockResolvedValue([] as never)
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([] as never)
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(INTEGRATIONS_OK as never)
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
      const planner = await loadSavePlanner()
      const writes = planner.planSectionSave(spec.section, spec.fields)
      const protectedWrites = writes.filter((w) => w.password)
      expect(protectedWrites.length, `BLOCKED RED (D-8): ${spec.section} must send exactly one protected write`).toBe(1)
      expect(protectedWrites[0].endpoint).toContain(spec.endpoint)
    },
  )

  it.each(D8_NO_PASSWORD_SECTIONS.map((s) => [s] as const))(
    '%s: no protected write at all (D-8 row 5)',
    async (section) => {
      const planner = await loadSavePlanner()
      // Typed as the Save-bar field contract; PlannedField (the planner's
      // parameter) accepts the SaveBarField superset, and a named constant
      // keeps the extra screen-side properties off the planner's fresh
      // literal check.
      const exampleField: SaveBarField = { key: `${section}.example`, label: 'Example', value: 'changed', kind: 'text' }
      const writes = planner.planSectionSave(section, [exampleField])
      expect(writes.filter((w) => w.password).length,
        `BLOCKED RED (D-8 row 5): ${section} carries no password values`).toBe(0)
    },
  )

  it('the whole planner table passes the guard (every section has at most one protected endpoint)', async () => {
    const planner = await loadSavePlanner()
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
    const planner = await loadSavePlanner()
    const injected = { gateway: ['/api/v1/config', '/api/v1/security/sandbox-config'] }
    expect(() => planner.assertOneProtectedEndpointPerSection(injected)).toThrow(
      /more than one protected endpoint|at most one protected endpoint/i,
    )
  })
})

describe('D-8 note — the generic Save bar with a HANDWRITTEN writer (boundary: the planner and the bar, not the real section forms)', () => {
  // Renders the REAL Save bar with the REAL prompt machinery at the shell
  // boundary; the section write leaves as a real PUT to the section's D-8
  // endpoint with the consent token (FR-027), counted at the fetch stub.
  // BOUNDARY (CHECK F08, stated honestly): the onSave callback below builds
  // the X-Reauth-Token header ITSELF from the token the bar hands it, so
  // these tests prove the planner's one-endpoint plan and the bar's
  // one-prompt-per-Save cadence — they CANNOT see a real section form hook
  // dropping the token (that fault passed all 17 of these as CHECK mutant
  // M12). The real form → wire checks live in the F08 describe below.
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
        const next = f.options?.find((o: unknown) => String(o) !== String(f.value)) ?? f.options?.[1]
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

// ── F08 — the real section forms put the consent token on the wire ───────────
//
// CHECK F08 (mutant M12): every test above still passed when a real section
// form hook dropped the consent token, because their onSave callbacks build
// the header themselves. Each test below drives the REAL section component
// in local mode through the REAL prompt chain (useSavePasswordPrompt →
// ReAuthDialog → the real POST /auth/reauth answered by the wire stub) and
// lets the section's write leave through the REAL api client onto the fetch
// stub. The assertion is the wire fact FR-027 fixes: exactly one protected
// write per Save, carrying X-Reauth-Token with the minted token. A form
// hook that drops the token fails here.

type Wire = { url: string; init: RequestInit }

/** Wire stub for the real-form tests: the reauth POST mints 'rt'; bodies per fragment must satisfy the write's response schema. */
function stubWire(reply: Record<string, unknown>) {
  const calls: Wire[] = []
  const respond = (status: number, body: unknown) => ({
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ 'Content-Type': 'application/json' }),
    json: async () => body,
    text: async () => JSON.stringify(body),
  })
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    calls.push({ url, init: init ?? {} })
    if (url.includes('/auth/reauth')) {
      return respond(200, { verified: true, token: 'rt', expires_in: 300 })
    }
    for (const [fragment, body] of Object.entries(reply)) {
      if (url.includes(fragment)) return respond(200, body)
    }
    return respond(200, {})
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}

/** The shell around a real section: the real prompt chain owns the one password prompt (FR-026). */
async function renderRealForm(ui: ReactNode) {
  const { SettingsShellContext, useSavePasswordPrompt } = await loadHarnessModules()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  function ShellHarness() {
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
        {ui}
        {dialog}
      </SettingsShellContext.Provider>
    )
  }
  render(
    <QueryClientProvider client={client}>
      <ShellHarness />
    </QueryClientProvider>,
  )
}

/** Save through the real prompt: answer the real ReAuthDialog once. */
async function answerRealPrompt() {
  await userEvent.type(await screen.findByTestId('reauth-password-input'), 'correct horse')
  await userEvent.click(screen.getByTestId('reauth-confirm'))
}

describe('F08 — the real section forms reach their one protected endpoint with the consent token (FR-027)', () => {
  it('limits: the real Performance form saves a password-marked change as exactly one PUT /performance carrying X-Reauth-Token', async () => {
    const calls = stubWire({ '/api/v1/performance': PERFORMANCE_OK })
    await renderRealForm(<PerformanceSection />)
    const input = await screen.findByLabelText('Max parallel agents')
    await waitFor(() => expect(input).toHaveValue('4'))
    await userEvent.clear(input)
    await userEvent.type(input, '8')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await answerRealPrompt()
    await waitFor(() => expect(callsTo(calls, '/api/v1/performance')).toHaveLength(1))
    expect(callsTo(calls, '/api/v1/performance')).toHaveLength(1) // exactly one protected write
    expect(callsTo(calls, '/api/v1/performance')[0].init.headers).toHaveProperty('X-Reauth-Token', 'rt')
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(1) // one consent minted the token
  })

  it('sandbox & network: the real Sandbox form saves as exactly one PUT /security/sandbox-config carrying X-Reauth-Token', async () => {
    const calls = stubWire({ '/api/v1/security/sandbox-config': SANDBOX_OK })
    await renderRealForm(<SandboxSection />)
    const path = await screen.findByRole('textbox', { name: /new allowed path/i })
    await userEvent.type(path, '/valid')
    await userEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await answerRealPrompt()
    await waitFor(() => expect(callsTo(calls, '/api/v1/security/sandbox-config')).toHaveLength(1))
    expect(callsTo(calls, '/api/v1/security/sandbox-config')).toHaveLength(1)
    expect(callsTo(calls, '/api/v1/security/sandbox-config')[0].init.headers).toHaveProperty('X-Reauth-Token', 'rt')
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(1)
  })

  it('protection: the real Security section saves the Auto-approve switch as exactly one PUT /security/sandbox-config carrying X-Reauth-Token', async () => {
    const calls = stubWire({ '/api/v1/security/sandbox-config': { ...SANDBOX_OK, auto_approve: true } })
    await renderRealForm(<SecuritySection view="protection" />)
    // The shell's prompt hook decides the mode from the app-state read; wait
    // for it so Save changes takes the mode this file pins (local).
    await waitFor(() => expect(api.fetchAppState).toHaveBeenCalled())
    // The draft is made at the Auto-approve switch of the Protection view, the
    // view the Settings shell renders for this section (SettingsSectionBody
    // passes view="protection"). That view shows the section's one Save bar in
    // the page itself. The legacy one-page layout, whose collapsed Advanced
    // disclosure held the bar, was deleted by Skills & Tools (spec C-22 row 6),
    // so there is nothing to expand.
    const toggle = await screen.findByTestId('auto-approve-global-switch')
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    await userEvent.click(toggle)
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await answerRealPrompt()
    await waitFor(() => expect(callsTo(calls, '/api/v1/security/sandbox-config')).toHaveLength(1))
    expect(callsTo(calls, '/api/v1/security/sandbox-config')).toHaveLength(1)
    expect(callsTo(calls, '/api/v1/security/sandbox-config')[0].init.headers).toHaveProperty('X-Reauth-Token', 'rt')
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(1)
  })

  it('providers: the real Voice input group saves the Active transcriber as exactly one PUT /integrations/providers/{id} carrying X-Reauth-Token', async () => {
    const calls = stubWire({ '/api/v1/integrations/providers/': INTEGRATIONS_OK })
    await renderRealForm(<IntegrationsSection />)
    const select = await screen.findByTestId('active-transcriber')
    await waitFor(() => expect(select).toHaveValue('groq'))
    await userEvent.selectOptions(select, 'elevenlabs')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await answerRealPrompt()
    await waitFor(() => expect(callsTo(calls, '/api/v1/integrations/providers/')).toHaveLength(1))
    expect(callsTo(calls, '/api/v1/integrations/providers/')).toHaveLength(1)
    expect(callsTo(calls, '/api/v1/integrations/providers/')[0].init.headers).toHaveProperty('X-Reauth-Token', 'rt')
    expect(callsTo(calls, '/auth/reauth')).toHaveLength(1)
  })
})
