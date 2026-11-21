/**
 * formSavedStateCheck.test.tsx — Settings redesign #1519, RED.
 *
 * CHECK F01 — the saved-state check on the REAL form sections after a write
 * whose answer was lost (FR-020 outcome 4, spec :1924, and C-6, spec :246).
 *
 * Scenario (the finding): the answer to a Save is LOST — the write never
 * returns (connection failure / lost response). FR-020 outcome 4 then says
 * the screen runs the saved-state check, and C-6 fixes what that check must
 * conclude:
 *   - a key listed in GET /config/pending-restart has its saved value in the
 *     entry's persisted_value (read from config.json);
 *   - an unlisted key's saved value equals the running value (the section's
 *     read);
 *   - draft == saved value → SAVED (in use when unlisted, NOT in use when
 *     listed);
 *   - saved == the pre-save value → NOT SAVED;
 *   - neither → the draft stays and the screen names the change: "This
 *     setting was changed somewhere else while you were saving. Saved
 *     value: <value>."
 *   - either read fails → UNKNOWN: "N change(s) may not have been saved"
 *     with Retry.
 *
 * Case table (PerformanceSection carries all four; the other forms carry the
 * case their endpoint can produce):
 *   A  persisted == draft, listed in pending-restart, running read still the
 *      old value → "Saved, but not in use yet." — Performance, Context,
 *      Third-party agents. RED today: every form's check is a BINARY
 *      `check(): 'saved' | 'not-saved'` that consults only the running read
 *      (useFormSaveBar.ts::checkUnknown; PerformanceSection.tsx::check,
 *      ContextSection.tsx::check, ThirdPartyAgentsSection.tsx::check), so it
 *      answers "These changes were not saved." — a false statement, because
 *      config.json holds the new value.
 *   B  key unlisted, running read == the pre-save value → not saved, draft
 *      kept — Performance. GREEN today (the binary path gets this one
 *      right); the test pins it so the four-way fix cannot regress it.
 *   C  running read holds a THIRD value → changed elsewhere, draft kept —
 *      Performance, Providers default model, Providers Active transcriber,
 *      Sandbox allowed folders (R2-02). RED today: the binary check
 *      collapses this onto "not saved", which tells the person their draft
 *      was never stored when in fact someone else changed the value.
 *      R2-02 additions: every changed-elsewhere case asserts the line's
 *      EXACT saved value (check mutant N13's "Saved value: 0" fails), the
 *      Performance case asserts Discard lands on the new saved base, and
 *      the Sandbox restart-list case pins that a listed key's persisted
 *      value — not the running read — decides, with the draft kept on top
 *      and Discard showing the restart-list value.
 *   D  both reads fail → unknown: "1 change may not have been saved" with
 *      Retry — Performance, Providers default model. GREEN today; pinned so
 *      the fix cannot regress it.
 *
 * Provider coverage note: the default-model writer (PUT /providers/
 * default-model) is NOT one of the C-6 writer table's endpoints, so no
 * pending-restart entry can exist for its key — cases A (listed key) and B
 * are meaningless there; C and D cover its saved-state surface.
 *
 * Fixture keys are the BACKEND's canonical dotted keys — the spec's entry
 * keys (spec :107: "The entry key is the dotted config path of each stored
 * field (for example gateway.log_level, context.mcp_result_cap,
 * sandbox.third_party_agents.clis.codex.path)"), which is what a
 * saved_not_refreshed answer's details.keys carries
 * (pkg/gateway/rest_apply_failed.go::performancePendingApplyConfigKeys,
 * pkg/gateway/rest_context_settings.go + rest_settings_merge_c6_test.go for
 * context.mcp_result_cap, rest_third_party_settings.go writing under
 * sandbox.third_party_agents). `clis.<cli>.path` is that endpoint's 400
 * refusal `field`, never a pending-restart key. If the four-way fix looks
 * its keys up under other spellings, that mismatch is a test-side
 * reconciliation owned by the test author — never a reason to weaken these
 * oracles.
 *
 * Harness boundary (stated honestly): reads are mocked at '@/lib/api' and
 * the writes reject with a plain Error — the same classification seam a lost
 * response hits (toWriteResponse re-throws it, classifyWrite turns it into
 * the unknown outcome). The pending-restart list is served on BOTH seams the
 * C-6 check may read through — readPendingRestartFresh's global fetch and
 * the api client's fetchPendingRestart — because the fix's seam choice is
 * the implementer's. The forms are the real components; the Save runs
 * standalone (no shell), which needs no consent token for these flows.
 *
 * RED history: committed before the round-10 fix lands, so cases A and C
 * fail on the landing branch for the finding's reason (the binary-check
 * presentation, named in each failure) — that failing run is the RED proof;
 * B and D pass and pin the behaviour the fix must keep.
 */

import { describe, it, expect, vi, beforeEach, afterEach, beforeAll } from 'vitest'
import * as React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PerformanceSection } from './PerformanceSection'
import { ContextSection } from './ContextSection'
import { ThirdPartyAgentsSection } from './ThirdPartyAgentsSection'
import { ProvidersSection } from './ProvidersSection'
import { IntegrationsSection } from './IntegrationsSection'
import { SandboxSection } from './SandboxSection'
import * as api from '@/lib/api'

// cmdk (inside ModelSelector's Command) needs the jsdom stubs the
// model-selector test file uses: ResizeObserver, scrollIntoView.
beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

// The ProvidersSection harness precedent: Radix Popover content does not
// render in jsdom, so the ModelSelector's option list would never mount.
// Rendering the popover's parts inline is what lets the default-model option
// testids be reached (ProvidersSection.test.tsx stubs the same module for
// the same flow).
vi.mock('@/components/ui/popover', () => ({
  Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
  PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild && React.isValidElement(children) ? children : React.createElement('div', null, children),
  PopoverContent: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
  PopoverAnchor: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
}))

// ── Fixtures (spec-shaped reads; the values the forms start from) ──────────

const PERF_BEFORE = {
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

// A COMPLETE ContextSettings — a sparse fixture makes toForm produce
// "undefined" strings that fail the screen's own validation, and a Save with
// invalidCount > 0 never writes (the part-28 fixture is sparse because that
// file never saves the context group; this one does).
const CONTEXT_BEFORE = {
  mcp_result_cap: 20,
  builtin_success_cap: 5,
  builtin_failure_cap: 10,
  tool_result_share_fraction: 0.5,
  ingest_bound_bytes: 1048576,
  default_context_window: null,
  model_overrides: [],
}

const TPA_BEFORE = {
  clis: {
    'claude-code': { path: '/usr/local/bin/claude', preset_args: '', detected: { installed: true, path: '/usr/local/bin/claude' } },
    codex: { path: '/usr/local/bin/codex', preset_args: '', detected: { installed: true, path: '/usr/local/bin/codex' } },
    opencode: { path: '', preset_args: '', detected: { installed: false } },
  },
}

const DEFAULT_MODEL_BEFORE = { provider: 'openrouter', model: 'z-ai/glm-5.2', context_window: 1048576, window_source: 'catalog' }

// The Active-transcriber read (FR-025): three configured voice services, the
// first active. The changed-elsewhere answer flips the active service to a
// THIRD id — neither the draft pick nor the pre-save value.
const INTEGRATIONS_BEFORE = {
  search: [
    { id: 'brave', kind: 'search', display_name: 'Brave', configured: true, requires_key: true, usable: true, active: true },
  ],
  voice: [
    { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs', configured: true, requires_key: true, usable: true, active: true },
    { id: 'speechmatics', kind: 'voice', display_name: 'Speechmatics', configured: true, requires_key: true, usable: true, active: false },
    { id: 'deepgram', kind: 'voice', display_name: 'Deepgram', configured: true, requires_key: true, usable: true, active: false },
  ],
  active_search: 'brave',
  active_voice: 'elevenlabs',
  default_search: 'brave',
  fallback_search: null,
}
const INTEGRATIONS_ELSEWHERE = {
  ...INTEGRATIONS_BEFORE,
  voice: INTEGRATIONS_BEFORE.voice.map((p) => ({ ...p, active: p.id === 'deepgram' })),
  active_voice: 'deepgram',
}

// The Sandbox configuration read (FR-010's editor + C-6's check): one allowed
// folder. The changed-elsewhere answer holds a THIRD list.
const SANDBOX_BEFORE = {
  mode: 'permissive',
  allowed_paths: ['/a'],
  ssrf: { allow_internal: [] },
  requires_restart: false,
}
const SANDBOX_ELSEWHERE = { ...SANDBOX_BEFORE, allowed_paths: ['/c'] }

const ANTHROPIC_CONNECTED = {
  status: 'connected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  has_models_endpoint: true,
  models: ['claude-sonnet-4-5'],
}

// Minimal ProvidersCatalog document: the provider the draft picks, with the
// one model it offers. shape_version/version are the document's required
// header fields (ProvidersCatalog, generated types).
const ANTHROPIC_CATALOG = {
  schema_version: '2.0.0',
  version: 'v2026.1.1',
  updated_at: '2026-01-01T00:00:00Z',
  providers: [
    {
      id: 'anthropic',
      name: 'anthropic',
      company: 'Anthropic',
      api: 'https://api.anthropic.com',
      region: 'us-east-1',
      auth_methods: ['api_key'],
      models: [
        {
          id: 'claude-sonnet-4-5',
          name: 'claude-sonnet-4-5',
          context_window: 200000,
          max_output_tokens: 64000,
          input_modalities: ['text'],
          tool_call: true,
          status: 'active',
        },
      ],
    },
  ],
}

// The section reads are stateful: a test points one at the value the C-6
// check must find, or at 'fail' to make the read fail (case D).
const reads = {
  performance: { value: PERF_BEFORE as unknown, fail: false },
  context: { value: CONTEXT_BEFORE as unknown, fail: false },
  thirdParty: { value: TPA_BEFORE as unknown, fail: false },
  defaultModel: { value: DEFAULT_MODEL_BEFORE as unknown, fail: false },
  integration: { value: INTEGRATIONS_BEFORE as unknown, fail: false },
  sandbox: { value: SANDBOX_BEFORE as unknown, fail: false },
}

// One fixture drives BOTH pending-restart seams. `entries: null` makes both
// reads FAIL (case D). Entries satisfy the generated PendingRestartEntry
// schema (key, persisted_value, applied_value, reason are all required).
const pendingRestart: { entries: unknown[] | null } = { entries: [] }
function pendingEntry(key: string, persisted: unknown, applied: unknown) {
  return { key, persisted_value: persisted, applied_value: applied, reason: 'apply_failed' }
}

// A lost response: the write's promise rejects with a non-ApiError, which
// toWriteResponse re-throws and classifyWrite classifies as unknown.
const LOST = new Error('the answer was lost')

function stubPendingRestartWire() {
  const calls: string[] = []
  const fetchMock = vi.fn(async (input: any) => {
    const url = String(input)
    calls.push(url)
    if (url.includes('/config/pending-restart')) {
      if (pendingRestart.entries === null) throw new TypeError('pending-restart unreadable')
      return { ok: true, status: 200, json: async () => pendingRestart.entries }
    }
    throw new Error(`formSavedStateCheck: unexpected wire fetch ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    // Reads — stateful so each case can point the C-6 check at its answer.
    fetchPerformanceSettings: vi.fn(async () => {
      if (reads.performance.fail) throw new Error('performance settings unreadable')
      return reads.performance.value
    }),
    fetchMaxToolIterationsLoweringPreview: vi.fn(async () => ({ value: 8, agents: [] })),
    getContextSettings: vi.fn(async () => {
      if (reads.context.fail) throw new Error('context settings unreadable')
      return reads.context.value
    }),
    getThirdPartyAgentSettings: vi.fn(async () => {
      if (reads.thirdParty.fail) throw new Error('third-party agent settings unreadable')
      return reads.thirdParty.value
    }),
    getDefaultModel: vi.fn(async () => {
      if (reads.defaultModel.fail) throw new Error('default model unreadable')
      return reads.defaultModel.value
    }),
    fetchIntegrationProviders: vi.fn(async () => {
      if (reads.integration.fail) throw new Error('integration providers unreadable')
      return reads.integration.value
    }),
    fetchSandboxConfig: vi.fn(async () => {
      if (reads.sandbox.fail) throw new Error('sandbox config unreadable')
      return reads.sandbox.value
    }),
    // The sandbox editor's status read: a healthy kernel sandbox so the
    // configuration card renders (the editor is what the check runs on).
    fetchSandboxStatus: vi.fn(async () => ({
      backend: 'landlock',
      available: true,
      kernel_level: true,
      policy_applied: true,
      seccomp_enabled: true,
      bind_ports_count: 0,
    })),
    fetchPendingRestart: vi.fn(async () => {
      if (pendingRestart.entries === null) throw new Error('pending-restart unreadable')
      return pendingRestart.entries
    }),
    // Mount-safety stubs for the Providers section (unused by these flows).
    fetchProviders: vi.fn(async () => []),
    // A real (minimal) catalog document: the effort control's
    // useServedCatalog renders its own "Retry" button when the catalog read
    // fails, which would collide with the Save bar's Retry in the case-D
    // oracle; an entryless catalog would leave the default-model selector
    // without offerable models. This entry is exactly what the draft picks.
    fetchProvidersCatalog: vi.fn(async () => ANTHROPIC_CATALOG),
    checkEntitlement: vi.fn(async () => undefined),
    fetchSignInStatus: vi.fn(async () => undefined),
    configureProvider: vi.fn(),
    testProvider: vi.fn(),
    deleteProvider: vi.fn(),
    signOutProvider: vi.fn(),
    // Writes — every Save here loses its answer (the scenario).
    updatePerformanceSettings: vi.fn(),
    putContextSettings: vi.fn(),
    updateThirdPartyAgentSettings: vi.fn(),
    putDefaultModel: vi.fn(),
    configureIntegrationProvider: vi.fn(),
    updateSandboxConfig: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

function renderWith(ui: React.ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  reads.performance = { value: PERF_BEFORE, fail: false }
  reads.context = { value: CONTEXT_BEFORE, fail: false }
  reads.thirdParty = { value: TPA_BEFORE, fail: false }
  reads.defaultModel = { value: DEFAULT_MODEL_BEFORE, fail: false }
  reads.integration = { value: INTEGRATIONS_BEFORE, fail: false }
  reads.sandbox = { value: SANDBOX_BEFORE, fail: false }
  pendingRestart.entries = []
  vi.mocked(api.updatePerformanceSettings).mockRejectedValue(LOST)
  vi.mocked(api.putContextSettings).mockRejectedValue(LOST)
  vi.mocked(api.updateThirdPartyAgentSettings).mockRejectedValue(LOST)
  vi.mocked(api.putDefaultModel).mockRejectedValue(LOST)
  vi.mocked(api.configureIntegrationProvider).mockRejectedValue(LOST)
  vi.mocked(api.updateSandboxConfig).mockRejectedValue(LOST)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

// The write went out (the scenario's lost answer) before the check runs.
// The parameter is unknown + a cast: the api module's declared types present
// these as plain functions (the mock lives in the factory), so there is no
// shared structural type to name.
async function waitForWrite(fn: unknown) {
  await waitFor(() => expect((fn as { mock: { calls: unknown[] } }).mock.calls.length).toBe(1))
}

describe('F01 — after a lost write answer, the real forms run the C-6 saved-state check (FR-020 outcome 4, spec :1924; C-6, spec :246)', () => {
  it('Performance, case A — pending-restart lists the key with the draft as its persisted value while the running read still holds the old value: the form says Saved, but not in use yet', async () => {
    const wireCalls = stubPendingRestartWire()
    // Case A's premise (C-6, spec :246): the key IS listed, and its
    // persisted_value (read from config.json) is the draft. Without this
    // entry the fixture degenerates into case B (unlisted → not saved).
    pendingRestart.entries = [pendingEntry('performance.max_parallel_agents', 8, 4)]
    renderWith(<PerformanceSection />)
    const input = await screen.findByLabelText('Max parallel agents')
    await waitFor(() => expect(input).toHaveValue('4'))
    await userEvent.clear(input)
    await userEvent.type(input, '8')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updatePerformanceSettings)
    // C-6: the key IS listed and its persisted_value (8, from config.json)
    // equals the draft → SAVED, NOT IN USE — regardless of the running read
    // still saying 4 (FR-020 outcome 1's presentation, reached through the
    // check: NOT_IN_USE_MESSAGE, useSaveBar.ts).
    expect(await screen.findByText(/Saved, but not in use yet/i)).toBeInTheDocument()
    // The check consulted the pending-restart read on at least one seam
    // (global fetch or the api client) — the C-6 file read, not the running
    // read alone, decided this.
    expect(
      wireCalls.some((u) => u.includes('/config/pending-restart')) ||
        vi.mocked(api.fetchPendingRestart).mock.calls.length > 0,
    ).toBe(true)
    // None of the other outcomes' presentations may appear instead.
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/may not have been saved/i)).not.toBeInTheDocument()
    // The draft resolved to the saved value; the save signal shows (FR-018).
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue('8')
    expect(screen.getByText(/Saved at/)).toBeInTheDocument()
  })

  it('Performance, case B — the key is unlisted and the running read still holds the pre-save value: the form says the changes were not saved and keeps the draft', async () => {
    stubPendingRestartWire()
    renderWith(<PerformanceSection />)
    const input = await screen.findByLabelText('Max parallel agents')
    await waitFor(() => expect(input).toHaveValue('4'))
    await userEvent.clear(input)
    await userEvent.type(input, '8')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updatePerformanceSettings)
    // C-6: unlisted key → saved value = running value = the pre-save 4 →
    // NOT SAVED (FR-020 outcome 3): the refusal line and the draft kept in
    // the field. (The bar's own count line switching to its failure
    // presentation is presentational; FR-020 outcome 3 mandates the field
    // message and the kept draft, both asserted here.)
    expect(await screen.findByText(/These changes were not saved/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue('8')
    expect(screen.queryByText(/Saved, but not in use yet/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
  })

  it('Performance, case C — the running read holds a third value: the form names the change as changed somewhere else and keeps the draft', async () => {
    stubPendingRestartWire()
    renderWith(<PerformanceSection />)
    const input = await screen.findByLabelText('Max parallel agents')
    await waitFor(() => expect(input).toHaveValue('4'))
    await userEvent.clear(input)
    await userEvent.type(input, '8')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // The read the C-6 check will re-issue answers a THIRD value — neither
    // the draft 8 nor the pre-save 4. (Set after the draft: the same stateful
    // read served the mount, as one gateway would serve both moments.)
    reads.performance = { value: { ...PERF_BEFORE, max_parallel_agents: 6 }, fail: false }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updatePerformanceSettings)
    // C-6: saved value 6 is neither the draft 8 nor the pre-save 4 →
    // CHANGED ELSEWHERE (FR-020 outcome 4's third branch): the screen says
    // so and names the stored value. R2-02: the value itself is the oracle
    // (displaySavedValue(6) → "6") — a wrong value shown in the line (check
    // mutant N13's "Saved value: 0") must fail, not just the line's presence.
    const elsewhere = await screen.findByText(/changed somewhere else/i)
    expect(elsewhere.textContent).toContain('Saved value: 6.')
    // The draft stays — the person decides what to do with it (FR-012).
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue('8')
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByText(/Saved, but not in use yet/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    // R2-02: discarding the draft shows the NEW saved base — the check's read
    // became the saved base, so Discard lands on 6. Leaving the stale pre-save
    // 4 in the field would invite a next Save that silently reverts the other
    // change (C-6; FR-012's draft semantics).
    await userEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => expect(screen.getByLabelText('Max parallel agents')).toHaveValue('6'))
    expect(screen.queryByText(/1 unsaved change/)).not.toBeInTheDocument()
  })

  it('Performance, case D — both reads fail: the bar says 1 change may not have been saved, with Retry, and keeps the draft', async () => {
    stubPendingRestartWire()
    renderWith(<PerformanceSection />)
    const input = await screen.findByLabelText('Max parallel agents')
    await waitFor(() => expect(input).toHaveValue('4'))
    await userEvent.clear(input)
    await userEvent.type(input, '8')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // Both C-6 reads die — the pending-restart list AND the section's read —
    // after the section mounted healthy (the gateway died between the load
    // and the save's check).
    pendingRestart.entries = null
    reads.performance = { value: PERF_BEFORE, fail: true }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updatePerformanceSettings)
    // C-6 last rule: with neither read answerable, nothing is known — the
    // honest presentation is the may-not-have-been-saved line with Retry
    // (FR-020 outcome 4's last branch), never a confident saved or not-saved.
    expect(await screen.findByText(/1 change may not have been saved/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue('8')
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved, but not in use yet/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
  })

  it('Context, case A — pending-restart lists context.mcp_result_cap with the draft as its persisted value: the section says Saved, but not in use yet', async () => {
    stubPendingRestartWire()
    pendingRestart.entries = [pendingEntry('context.mcp_result_cap', 50, 20)]
    renderWith(<ContextSection />)
    const cap = await screen.findByLabelText('MCP tool result cap')
    await waitFor(() => expect(cap).toHaveValue('20'))
    await userEvent.clear(cap)
    await userEvent.type(cap, '50')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.putContextSettings)
    expect(await screen.findByText(/Saved, but not in use yet/i)).toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/may not have been saved/i)).not.toBeInTheDocument()
    // The stored value is the draft; the field keeps showing it.
    expect(screen.getByLabelText('MCP tool result cap')).toHaveValue('50')
  })

  it('Third-party agents, case A — pending-restart lists sandbox.third_party_agents.clis.codex.path with the draft as its persisted value: the section says Saved, but not in use yet', async () => {
    stubPendingRestartWire()
    // The entry key is the dotted CONFIG path (spec :107 names
    // sandbox.third_party_agents.clis.codex.path as the example);
    // clis.codex.path is the 400 refusal's field name, not this list's key.
    pendingRestart.entries = [pendingEntry('sandbox.third_party_agents.clis.codex.path', '/opt/codex/bin/codex', '/usr/local/bin/codex')]
    renderWith(<ThirdPartyAgentsSection />)
    const path = await screen.findByLabelText('Codex program path')
    await waitFor(() => expect(path).toHaveValue('/usr/local/bin/codex'))
    await userEvent.clear(path)
    await userEvent.type(path, '/opt/codex/bin/codex')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updateThirdPartyAgentSettings)
    expect(await screen.findByText(/Saved, but not in use yet/i)).toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/may not have been saved/i)).not.toBeInTheDocument()
    // FR-022's saved-not-refreshed presentation: the rows show what was sent.
    expect(screen.getByLabelText('Codex program path')).toHaveValue('/opt/codex/bin/codex')
  })

  it('Providers default model, case C — the read holds a third pair: the card names the change as changed somewhere else and keeps the draft pick', async () => {
    stubPendingRestartWire()
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_CONNECTED] as never)
    renderWith(<ProvidersSection />)
    await screen.findByTestId('default-model-provider')
    await userEvent.click(screen.getByTestId('default-model-change-btn'))
    await waitFor(() => expect(screen.getByTestId('default-model-select')).toBeInTheDocument())
    await userEvent.click(screen.getByTestId('default-model-option-claude-sonnet-4-5'))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // The read now answers a pair that is neither the draft pick nor the
    // pre-save pair — someone else changed the default model meanwhile.
    reads.defaultModel = { value: { provider: 'google', model: 'gemini-3-pro' }, fail: false }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.putDefaultModel)
    // PRODUCTION-SIDE (kept RED on landing 21c5c9b423, reported to
    // team-lead): FR-020's outcome-4 branches are classified in order and
    // C-6's carve-out (spec :246) reserves only the "not saved" conclusion
    // for writers that record no failed applies — a third value is the
    // changed-elsewhere branch for every Save-bar write. Landing's
    // saveOutcome.ts::savedStateCheck short-circuits recorded:false to
    // unknown BEFORE that branch; the fix exists on the frontend branch
    // (round 11, c884413e0c) and lands with its next merge. The oracle
    // stays the spec, not the FE.
    expect(await screen.findByText(/changed somewhere else/i)).toBeInTheDocument()
    // R2-02 + R3-03 (N32): the FULL saved pair is the oracle — a wrong
    // provider with the right model (check mutant N32's wrong-provider line)
    // must fail, not just the model slug. The pair is spelled provider/model,
    // the spec's own pair spelling (FR-004's provider=<p>&model=<m>).
    expect(screen.getByText(/changed somewhere else/i).textContent).toContain(
      'Saved value: google/gemini-3-pro.',
    )
    // The draft pick stays on the card and on the bar (FR-012).
    // The draft pick is kept; the provider label's exact rendering follows
    // the catalog (catalogLabel appends the region), so the pair's identity
    // is asserted, not one presentation of it.
    expect(screen.getByTestId('default-model-provider')).toHaveTextContent(/anthropic/i)
    expect(screen.getByTestId('default-model-model')).toHaveTextContent('claude-sonnet-4-5')
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved, but not in use yet/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    // R3-03: Discard lands on the NEW saved base — the check's fresh read
    // became the saved pair, so the card shows google/gemini-3-pro, never the
    // stale pre-save openrouter pair (FR-012: drafts return to the last
    // SAVED value; the Performance case C leg pins the same rule for numbers).
    // (Whether the outcome line itself lingers after the Discard is
    // spec-silent — not asserted either way.)
    await userEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => expect(screen.getByTestId('default-model-provider')).toHaveTextContent(/google/i))
    expect(screen.getByTestId('default-model-model')).toHaveTextContent('gemini-3-pro')
    expect(screen.queryByText(/1 unsaved change/)).not.toBeInTheDocument()
  })

  it('Providers default model, case D — the reads fail: the bar says 1 change may not have been saved, with Retry, and keeps the draft pick', async () => {
    stubPendingRestartWire()
    pendingRestart.entries = null
    vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_CONNECTED] as never)
    renderWith(<ProvidersSection />)
    await screen.findByTestId('default-model-provider')
    await userEvent.click(screen.getByTestId('default-model-change-btn'))
    await waitFor(() => expect(screen.getByTestId('default-model-select')).toBeInTheDocument())
    await userEvent.click(screen.getByTestId('default-model-option-claude-sonnet-4-5'))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    reads.defaultModel = { value: DEFAULT_MODEL_BEFORE, fail: true }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.putDefaultModel)
    expect(await screen.findByText(/1 change may not have been saved/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    // The draft pick is kept; the provider label's exact rendering follows
    // the catalog (catalogLabel appends the region), so the pair's identity
    // is asserted, not one presentation of it.
    expect(screen.getByTestId('default-model-provider')).toHaveTextContent(/anthropic/i)
    expect(screen.getByTestId('default-model-model')).toHaveTextContent('claude-sonnet-4-5')
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
  })

  it('Providers active transcriber, case C — the read holds a third active service: the section names the change with the exact service and keeps the draft pick (R2-02)', async () => {
    stubPendingRestartWire()
    renderWith(<IntegrationsSection />)
    const select = await screen.findByTestId('active-transcriber')
    await waitFor(() => expect(select).toHaveValue('elevenlabs'))
    await userEvent.selectOptions(select, 'speechmatics')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // The read the C-6 check will re-issue answers a THIRD active service —
    // someone else activated Deepgram while the answer was in flight.
    reads.integration = { value: INTEGRATIONS_ELSEWHERE, fail: false }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.configureIntegrationProvider)
    expect(await screen.findByText(/changed somewhere else/i)).toBeInTheDocument()
    // The saved value is named exactly: displaySavedValue shows strings raw,
    // so the line carries the service id itself (R2-02's exact-value oracle).
    expect(screen.getByText(/changed somewhere else/i).textContent).toContain('Saved value: deepgram.')
    // The draft pick stays (FR-012) and the bar still counts it.
    expect(screen.getByTestId('active-transcriber')).toHaveValue('speechmatics')
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved, but not in use yet/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    // R3-03: Discard lands on the NEW saved base — the fresh read (deepgram)
    // is the saved active service now, so the select shows deepgram, never
    // the stale pre-save elevenlabs (FR-012, the Performance case C rule).
    await userEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => expect(screen.getByTestId('active-transcriber')).toHaveValue('deepgram'))
    expect(screen.queryByText(/1 unsaved change/)).not.toBeInTheDocument()
  })

  it('Sandbox allowed folders, case C — before [/a], draft [/b], the read holds the third list [/c]: the section names the change with the exact list and keeps the draft (R2-02)', async () => {
    stubPendingRestartWire()
    renderWith(<SandboxSection />)
    await screen.findByText('/a')
    // before [/a] → draft [/b]: Add/Delete stage drafts (FR-010) — delete the
    // staged /a, add /b. Nothing is sent until Save changes.
    await userEvent.click(screen.getByRole('button', { name: /delete path \/a/i }))
    const input = screen.getByRole('textbox', { name: /new allowed path/i })
    await userEvent.type(input, '/b')
    await userEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText('/b')).toBeInTheDocument()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // The read the C-6 check will re-issue answers the third list [/c].
    reads.sandbox = { value: SANDBOX_ELSEWHERE, fail: false }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updateSandboxConfig)
    expect(await screen.findByText(/changed somewhere else/i)).toBeInTheDocument()
    // The saved list is named exactly: displaySavedValue stringifies
    // non-strings, so the line carries ["/c"] (R2-02's exact-value oracle).
    expect(screen.getByText(/changed somewhere else/i).textContent).toContain('Saved value: ["/c"].')
    // The draft list stays in the editor (FR-012) — /b kept, the saved /c
    // does not displace it.
    expect(screen.getByText('/b')).toBeInTheDocument()
    expect(screen.queryByText('/c')).not.toBeInTheDocument()
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved, but not in use yet/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
  })

  it('Sandbox allowed folders on the restart list — the entry\'s persisted value decides, the draft is kept, and Discard shows the restart-list value (R2-02)', async () => {
    stubPendingRestartWire()
    // The key IS listed, its persisted_value the third list [/c]; the running
    // read still holds the pre-save [/a]. C-6: a listed key's saved value is
    // the entry's persisted_value — if the running read decided, this would
    // never be changed-elsewhere with [/c], so the exact value proves the
    // list decided.
    pendingRestart.entries = [pendingEntry('sandbox.allowed_paths', ['/c'], ['/a'])]
    renderWith(<SandboxSection />)
    await screen.findByText('/a')
    await userEvent.click(screen.getByRole('button', { name: /delete path \/a/i }))
    const input = screen.getByRole('textbox', { name: /new allowed path/i })
    await userEvent.type(input, '/b')
    await userEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText('/b')).toBeInTheDocument()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updateSandboxConfig)
    expect(await screen.findByText(/changed somewhere else/i)).toBeInTheDocument()
    expect(screen.getByText(/changed somewhere else/i).textContent).toContain('Saved value: ["/c"].')
    // The sent list stays a draft on top of the saved configuration (FR-012).
    expect(screen.getByText('/b')).toBeInTheDocument()
    expect(screen.queryByText('/c')).not.toBeInTheDocument()
    // Discard abandons the draft: the editor shows the saved configuration —
    // the restart list's persisted value — not the stale pre-save [/a].
    await userEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => expect(screen.getByText('/c')).toBeInTheDocument())
    expect(screen.queryByText('/b')).not.toBeInTheDocument()
    expect(screen.queryByText(/1 unsaved change/)).not.toBeInTheDocument()
  })

  it('Sandbox, old running read on an unlisted key — the result stays unknown: a non-recording writer\'s read never proves "not saved" (C-6, R3-03/N25)', async () => {
    stubPendingRestartWire()
    renderWith(<SandboxSection />)
    await screen.findByText('/a')
    // before [/a] → draft [/b], the answer lost (the scenario).
    await userEvent.click(screen.getByRole('button', { name: /delete path \/a/i }))
    const input = screen.getByRole('textbox', { name: /new allowed path/i })
    await userEvent.type(input, '/b')
    await userEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // The key is UNLISTED and the check's fresh read still answers the
    // pre-save list [/a] (the reads stay at SANDBOX_BEFORE). PUT /security/
    // sandbox-config records no failed applies, so per C-6 a read equal to
    // the value before the Save proves nothing — the result stays UNKNOWN
    // (FR-020 outcome 4's last rule). This premise is the one that catches
    // check mutant N25, which flips the section's items to recorded and would
    // falsely conclude "not saved" here.
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updateSandboxConfig)
    expect(await screen.findByText(/1 change may not have been saved/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    // Never a confident "not saved", never a saved signal, never
    // changed-elsewhere — nothing was provable.
    expect(screen.queryByText(/These changes were not saved/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
    // The draft stays (FR-020 outcome 4's last branch).
    expect(screen.getByText('/b')).toBeInTheDocument()
  })

  it('Sandbox, changed elsewhere via the restart list — the required saved-config refresh runs, and when it fails the result stays unknown with Retry (C-6, R3-03/N26)', async () => {
    stubPendingRestartWire()
    // The key IS listed with a third persisted value [/c], so the
    // changed-elsewhere conclusion alone needs no server read — and the saved
    // configuration must STILL be refreshed from the server afterwards (C-6;
    // the changed-elsewhere branch's required read). That required refresh
    // fails here: nothing is known, whatever the list already proved.
    pendingRestart.entries = [pendingEntry('sandbox.allowed_paths', ['/c'], ['/a'])]
    renderWith(<SandboxSection />)
    await screen.findByText('/a')
    await userEvent.click(screen.getByRole('button', { name: /delete path \/a/i }))
    const input = screen.getByRole('textbox', { name: /new allowed path/i })
    await userEvent.type(input, '/b')
    await userEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    reads.sandbox = { value: SANDBOX_BEFORE, fail: true }
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForWrite(api.updateSandboxConfig)
    // The failed required refresh must resolve to UNKNOWN with Retry (C-6's
    // "if either read fails" rule) — never to a falsely saved outcome (check
    // mutant N26 returns saved here; the bar would show its saved signal).
    expect(await screen.findByText(/1 change may not have been saved/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Saved, but not in use yet/i)).not.toBeInTheDocument()
    // The draft stays.
    expect(screen.getByText('/b')).toBeInTheDocument()
  })
})
