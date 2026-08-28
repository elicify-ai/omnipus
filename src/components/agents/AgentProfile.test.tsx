import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, Skill } from '@/lib/api'

// ResizeObserver is required by cmdk (used inside the ModelSelector popover);
// jsdom does not implement it. Polyfill with a noop for the tests that open
// the popover. We use vi.stubGlobal so individual tests can unstub it via
// vi.unstubAllGlobals() to test the "ResizeObserver unavailable" fallback.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}
// Radix Select (portal + pointer events) needs hasPointerCapture to open in jsdom.
if (typeof Element !== 'undefined' && !Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}

// test_agent_profile_sections (test #13)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent profile renders with type-appropriate sections
//             wave5a-wire-ui-spec.md — US-7 AC2: core agent sections
//             wave5a-wire-ui-spec.md — US-7 AC3: locked core agent read-only sections
//
// Timing note (echo-race fix, P2 'Text input Auto save'): AgentProfile's
// main useAutoSave call was raised from the 500ms default to 1500ms (see
// AgentProfile.tsx). Deliberately widened every REAL-TIMER `waitFor(...,
// { timeout: 3000 })` in this file that gates on the debounced autosave
// actually firing (fireEvent.change → updateAgent/testAgentRunner called) to
// `{ timeout: 6000 }`, preserving roughly the original ~6x margin over the
// debounce interval. `{ timeout: 5000 }` sites already had enough headroom
// and were left as-is. Sites NOT gated on the autosave debounce (explicit
// mutation buttons, error-state renders) were left untouched.

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => ({}),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgent: vi.fn(),
    fetchWorkspace: vi.fn(),
    updateAgent: vi.fn(),
    updateWorkspace: vi.fn(),
    deleteAgent: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
  }
})

import { fetchAgent, fetchWorkspace, fetchSkills, updateAgent, updateWorkspace, deleteAgent, fetchProviders, testAgentRunner } from '@/lib/api'
import type { Workspace } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { ApiError } from '@/lib/api-error'

const mockCoreAgent: Agent = {
  id: 'general-assistant',
  name: 'General Assistant',
  type: 'core',
  locked: false,
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'General purpose assistant',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  rate_limits: { use_global_defaults: true },
  stats: { total_sessions: 5, total_tokens: 12000, total_cost: 0.05 },
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  memory_enabled: true,
}

const mockLockedCoreAgent: Agent = {
  id: 'mia',
  name: 'Mia',
  type: 'core',
  locked: true,
  needs_model: false,
  status: 'active',
  model: 'claude-opus-4-6',
  description: 'Core agent with compiled prompt — identity is read-only',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  memory_enabled: true,
}

// Tier-branched form fixtures (Spec-4 FR-4.1 + locked concept in
// `.preview-doc/agents.html`): workers carry an executor; base agents do not.
// `mockWorkerAgent` is the canonical editable worker; `mockLockedWorkerAgent`
// is a worker that has been locked down (e.g. a marketplace-supplied worker).
const mockWorkerAgent: Agent = {
  ...mockCoreAgent,
  id: 'web-researcher',
  name: 'Web Researcher',
  type: 'Subagent',
  description: 'Delegation-only labour agent — no heartbeat, optional soul',
  // Spec-4 FR-4.1: every worker has a runner. Absent → native default in
  // the component layer; we set one explicitly so the executor accordion
  // hydrates to a meaningful kind for the tests.
  executor: { kind: 'external-cli', cli: 'claude-code' },
}

const mockLockedWorkerAgent: Agent = {
  ...mockWorkerAgent,
  id: 'marketplace-pack-worker',
  name: 'Marketplace Worker',
  locked: true,
}

// W2c / field matrix (docs/internal/architecture/agent-types-field-matrix.md):
// isExternal/isNativeWorker are TYPE-based, not executor-based — a
// `subagent_3p` agent is the ONLY kind that is genuinely external. This
// fixture is the canonical "truly external" agent (contrast with
// `mockWorkerAgent`, which is `type: 'Subagent'` — native — even though it
// happens to carry an `external-cli` executor value in some fixtures above;
// that combination is legacy-test shorthand, not a real subagent_3p).
const mockSubagent3pAgent: Agent = {
  ...mockCoreAgent,
  id: 'external-researcher',
  name: 'External Researcher',
  type: 'subagent_3p',
  description: 'Delegation-only worker on an external CLI runner',
  executor: { kind: 'external-cli', cli: 'claude-code' },
}

// ADR-052 FR-038/FR-039 — the seeded Judge System Agent. Locked + type
// 'system'; its soul carries the judging rubric (soul/rubric unification —
// `AgentConfig.Rubric` was deleted) and memory_enabled is seeded false
// (impartial, reproducible verdicts).
const mockJudgeAgent: Agent = {
  id: 'judge',
  name: 'Judge',
  type: 'system',
  locked: true,
  needs_model: false,
  status: 'active',
  model: 'claude-opus-4-6',
  description: 'Impartial acceptance-criteria verifier',
  soul: 'You are the Judge — an impartial acceptance-criteria evaluator.',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  memory_enabled: false,
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderProfile(agentId: string, client?: QueryClient) {
  return render(
    <QueryClientProvider client={client ?? makeClient()}>
      <AgentProfile agentId={agentId} />
    </QueryClientProvider>
  )
}

// Wave 5 / Radix Tabs: the Radix Tabs trigger uses keyboard activation
// (Enter / Space) for state changes in JSDOM — `fireEvent.click()` alone
// does not flush the internal onValueChange. This helper mirrors the
// focus + keyDown pattern that Radix expects, and falls back to click for
// any test where keyboard activation is not what we want.
function switchTab(testId: string) {
  const trigger = screen.getByTestId(testId)
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockCoreAgent)
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(updateAgent).mockReset().mockResolvedValue(mockCoreAgent)
  vi.mocked(testAgentRunner).mockReset().mockResolvedValue({
    ok: true,
    reason: '',
    message: 'ready',
    cli: 'claude-code',
  })
  // Default: two connected providers with model lists — the provider-aware
  // fallback editor needs ≥2 provider groups to exercise the provider-grouped
  // chip layout (and the per-provider badge attribution).
  vi.mocked(fetchProviders).mockResolvedValue([
    { id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter', status: 'connected', models: ['z-ai/glm-5.2', 'z-ai/glm-5-turbo'], auth_method: 'api_key', dependents: [], backs_default: false },
    { id: 'anthropic', name: 'anthropic', display_name: 'Anthropic', status: 'connected', models: ['claude-sonnet-4-6', 'claude-opus-4-6'], auth_method: 'api_key', dependents: [], backs_default: false },
  ])
})

describe('AgentProfile — loading state', () => {
  it('shows "Loading agent..." while data is fetching', () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7: profile shows loading state
    vi.mocked(fetchAgent).mockReturnValue(new Promise(() => {})) // never resolves
    renderProfile('general-assistant')
    expect(screen.getByText(/loading agent/i)).toBeInTheDocument()
  })
})

describe('AgentProfile — error state', () => {
  it('shows the generic "couldn\'t load" error + retry when fetch fails with a non-404', async () => {
    // After the slide-over refactor the error path distinguishes 404 from
    // other failures so the user gets the right copy and a retry affordance
    // for transient errors. A plain `Error` (not an `ApiError` with a 404
    // status) lands in the generic branch.
    vi.mocked(fetchAgent).mockRejectedValue(new Error('Not found'))
    renderProfile('bad-id')
    // The body shows the new "Couldn't load agent" copy. The sr-only
    // SheetTitle uses the same string for the non-404 branch, so multiple
    // matches are expected; assert at least one is present and the retry
    // button is offered for the user to recover from a transient failure.
    const matches = await screen.findAllByText(/couldn't load agent/i)
    expect(matches.length).toBeGreaterThanOrEqual(1)
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })

  it('shows "Agent not found" with no retry for a 404 ApiError', async () => {
    // 404 is a "this agent is gone" signal — retrying won't help, so the
    // generic copy is replaced and the retry button is hidden.
    vi.mocked(fetchAgent).mockRejectedValue(new ApiError(404, 'Agent not found'))
    renderProfile('bad-id')
    // The body shows "Agent not found" and the sr-only SheetTitle also
    // matches; assert on the visible body (the <p> in the centred panel).
    const matches = await screen.findAllByText(/agent not found/i)
    expect(matches.length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
  })
})

describe('AgentProfile — core agent sections (test #13)', () => {
  it('renders agent name, type badge, and model section after loading', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7 AC2: core agent sections
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getAllByText(/core/i).length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText(/Model/).length).toBeGreaterThanOrEqual(1)
  })

  it('shows Rate Limits section with "Use global defaults" for core agent', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7 AC5: rate limits defaults toggle
    // Wave 5 / spec §6.2: Rate Limits is now inside the Advanced tab; click
    // the tab to open the panel and assert on the "Use global defaults" copy.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-advanced')
    expect(await screen.findByText(/Use global defaults/i)).toBeInTheDocument()
  })

  it('shows Stats section when stats are present', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7: stats section
    // Wave 5: stats are in the Advanced tab. Click it before asserting.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-advanced')
    // Activity heading is the stat section container.
    expect(await screen.findByText(/^Activity$/i)).toBeInTheDocument()
  })

  it('shows the slide-over footer when the profile is fully rendered', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7 AC2: editable sections for core.
    // Wave 5 / spec §6.1: footer shows last-saved-indicator + delete-agent-button
    // (no separate Close button). Assert on the spec-mandated testids.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getByTestId('last-saved-indicator')).toBeInTheDocument()
    expect(screen.getByTestId('delete-agent-button')).toBeInTheDocument()
  })

  it('shows the autosave scope cue near the save indicator (UAT 4b)', async () => {
    // Agent edits are autosave-only and apply everywhere the agent is used —
    // the footer makes that scope explicit next to the save indicator.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    const cue = screen.getByTestId('autosave-scope-cue')
    expect(cue).toBeInTheDocument()
    expect(cue).toHaveTextContent(/apply everywhere this agent is used/i)
  })

  it('shows tools & permissions section when tools are present', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7: tools section
    // Wave 5: Tools & Permissions now lives inside the Tools tab; click it
    // before asserting on the section heading.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-tools')
    expect(await screen.findByText(/Tools.*Permissions/i)).toBeInTheDocument()
  })
})

describe('AgentProfile — locked core agent sections (test #13)', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
  })

  it('shows Rate Limits for locked core agents, editable (operator decision 2026-07-03)', async () => {
    // Traces to: agent-types-field-matrix.md — rate_limits is mutable on the
    // backend for locked agents; the UI now exposes it (superseding the old
    // "locked agents hide rate limits" behavior — only identity/soul/skills
    // stay 403'd for locked core agents).
    renderProfile('mia')
    await screen.findByText('Mia')
    switchTab('tab-advanced')
    const toggle = await screen.findByText(/Use global defaults/i)
    expect(toggle).toBeInTheDocument()
    // The "Use global defaults" switch is interactive (not disabled).
    const section = toggle.closest('section') as HTMLElement
    const globalDefaultsSwitch = section.querySelector('button[role="switch"]') as HTMLButtonElement
    expect(globalDefaultsSwitch).not.toBeNull()
    expect(globalDefaultsSwitch.disabled).toBe(false)
  })

  it('does NOT show Save button for locked core agent', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7 AC3: locked agents are not editable
    renderProfile('mia')
    await screen.findByText('Mia')
    expect(screen.queryByRole('button', { name: /save/i })).toBeNull()
  })
})

// D3 (UAT v0.1.1 defects) — hydration must never trigger a spurious PUT.
//
// Root cause: AgentProfile's form fields start at hardcoded useState
// defaults (name='', model='', ...). Before this fix, useAutoSave's
// `disabled` option was never set for this call, so it defaulted to
// `false` for the ENTIRE life of the component — useAutoSave's own "skip
// first render" baseline-capture logic ran on the very first commit
// (mount, before the agent query had resolved), seeding its baseline from
// the all-defaults state. The LATER commit where the real agent data
// hydrates into those fields then looked like a genuine user edit, arming
// the 1500ms debounce and firing `updateAgent` — a PUT that just echoes
// the server's own data back, bumping `updated_at` and showing "✓ Saved
// just now" to a user who never touched the form.
//
// Field evidence: opening the read-only core agent Mia fired
// `GET /api/v1/agents/mia` followed by an unsolicited `PUT` ~1.5s later.
// Reproduced by 2 UAT testers with network capture.
describe('AgentProfile — D3: hydration must not trigger a spurious PUT', () => {
  it('opening a read-only core agent and touching nothing never calls updateAgent, even after the debounce window elapses (REVERT-PROOF: fails without the formHydrated gate)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 1500ms debounce so a spurious hydration-triggered save gets every
    // chance to fire.
    await new Promise((resolve) => setTimeout(resolve, 2200))
    expect(updateAgent).not.toHaveBeenCalled()
  })

  it('the same passive-open holds for an editable (non-locked) agent too — the bug is not locked-agent-specific', async () => {
    // Explicitly guards against reintroducing an `agent.locked` guard as
    // the "fix" — the RCA confirmed that's the wrong mechanism, since the
    // spurious PUT fires for every agent, locked or not.
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    await new Promise((resolve) => setTimeout(resolve, 2200))
    expect(updateAgent).not.toHaveBeenCalled()
  })

  // Root cause was inside useAutoSave.ts itself: `initializedRef` seeded its
  // "skip first render" baseline exactly once per hook instance and never
  // re-armed on a LATER `disabled: true→false` transition, so only the
  // FIRST entity a long-lived hook instance ever hydrated got a safe
  // baseline — every subsequent entity's hydration still looked like a
  // genuine edit relative to the previous entity's stale baseline. Fixed by
  // making useAutoSave itself re-arm `previousJsonRef`/`lastSavedJsonRef`
  // from the CURRENT `data` on every `disabled` true→false transition (see
  // `wasDisabledRef` in useAutoSave.ts), not just the hook's very first
  // commit — so `formHydrated`'s existing false→true→false→true cycle
  // across agent switches (AgentProfile.tsx:284/884) now gets a fresh
  // baseline every time, not just the first.
  it('REVERT-PROOF: switching to a SECOND agent within the same mounted AgentProfile instance (it does not unmount on agent switch) does not fire a spurious PUT for that second agent', async () => {
    vi.mocked(fetchAgent).mockImplementation((id: string) =>
      Promise.resolve(id === 'mia' ? mockLockedCoreAgent : mockCoreAgent),
    )
    const client = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="general-assistant" />
      </QueryClientProvider>,
    )
    await screen.findByText('General Assistant')
    // Let the first agent's hydration settle well past the debounce window
    // before switching — the passive-open tests above already cover the
    // first-agent case; this just establishes a clean starting point.
    await new Promise((resolve) => setTimeout(resolve, 2200))
    expect(updateAgent).not.toHaveBeenCalled()

    // Switch to a SECOND agent WITHOUT unmounting — same QueryClientProvider,
    // same <AgentProfile> element position, only the `agentId` prop changes.
    // This mirrors the real app: AgentProfile is mounted at the route/layout
    // level and does not unmount when the operator opens a different agent
    // (see the `formHydrated` doc comment in AgentProfile.tsx).
    rerender(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="mia" />
      </QueryClientProvider>,
    )
    await screen.findByText('Mia')
    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 1500ms debounce so a spurious hydration-triggered save of the SECOND
    // agent gets every chance to fire.
    await new Promise((resolve) => setTimeout(resolve, 2200))
    expect(updateAgent).not.toHaveBeenCalled()
  })

  // EMPIRICAL TEST — 7-reviewer-gate Finding B (fix/uat-v0.1.1-defects):
  //
  // A different reviewer suspected AgentProfile could still coalesce its
  // hydration-readiness reset on a CACHED revisit. AgentProfile uses TWO
  // plain effects — `[agentId]` resets `formHydrated` to false, `[agentId,
  // agent]` sets it back to true once `agent` is available — instead of the
  // mid-render state-adjustment pattern WorkspaceTeamTab/WorkspaceSettingsTab
  // use specifically because a plain effect pair CAN be coalesced by React
  // when the new entity's `agent` is already cache-resolved on the very
  // SAME render as the `agentId` change: both effects fire back-to-back in
  // the same passive-effect flush, and their `setFormHydrated(false)` then
  // `setFormHydrated(true)` calls batch into a single re-render — so
  // `disabled` (`!formHydrated`) passed to useAutoSave never actually
  // commits as `true` in between. Without that real false-render,
  // useAutoSave's `wasDisabledRef` re-arm (the D3 fix above) never engages,
  // and the revisited entity's freshly-hydrated data is diffed against the
  // PREVIOUS entity's stale baseline — indistinguishable from a genuine
  // edit, arming the debounce and firing a spurious echo PUT.
  //
  // The existing D3 regression test above (switching to a SECOND agent)
  // cannot exercise this: `fetchAgent`'s mock always returns a promise, so
  // the second agent's `agent` is undefined for at least one render — a
  // real (non-coalesced) false→true cycle. This test instead REVISITS an
  // agent whose data is already sitting in the QueryClient cache from an
  // earlier fetch in this same test (a genuine cache hit, synchronously
  // resolved on the same render as the `agentId` prop change), which is
  // the precondition the reviewer flagged.
  it('EMPIRICAL (Finding B): revisiting an ALREADY-CACHED agent (A→B→A) does not fire a spurious PUT for the revisited agent', async () => {
    vi.mocked(fetchAgent).mockImplementation((id: string) =>
      Promise.resolve(id === 'mia' ? mockLockedCoreAgent : mockCoreAgent),
    )
    const client = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="general-assistant" />
      </QueryClientProvider>,
    )
    await screen.findByText('General Assistant')
    // general-assistant's data is now cached in `client` (queryKey
    // ['agent', 'general-assistant']).
    await new Promise((resolve) => setTimeout(resolve, 2200))
    expect(updateAgent).not.toHaveBeenCalled()

    // Switch to a SECOND agent — first-ever fetch for 'mia', so this leg
    // behaves like the existing D3 test above (a real, non-coalesced cycle).
    rerender(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="mia" />
      </QueryClientProvider>,
    )
    await screen.findByText('Mia')
    await new Promise((resolve) => setTimeout(resolve, 2200))
    expect(updateAgent).not.toHaveBeenCalled()

    // REVISIT general-assistant. Its data is already cached from the first
    // render above, so `useQuery` can resolve it SYNCHRONOUSLY on the same
    // render as the `agentId` prop flipping back — the cache-hit precondition
    // for the coalescing hazard Finding B raised.
    rerender(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="general-assistant" />
      </QueryClientProvider>,
    )
    await screen.findByText('General Assistant')
    await new Promise((resolve) => setTimeout(resolve, 2200))
    expect(updateAgent).not.toHaveBeenCalled()
  })
})

// Guard-noise fix (AgentProfile.tsx:405-450): the `updated_at` monotonic
// hydration guard used to conflate "genuinely stale" (`<`) with "identical,
// nothing to apply" (`===`) under one `<=` branch, console.warn-ing (+
// logDiagnostic) on BOTH. Only the `<` case is load-bearing noise; `===` is
// a normal no-op (e.g. a same-content invalidateQueries echo) and must stay
// silent.
describe('AgentProfile — guard-noise fix: stale vs identical updated_at', () => {
  it('warns via console.warn for a genuinely OLDER incoming snapshot (still load-bearing)', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const t0 = '2026-01-01T00:00:10.000Z'
    const t1 = '2026-01-01T00:00:05.000Z' // older than t0
    const first: Agent = { ...mockCoreAgent, updated_at: t0 }
    vi.mocked(fetchAgent).mockResolvedValue(first)
    const client = makeClient()
    renderProfile('general-assistant', client)
    await screen.findByText('General Assistant')
    warnSpy.mockClear()

    // Simulate a stale refetch landing with an OLDER updated_at than what
    // was already incorporated — queryClient.setQueryData mimics a raw
    // cache write the way a race-y refetch would.
    client.setQueryData(['agent', 'general-assistant'], { ...first, updated_at: t1 })

    await waitFor(() => {
      expect(warnSpy).toHaveBeenCalledWith(
        'agentProfile.updatedAtGuardRejectedHydration',
        expect.anything(),
      )
    })
    warnSpy.mockRestore()
  })

  it('does NOT call console.warn for an IDENTICAL incoming snapshot (silent no-op)', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const t0 = '2026-01-01T00:00:10.000Z'
    const first: Agent = { ...mockCoreAgent, updated_at: t0 }
    vi.mocked(fetchAgent).mockResolvedValue(first)
    const client = makeClient()
    renderProfile('general-assistant', client)
    await screen.findByText('General Assistant')
    warnSpy.mockClear()

    // Same-content echo: identical updated_at, identical everything else —
    // e.g. a same-content invalidateQueries refetch.
    client.setQueryData(['agent', 'general-assistant'], { ...first })

    // Give the effect a chance to run; then assert no warning was ever logged.
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(warnSpy).not.toHaveBeenCalledWith(
      'agentProfile.updatedAtGuardRejectedHydration',
      expect.anything(),
    )
    warnSpy.mockRestore()
  })
})

// US-E6: per-agent skill assignment tests
// Traces to: nontech-ux-hardening-spec.md §6.5, F-06
describe('AgentProfile — Skills picker (US-E6)', () => {
  // Item 2 reorg: Skills now has its own tab literally labeled "Skills" —
  // both the desktop TabsTrigger and the mobile AccordionTrigger also
  // render that exact text, so a bare `findByText(/^Skills$/i)` now
  // matches 3 elements (trigger, trigger, section heading). Scope to the
  // active tabpanel (Radix Tabs.Content, role="tabpanel") to find only the
  // section heading inside it.
  async function skillsTabPanel() {
    return screen.findByRole('tabpanel')
  }

  it('always shows "Skills" section heading', async () => {
    // Open it and assert the section heading is present (the heading is
    // always visible inside the tab panel).
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-skills')
    const panel = await skillsTabPanel()
    expect(within(panel).getByText(/^Skills$/i)).toBeInTheDocument()
  })

  it('shows empty state when no skills are installed', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([])
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-skills')
    const panel = await skillsTabPanel()
    expect(within(panel).getByText(/^Skills$/i)).toBeInTheDocument()
  })

  it('new agent with no skills shows 0 granted count (not labeled)', async () => {
    // A new agent has skills = [] or undefined; the count badge only renders when > 0.
    const agentNoSkills: Agent = { ...mockCoreAgent, skills: undefined }
    vi.mocked(fetchAgent).mockResolvedValue(agentNoSkills)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-skills')
    const panel = await skillsTabPanel()
    within(panel).getByText(/^Skills$/i)
    // The "X granted" badge must NOT appear when skills is empty/absent.
    expect(within(panel).queryByText(/granted/i)).toBeNull()
  })

  it('shows granted count badge when agent has skills', async () => {
    const agentWithSkills: Agent = { ...mockCoreAgent, skills: ['web-research', 'code-review'] }
    vi.mocked(fetchAgent).mockResolvedValue(agentWithSkills)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-skills')
    // "2 granted" badge in the section header
    expect(await screen.findByText(/2 granted/i)).toBeInTheDocument()
  })

  it('granting skills to agent A does not affect agent B rendering (AC2)', async () => {
    // Two separate renders simulate two agents. AC2: agent A's skills must not
    // bleed into agent B when the component is unmounted and remounted.
    const agentA: Agent = { ...mockCoreAgent, id: 'agent-a', name: 'Agent A', skills: ['web-research'] }
    const agentB: Agent = { ...mockCoreAgent, id: 'agent-b', name: 'Agent B', skills: [] }

    // Render agent A — should show 1 granted
    vi.mocked(fetchAgent).mockResolvedValue(agentA)
    const { unmount } = renderProfile('agent-a')
    await screen.findByText('Agent A')
    switchTab('tab-skills')
    expect(await screen.findByText(/1 granted/i)).toBeInTheDocument()
    unmount()

    // Render agent B — must show 0 granted (no badge)
    vi.mocked(fetchAgent).mockResolvedValue(agentB)
    renderProfile('agent-b')
    await screen.findByText('Agent B')
    switchTab('tab-skills')
    const panel = await skillsTabPanel()
    within(panel).getByText(/^Skills$/i)
    expect(within(panel).queryByText(/granted/i)).toBeNull()
  })
})

// B-2 extension — locked agent skills picker read-only
// Traces to: B-2 (#332 / US-D5) extended to Skills field, nontech-ux-hardening-spec §6.5
describe('AgentProfile — B-2: Skills picker read-only for locked agents', () => {
  const mockSkills: Skill[] = [
    { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: true, status: 'active' },
  ]

  beforeEach(() => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    vi.mocked(fetchSkills).mockResolvedValue(mockSkills)
  })

  it('shows "Skill assignment is read-only" notice for locked agents when tab is open', async () => {
    renderProfile('mia')
    await screen.findByText('Mia')
    // Open the Skills tab (item 2 reorg — split from the former Tools tab)
    switchTab('tab-skills')
    // The read-only notice must be visible
    expect(await screen.findByText(/skill assignment is read-only/i)).toBeInTheDocument()
  })

  it('renders skill checkboxes as disabled for locked agents', async () => {
    renderProfile('mia')
    await screen.findByText('Mia')
    // Open the Skills tab (item 2 reorg — split from the former Tools tab)
    switchTab('tab-skills')
    // Wait for skill to appear
    const checkbox = await screen.findByTestId('skill-checkbox-web-research')
    expect((checkbox as HTMLInputElement).disabled).toBe(true)
  })
})

// Open the ExecutorSelector Runtime <Select> (Radix) and pick an option by
// accessible name. Radix renders options into a portal only when open.
function pickExecutorKind(optionName: RegExp) {
  fireEvent.click(screen.getByTestId('executor-kind-select'))
  const option = screen.getByRole('option', { name: optionName })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

// Spec-4 FR-4.1 — Executor section wired into the worker agent profile.
// Workers are the only tier that gets an executor accordion. Base agents
// (core/custom/system) run native/in-process only — no third-party
// executor is selectable, so the entire accordion is omitted rather than
// rendered disabled.
describe('AgentProfile — Executor section is worker-only (Spec-4)', () => {
  it('renders the Executor accordion for a worker (absent executor → native default)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, executor: undefined })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    // Executor is in the default-open set for workers (W6-B1 / I1); only
    // click to open if the selector is not yet on screen.
    if (!screen.queryByTestId('executor-kind-select')) {
      switchTab('tab-advanced')
    }
    const kind = await screen.findByTestId('executor-kind-select')
    // Absent executor → native default (Radix SelectValue renders the label).
    expect(kind).toHaveTextContent(/Native/i)
  })

  it('hydrates an existing external-cli executor and its cli on a worker', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockWorkerAgent,
      executor: { kind: 'external-cli', cli: 'codex' },
    })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    if (!screen.queryByTestId('executor-kind-select')) {
      switchTab('tab-advanced')
    }
    const kind = await screen.findByTestId('executor-kind-select')
    expect(kind).toHaveTextContent(/External CLI/i)
    const cli = await screen.findByTestId('executor-cli-select')
    expect(cli).toHaveTextContent(/Codex/i)
  })

  it('persists a worker runtime change through updateAgent (auto-save)', async () => {
    vi.mocked(updateAgent).mockResolvedValue(mockWorkerAgent)
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, executor: undefined })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    if (!screen.queryByTestId('executor-kind-select')) {
      switchTab('tab-advanced')
    }
    await screen.findByTestId('executor-kind-select')
    pickExecutorKind(/External CLI/i)
    // The cli select now appears with the claude-code default.
    const cli = await screen.findByTestId('executor-cli-select')
    expect(cli).toHaveTextContent(/Claude Code/i)
    // Auto-save debounces, then PUTs the executor with the worker id.
    await waitFor(
      () => {
        expect(updateAgent).toHaveBeenCalled()
        const lastCall = vi.mocked(updateAgent).mock.calls.at(-1)!
        expect(lastCall[0]).toBe('web-researcher')
        expect(lastCall[1].executor).toEqual({ kind: 'external-cli', cli: 'claude-code' })
      },
      { timeout: 6000 },
    )
  })

  it('renders the worker runtime selector disabled for locked workers', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockLockedWorkerAgent,
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    renderProfile('marketplace-pack-worker')
    await screen.findByText('Marketplace Worker')
    if (!screen.queryByTestId('executor-kind-select')) {
      switchTab('tab-advanced')
    }
    const kind = await screen.findByTestId('executor-kind-select')
    expect(kind).toBeDisabled()
    // The test button is hidden for locked agents.
    expect(screen.queryByTestId('runner-test-button')).toBeNull()
  })

  // Wave 6 / I12 — runner-test guard. When the user saves a worker with an
  // external-cli executor, the save flow runs testAgentRunner BEFORE
  // updateAgent. A failure aborts the save (no silent commit). A success
  // caches the signature so subsequent saves for the same kind+cli do not
  // re-fire the test (avoids hammering the runner on every keystroke).
  it('I12: calls testAgentRunner before updateAgent when transitioning to external-cli', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, executor: undefined })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    if (!screen.queryByTestId('executor-kind-select')) {
      switchTab('tab-advanced')
    }
    await screen.findByTestId('executor-kind-select')
    pickExecutorKind(/External CLI/i)
    await waitFor(() => {
      expect(testAgentRunner).toHaveBeenCalledWith('web-researcher')
    }, { timeout: 6000 })
    // And the save still commits (test returned ok).
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
  })

  it('I12: blocks updateAgent when the runner test fails (missing-binary)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, executor: undefined })
    vi.mocked(testAgentRunner).mockResolvedValue({
      ok: false,
      reason: 'missing-binary',
      message: 'claude not found on PATH',
      cli: 'claude-code',
    })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    if (!screen.queryByTestId('executor-kind-select')) {
      switchTab('tab-advanced')
    }
    await screen.findByTestId('executor-kind-select')
    pickExecutorKind(/External CLI/i)
    await waitFor(() => expect(testAgentRunner).toHaveBeenCalled(), { timeout: 6000 })
    // Item 9a hardening: this is a real-timer NEGATIVE window — it only
    // proves anything if it spans the full 1500ms debounce (raised from the
    // 500ms default; see AgentProfile.tsx's main useAutoSave call). At
    // 800ms the debounce timer hasn't even fired yet, so "not called" was
    // trivially true regardless of whether the runner-test block actually
    // works. Give the auto-save a chance to attempt (and be blocked from
    // completing) the save.
    await new Promise((r) => setTimeout(r, 1800))
    expect(updateAgent).not.toHaveBeenCalled()
  })

  it('I12: skips testAgentRunner when executor is native (no need to test native runners)', async () => {
    // Use a fresh agent id never seen before so the testedExecutorSig cache
    // is empty and any call to testAgentRunner would be observed.
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockWorkerAgent,
      id: 'native-only-worker',
      name: 'Native Worker',
      executor: { kind: 'native' },
    })
    renderProfile('native-only-worker')
    await screen.findByText('Native Worker')
    // Wait for hydration + auto-save window to pass.
    await new Promise((r) => setTimeout(r, 800))
    expect(testAgentRunner).not.toHaveBeenCalled()
  })

  it('does NOT render the Executor accordion for a base (core) agent', async () => {
    // Base agents run native/in-process only. The locked concept
    // (`.preview-doc/agents.html`) makes the executor a worker-only
    // property; the backend rejects a non-native executor on a non-worker
    // and the FE mirrors that by hiding the field entirely.
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      // Even when the data carries an executor, the FE does not surface it
      // for base agents.
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.queryByText(/^Executor$/)).toBeNull()
    // The selector itself is not in the DOM for base agents.
    expect(screen.queryByTestId('executor-selector')).toBeNull()
  })
})

// Tier-branched form (locked concept: `.preview-doc/agents.html`).
// A worker is a delegation-only labour agent — never a chat target, no
// heartbeat, never the default. The form reflects that by HIDE-ing the
// worker-irrelevant accordions and relabelling the soul field as an
// optional "Task prompt". Base agents get the full set.
describe('AgentProfile — tier-branched form (worker vs base)', () => {
  it('worker form: shows Executor, hides Heartbeat, shows optional Task prompt', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, soul: '' })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    // Worker-only: Executor section is inside the Advanced tab.
    switchTab('tab-advanced')
    expect(await screen.findByText(/^Executor$/)).toBeInTheDocument()
    // Schedules section is removed from the Advanced tab entirely.
    expect(screen.queryByText(/^Schedules$/)).toBeNull()
    // Open the Personality tab and assert the worker relabel + no heartbeat
    switchTab('tab-personality')
    const taskPrompt = await screen.findByTestId('worker-task-prompt')
    // Worker task prompt is required (spec change: remove optional label).
    expect((taskPrompt as HTMLTextAreaElement).required).toBe(true)
    expect(taskPrompt.getAttribute('aria-required')).toBe('true')
    // The "Personality & instructions" persona framing is gone for workers
    expect(screen.queryByText(/Personality\s*&\s*instructions/i)).toBeNull()
    // No heartbeat affordances for workers
    expect(screen.queryByText(/Enable heartbeat/i)).toBeNull()
    expect(screen.queryByText(/HEARTBEAT\.md/i)).toBeNull()
    // No "Set as default" / default-★ control in the profile for workers
    // (the default-★ lives on AgentCard; the profile must never surface it
    // for workers — they are never the default per the locked concept).
    expect(screen.queryByRole('button', { name: /set as default/i })).toBeNull()
  })

  it('worker form: shows the runtime selector AND a Test-run path on external-cli', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    // Executor is in the default-open set for workers (W6-B1 / I1); only
    // click to open if the runner-test button is not yet on screen.
    if (!screen.queryByTestId('runner-test-button')) {
      switchTab('tab-advanced')
    }
    // Test Connection button is part of the ExecutorSelector and only
    // renders for workers. Confirms the "Test-run" action requirement.
    expect(await screen.findByTestId('runner-test-button')).toBeInTheDocument()
  })

  it('base (core) form: hides Executor, hides Schedules, no agent-level heartbeat (now workspace-scoped)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Base: Advanced tab — no Executor (workers only), no Schedules (removed).
    switchTab('tab-advanced')
    expect(screen.queryByText(/^Executor$/)).toBeNull()
    expect(screen.queryByText(/^Schedules$/)).toBeNull()
    // Heartbeat is now workspace-scoped (spec A1/F-10) — no agent-level heartbeat UI.
    switchTab('tab-personality')
    expect(screen.queryByText(/Enable heartbeat/i)).toBeNull()
  })

  it('base (core) form: still has the "Personality & instructions" framing (no worker relabel)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Behavior may already be open by default (W6-B1 / I1) — only click
    // if the framing heading is not yet on screen.
    if (!screen.queryByText(/Personality\s*&\s*instructions/i)) {
      switchTab('tab-personality')
    }
    expect(await screen.findByText(/Personality\s*&\s*instructions/i)).toBeInTheDocument()
    // Worker relabel is absent
    expect(screen.queryByText(/Task prompt/i)).toBeNull()
  })

  it('custom (base) form: also hides the Executor and hides Schedules', async () => {
    // Custom agents are base-tier too — they run native only, never
    // external CLI. The split is worker vs non-worker, not core vs custom.
    // Wire contract: user-created chat agents use type='Main' (the
    // 'custom' enum value was retired in the Wave 1 spec).
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Schedules section is removed; Executor is worker-only.
    switchTab('tab-advanced')
    expect(screen.queryByText(/^Executor$/)).toBeNull()
    expect(screen.queryByText(/^Schedules$/)).toBeNull()
  })
})

// Provider-aware fallback editor.
//
// Each chip shows provider name + model; the add UI is a `<ModelSelector>`
// with provider grouping. The wire payload is `[{model, provider}]` — the
// editor tracks it 1:1 and emits the same shape on save (no projection in
// or out). The provider field is what makes US-3 (rate-limit on primary's
// provider doesn't poison the fallback's provider) possible.
describe('AgentProfile — provider-aware fallback editor', () => {
  // Item 1 reorg: the fallback editor now lives on Basics, directly below
  // the Model section, which is the DEFAULT-open tab/accordion. Desktop
  // Tabs and mobile Accordion both default to "basics" and are
  // simultaneously mounted (established pattern in this file — see the
  // locked-agent shell_policy persistence test's use of getAllBy*), so
  // every query below uses the *All* variant and acts on the first
  // (desktop) match for interactions.
  async function openFallbackEditor(agent: typeof mockCoreAgent = mockCoreAgent) {
    vi.mocked(fetchAgent).mockResolvedValue(agent)
    renderProfile(agent.id)
    await screen.findByText(agent.name)
    // Fallback models is on Basics, the default-open tab — no switch
    // needed. Wait for both the desktop and mobile copies to hydrate.
    await waitFor(() => {
      expect(screen.getAllByText(/Fallback models/i).length).toBeGreaterThanOrEqual(1)
    })
  }

  it('renders existing fallbacks as chips with model name and provider badge', async () => {
    // Hydrate with two fallbacks across two different providers.
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
        { model: 'claude-sonnet-4-6', provider: 'anthropic' },
      ],
    })
    // Each model name appears as a chip (desktop + mobile copies)
    expect(screen.getAllByTestId('fallback-chip-model-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByTestId('fallback-chip-model-claude-sonnet-4-6').length).toBeGreaterThanOrEqual(1)
    // Each chip has a provider badge (FR-005)
    expect(screen.getAllByTestId('fallback-chip-provider-z-ai/glm-5-turbo')[0]).toHaveTextContent(/openrouter/i)
    expect(screen.getAllByTestId('fallback-chip-provider-claude-sonnet-4-6')[0]).toHaveTextContent(/anthropic/i)
  })

  it('removes a fallback chip via the X button', async () => {
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
        { model: 'claude-sonnet-4-6', provider: 'anthropic' },
      ],
    })
    // Both present initially
    expect(screen.getAllByTestId('fallback-chip-model-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByTestId('fallback-chip-model-claude-sonnet-4-6').length).toBeGreaterThanOrEqual(1)
    // Click the remove button on the first (desktop) chip — both copies
    // share the same underlying state, so this removes it everywhere.
    fireEvent.click(screen.getAllByTestId('fallback-chip-remove-z-ai/glm-5-turbo')[0])
    // First chip is gone, second remains
    expect(screen.queryAllByTestId('fallback-chip-model-z-ai/glm-5-turbo').length).toBe(0)
    expect(screen.getAllByTestId('fallback-chip-model-claude-sonnet-4-6').length).toBeGreaterThanOrEqual(1)
  })

  it('adds a fallback via the ModelSelector with provider tracking', async () => {
    // Provider tracking on add: picking a model in the dropdown must record
    // both model AND the provider that owns it (per spec, the fallback can
    // route through a different provider).
    await openFallbackEditor({ ...mockCoreAgent, fallback_models: [] })

    // The "add fallback" ModelSelector is mounted (desktop + mobile). Open
    // the desktop one and pick a model. The trigger button is identified by
    // data-testid on the add-fallback selector (the primary model selector
    // above uses a different testid).
    const addTrigger = screen.getAllByTestId('fallback-add-trigger')[0]
    fireEvent.click(addTrigger)

    // The CommandItem for claude-sonnet-4-6 lives inside the popover content.
    // OpenAI / Anthropic models appear under their provider heading.
    const item = await screen.findByTestId('fallback-add-item-claude-sonnet-4-6')
    fireEvent.click(item)

    // The new chip should now appear with both model and provider
    expect((await screen.findAllByTestId('fallback-chip-model-claude-sonnet-4-6')).length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByTestId('fallback-chip-provider-claude-sonnet-4-6')[0]).toHaveTextContent(/anthropic/i)
  })

  it('persists the fallback list with model AND provider on save', async () => {
    // The wire shape is [{model, provider}] — the save payload carries
    // both fields for each entry, not just the slug.
    vi.mocked(updateAgent).mockResolvedValue(mockCoreAgent)
    vi.mocked(updateAgent).mockClear() // ignore earlier test's calls
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
        { model: 'claude-sonnet-4-6', provider: 'anthropic' },
      ],
    })

    // Remove one
    fireEvent.click(screen.getAllByTestId('fallback-chip-remove-z-ai/glm-5-turbo')[0])
    // Wait for the auto-save debounce + flush
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls
        expect(calls.length).toBeGreaterThan(0)
      },
      { timeout: 6000 },
    )
    // Filter to calls for THIS agent id only — other tests in the file
    // (e.g. the worker tier-branched test) call updateAgent with a
    // different id, and the auto-save's last-call assertion would
    // otherwise pick up a stale call from another describe block.
    const callsForAgent = vi.mocked(updateAgent).mock.calls.filter(
      ([id]) => id === mockCoreAgent.id,
    )
    const last = callsForAgent.at(-1)!
    expect(Array.isArray(last[1].fallback_models)).toBe(true)
    // After removing z-ai/glm-5-turbo, only claude-sonnet-4-6 remains —
    // emitted with BOTH model and provider (the wire shape is object, not string).
    expect(last[1].fallback_models).toEqual([
      { model: 'claude-sonnet-4-6', provider: 'anthropic' },
    ])
  })

  it('a single fallback-model change fires exactly one PUT — no phantom passive-idle second PUT (UAT data-loss regression)', async () => {
    // Regression for a live UAT data-loss bug: adding a fallback model via
    // this exact picker fired a SECOND `PUT /api/v1/agents/{id}` ~600ms
    // after the first, purely passively — no tab switch, no visibility
    // change, no further user action, just idle time passing — and that
    // second PUT omitted `fallback_models` entirely, silently clobbering
    // the correct first save.
    //
    // Root cause: the save-success handler in AgentProfile.tsx patched the
    // React Query cache with ONLY `updated_at` from the PUT response
    // (`{ ...old, updated_at: resp.updated_at }`) instead of the full
    // response, leaving every OTHER field on the cached `agent` object —
    // including `fallback_models` — pointing at the STALE pre-save
    // snapshot. That still-new object reference re-triggered the
    // `[agentId, agent]` hydration effect; its only guard,
    // `isDirtyRef.current`, had ALREADY been cleared to `false` by this
    // same save's own success path moments earlier, so the effect
    // re-hydrated every field from the stale-except-updated_at object,
    // silently reverting local `fallbackModels` state back to empty. That
    // reversion looked like a genuine new edit to `useAutoSave`, which
    // fired a second, correctly-serialized (non-overlapping — FIX 3 was
    // never at fault here) debounce cycle carrying the wrong, reverted
    // data.
    //
    // `updateAgent` here echoes back the FULL updated agent (mirroring the
    // real backend's PUT contract — "returns the complete updated agent
    // object" per contracts/openapi.yaml) rather than the static
    // `mockCoreAgent` stub the other tests in this file use — this test
    // would have failed against the pre-fix code, which discarded
    // everything from that response except `updated_at`.
    //
    // `updated_at` is set on BOTH the initial `fetchAgent` fixture and
    // each `updateAgent` response, strictly increasing, so the
    // monotonic-`updated_at` hydration guard can actually engage: the
    // `fetchAgent` mock stays STATIC for the lifetime of this test (it
    // does not learn about the save), so the background refetch that
    // `invalidateQueries` fires after the save is itself a second,
    // independent source of a stale (older `updated_at`) `agent`
    // snapshot — proving the guard, not just the full-response cache
    // patch, is what keeps a stale reference from reverting local state.
    const baseUpdatedAt = Date.parse('2026-01-01T00:00:00.000Z')
    let callCount = 0
    vi.mocked(updateAgent).mockReset().mockImplementation(async (_id, body) => {
      callCount += 1
      return {
        ...mockCoreAgent,
        ...body,
        updated_at: new Date(baseUpdatedAt + callCount * 1000).toISOString(),
      } as Agent
    })
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [],
      updated_at: new Date(baseUpdatedAt).toISOString(),
    })

    fireEvent.click(screen.getAllByTestId('fallback-add-trigger')[0])
    const item = await screen.findByTestId('fallback-add-item-claude-sonnet-4-6')
    fireEvent.click(item)

    // The first (and — this test asserts — ONLY) debounced save.
    await waitFor(
      () => expect(vi.mocked(updateAgent).mock.calls.length).toBeGreaterThan(0),
      { timeout: 6000 },
    )
    const firstCallCount = vi.mocked(updateAgent).mock.calls.length
    expect(firstCallCount).toBe(1)
    const firstPayload = vi.mocked(updateAgent).mock.calls[0][1]
    expect(firstPayload.fallback_models).toEqual([
      { model: 'claude-sonnet-4-6', provider: 'anthropic' },
    ])

    // PASSIVE idle wait — no further clicks, no tab switch, no
    // visibility/unload event — mirrors the UAT tester's exact repro
    // ("do nothing else — wait ~2s"). Wait comfortably past another full
    // debounce interval (default 500ms) so a phantom second save gets
    // every chance to fire.
    await new Promise((resolve) => setTimeout(resolve, 1200))

    // Exactly one PUT must have fired — no stale-closure second PUT
    // silently clobbering the first, correct save.
    expect(vi.mocked(updateAgent).mock.calls.length).toBe(firstCallCount)
  })

  it('locked core agents: read-only fallback summary, no add-trigger, no provider picker (G6)', async () => {
    // W6-C2 / G6: `fallback_models` is wire-allowed for locked core
    // agents but the editor strips it via `canEdit`. Pre-C2 operators
    // had no way to see what the locked core compiled with; now we
    // surface the configured chain as a read-only summary so the
    // operator can verify the inherited fallback. Item 1 reorg: the two
    // locked-only summaries (formerly duplicated on Basics and Tools) are
    // now CONSOLIDATED into this single copy on Basics.
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockLockedCoreAgent,
      fallback_models: [
        { model: 'claude-opus-4-6', provider: 'anthropic' },
      ],
    })
    renderProfile('mia')
    await screen.findByText('Mia')
    // The summary panel is present (G6), the locked note is visible.
    // With desktop Tabs and mobile Accordion both in the DOM, the Basics
    // panel duplicates the summary; assert at least one is visible.
    expect(screen.getAllByTestId('fallback-summary-locked-basics').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText(/inherited from the locked core config/i).length).toBeGreaterThanOrEqual(1)
    // The summary lists the configured fallback (model + provider).
    expect(screen.getAllByTestId('fallback-summary-model-claude-opus-4-6')[0]).toHaveTextContent(/claude-opus-4-6/)
    expect(screen.getAllByTestId('fallback-summary-provider-claude-opus-4-6')[0]).toHaveTextContent(/anthropic/i)
    // The EDITOR affordances (chip, add-trigger, provider select) must
    // NOT render for locked agents — the summary is read-only.
    expect(screen.queryByTestId('fallback-chip-model-claude-opus-4-6')).toBeNull()
    expect(screen.queryByTestId('fallback-add-trigger')).toBeNull()
    expect(screen.queryByTestId('fallback-provider-select-claude-opus-4-6')).toBeNull()
  })

  it('locked core agents with no fallback_models show an "empty" summary line', async () => {
    // The summary must not crash when `fallback_models` is absent on a
    // locked agent — surface the empty-chain copy instead.
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    expect(screen.getAllByTestId('fallback-summary-locked-basics').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText(/no fallback chain configured/i).length).toBeGreaterThanOrEqual(1)
  })

  // W6-C2 / I9: per-chip provider picker. The fallback can route through
  // a different provider than the primary (FR-007), so the chip must
  // expose the provider as a pickable field that persists the
  // **provider id** (the wire routing key) on save.
  it('renders a per-chip provider picker bound to connected providers (I9)', async () => {
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
      ],
    })
    // The provider select is rendered for each chip (desktop + mobile).
    const providerSelect = screen.getAllByTestId('fallback-provider-select-z-ai/glm-5-turbo')[0]
    expect(providerSelect).toBeInTheDocument()
    expect((providerSelect as HTMLSelectElement).value).toBe('openrouter')
    // Connected providers are populated as options.
    expect(screen.getAllByTestId('fallback-provider-option-openrouter-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByTestId('fallback-provider-option-anthropic-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
    // An "empty" option exists so the user can clear the provider
    // (which surfaces the persistent warning — I11).
    expect(screen.getAllByTestId('fallback-provider-option-empty-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
  })

  it('changing the provider combobox persists the provider ID (not display name) on save (I9)', async () => {
    // Regression: pre-C2 the editor stored the provider DISPLAY name
    // in `entry.provider` (FR-007 spec: routing key, e.g. "openrouter").
    // Pin the wire payload as provider id; the display name is layered
    // separately at render time.
    vi.mocked(updateAgent).mockResolvedValue(mockCoreAgent)
    vi.mocked(updateAgent).mockClear()
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'claude-sonnet-4-6', provider: 'openrouter' },
      ],
    })
    // Switch the provider to anthropic — a different connected provider.
    fireEvent.change(screen.getAllByTestId('fallback-provider-select-claude-sonnet-4-6')[0], {
      target: { value: 'anthropic' },
    })
    // Wait for the auto-save debounce + flush.
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls.filter(
          ([id]) => id === mockCoreAgent.id,
        )
        expect(calls.length).toBeGreaterThan(0)
      },
      { timeout: 6000 },
    )
    const calls = vi.mocked(updateAgent).mock.calls.filter(
      ([id]) => id === mockCoreAgent.id,
    )
    const last = calls.at(-1)!
    expect(last[1].fallback_models).toEqual([
      // The wire value MUST be the provider ID, not "Anthropic" or
      // "OpenRouter". This is the I9 contract.
      { model: 'claude-sonnet-4-6', provider: 'anthropic' },
    ])
  })

  // W6-C2 / I10: reorder controls. The wire contract for
  // `fallback_models` says entries are tried in the order they appear,
  // so reordering changes runtime behavior. Up arrow disabled at index
  // 0; down arrow disabled at last index.
  it('renders up/down reorder buttons on each chip (I10)', async () => {
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
        { model: 'claude-sonnet-4-6', provider: 'anthropic' },
      ],
    })
    expect(screen.getAllByTestId('fallback-chip-up-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByTestId('fallback-chip-down-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByTestId('fallback-chip-up-claude-sonnet-4-6').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByTestId('fallback-chip-down-claude-sonnet-4-6').length).toBeGreaterThanOrEqual(1)
    // Up is disabled for the first chip.
    expect((screen.getAllByTestId('fallback-chip-up-z-ai/glm-5-turbo')[0] as HTMLButtonElement).disabled).toBe(true)
    // Down is disabled for the last chip.
    expect((screen.getAllByTestId('fallback-chip-down-claude-sonnet-4-6')[0] as HTMLButtonElement).disabled).toBe(true)
  })

  it('moving a fallback down reorders the wire array on save (I10)', async () => {
    vi.mocked(updateAgent).mockResolvedValue(mockCoreAgent)
    vi.mocked(updateAgent).mockClear()
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
        { model: 'claude-sonnet-4-6', provider: 'anthropic' },
      ],
    })
    // Move the first fallback DOWN — the array becomes [claude-sonnet-4-6, z-ai/glm-5-turbo].
    fireEvent.click(screen.getAllByTestId('fallback-chip-down-z-ai/glm-5-turbo')[0])
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls.filter(
          ([id]) => id === mockCoreAgent.id,
        )
        expect(calls.length).toBeGreaterThan(0)
      },
      { timeout: 6000 },
    )
    const calls = vi.mocked(updateAgent).mock.calls.filter(
      ([id]) => id === mockCoreAgent.id,
    )
    const last = calls.at(-1)!
    expect(last[1].fallback_models).toEqual([
      { model: 'claude-sonnet-4-6', provider: 'anthropic' },
      { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
    ])
  })

  // W6-C2 / I11: persistent indicator when the chip's provider field
  // is empty. The model is not in any connected provider, so the
  // fallback would silently fail at runtime.
  it('shows the persistent warning indicator + aria-label when provider is missing (I11)', async () => {
    // Hydrate with an entry whose provider was empty on the wire (e.g.
    // a free-text model that was never connected). After hydration the
    // chip's `provider` field stays empty because `modelToProvider`
    // cannot resolve the slug.
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
        { model: 'some-unconnected-slug', provider: '' },
      ],
    })
    // The unconnected chip has the persistent warning indicator.
    const warning = screen.getAllByTestId('fallback-chip-warning-some-unconnected-slug')[0]
    expect(warning).toBeInTheDocument()
    expect(warning.getAttribute('aria-label')).toBe(
      'Provider not connected — fallback will not be used at runtime',
    )
    expect(warning.getAttribute('title')).toBe(
      'Provider not connected — fallback will not be used at runtime',
    )
    // The connected chip does NOT show the indicator (regression guard).
    expect(screen.queryByTestId('fallback-chip-warning-z-ai/glm-5-turbo')).toBeNull()
  })

  it('clearing the provider combobox flips the chip into the warning state (I11)', async () => {
    // Sanity: an explicit user action — picking "—" on the provider
    // select — must trigger the warning indicator on that chip.
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
      ],
    })
    expect(screen.queryByTestId('fallback-chip-warning-z-ai/glm-5-turbo')).toBeNull()
    fireEvent.change(screen.getAllByTestId('fallback-provider-select-z-ai/glm-5-turbo')[0], {
      target: { value: '' },
    })
    await waitFor(() => {
      expect(screen.getAllByTestId('fallback-chip-warning-z-ai/glm-5-turbo').length).toBeGreaterThanOrEqual(1)
    })
  })
})

// Wave 5 / spec §6 — Edit slide-over body uses a 5-6 tab layout (Basics,
// Personality, Tools, Skills, Advanced [+Runtime for subagent_3p]
// [+Heartbeat with workspace context]) instead of the prior 10-section
// Accordion. Item 2 reorg split Skills out of the former combined Tools
// tab into its own tab. The `tab-basics` / `tab-personality` / `tab-tools`
// / `tab-skills` / `tab-advanced` / `tab-runtime` / `tab-heartbeat`
// testids anchor the tab bar; tests below exercise the spec BDDs
// (#15/#16/#17 from agent-form-requirements.md).
describe('AgentProfile — Wave 5 tab structure (spec §6.2-§6.4)', () => {
  it('renders 5 tabs (basics, personality, tools, skills, advanced) for a Main agent', async () => {
    // Traces to: agent-form-requirements.md §9.3 — "Edit slide-over (Main) shows tabs"
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getByTestId('tab-basics')).toBeInTheDocument()
    expect(screen.getByTestId('tab-personality')).toBeInTheDocument()
    expect(screen.getByTestId('tab-tools')).toBeInTheDocument()
    // Item 2 reorg: Skills is now its own tab.
    expect(screen.getByTestId('tab-skills')).toBeInTheDocument()
    expect(screen.getByTestId('tab-advanced')).toBeInTheDocument()
    // No Runtime tab for Main
    expect(screen.queryByTestId('tab-runtime')).toBeNull()
  })

  it('renders 4 tabs including Runtime (and NO Tools/Skills tab) for a subagent_3p agent', async () => {
    // Traces to: agent-form-requirements.md §9.3, amended by the field
    // matrix: the Tools and Skills tabs are omitted for external CLI
    // workers (every toolsPanel/skillsPanel section is out of scope for
    // them), so the external edit slide-over shows Basics / Personality /
    // Runtime / Advanced.
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      id: 'external-worker',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    expect(screen.getByTestId('tab-basics')).toBeInTheDocument()
    expect(screen.getByTestId('tab-personality')).toBeInTheDocument()
    expect(screen.queryByTestId('tab-tools')).toBeNull()
    expect(screen.queryByTestId('tab-skills')).toBeNull()
    expect(screen.getByTestId('tab-runtime')).toBeInTheDocument()
    expect(screen.getByTestId('tab-advanced')).toBeInTheDocument()
  })

  // Item 4 reorg: pin the tab ORDER for both native and external agents,
  // and heartbeat's position when present (between Skills and Advanced —
  // moved from visually-first). Order is read off the rendered
  // TabsTrigger DOM sequence via `querySelectorAll`, which returns matches
  // in document order — not just presence — so a future accidental reorder
  // is caught.
  it('tab order for a native Main agent (no workspace context): Basics, Personality, Tools, Skills, Advanced', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    const tablist = screen.getByRole('tablist')
    const order = Array.from(tablist.querySelectorAll('[role="tab"]')).map(
      (el) => el.getAttribute('data-testid'),
    )
    expect(order).toEqual(['tab-basics', 'tab-personality', 'tab-tools', 'tab-skills', 'tab-advanced'])
  })

  it('tab order for a subagent_3p agent: Basics, Personality, Runtime, Advanced (no Tools/Skills/Heartbeat)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      id: 'external-worker',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    const tablist = screen.getByRole('tablist')
    const order = Array.from(tablist.querySelectorAll('[role="tab"]')).map(
      (el) => el.getAttribute('data-testid'),
    )
    expect(order).toEqual(['tab-basics', 'tab-personality', 'tab-runtime', 'tab-advanced'])
  })

  it('tab order with workspace context (Heartbeat shown): Basics, Personality, Tools, Skills, Heartbeat, Advanced', async () => {
    // FR-016 / US-5: Heartbeat only renders with workspace context AND for
    // a non-worker agent. `renderProfileWithWorkspace` + `mockWorkspace`
    // are declared later in this file as `function`/module-level `const`
    // bindings, both fully initialized before any `it()` body runs.
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    vi.mocked(fetchWorkspace).mockResolvedValue(mockWorkspace)
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    const tablist = await screen.findByRole('tablist')
    await waitFor(() => {
      expect(within(tablist).getByTestId('tab-heartbeat')).toBeInTheDocument()
    })
    const order = Array.from(tablist.querySelectorAll('[role="tab"]')).map(
      (el) => el.getAttribute('data-testid'),
    )
    expect(order).toEqual(['tab-basics', 'tab-personality', 'tab-tools', 'tab-skills', 'tab-heartbeat', 'tab-advanced'])
    // Reset store for subsequent tests in this file.
    useUiStore.setState({ editAgentWorkspaceId: null })
  })

  // Mobile accordion structural tests — mirrors the 3 tablist-order tests
  // above exactly (same fixtures, same 3 shapes), but against the `<
  // sm` accordion rendered alongside the desktop Tabs (AgentProfile.tsx's
  // `<Accordion type="single" collapsible ... className="block sm:hidden">`)
  // rather than the tablist. The accordion is built from the SAME
  // conditional branches (isExternalAgent / showHeartbeatTab) as the tab
  // bar, so its trigger sequence must match 1-for-1 — these tests catch a
  // reorder OR an accidental omission (e.g. deleting the mobile Skills
  // AccordionItem while leaving the desktop Skills tab in place) that the
  // desktop-only tablist tests above cannot see. AccordionTrigger renders a
  // plain `<button data-testid="accordion-*">` (see
  // src/components/ui/accordion.tsx) with no distinguishing ARIA role, so —
  // unlike the tablist queries above — triggers are read off their shared
  // `data-testid` prefix, in DOM order. Queried via `document.body` (NOT the
  // `render()`-returned `container`): AgentProfile renders inside a Radix
  // Sheet/Dialog, which portals its content straight to `document.body`,
  // outside the local `container` subtree — the same reason the tablist
  // queries above use `screen.getByRole` rather than `container` too.
  it('mobile accordion trigger order for a native Main agent (no workspace context): Basics, Personality, Tools, Skills, Advanced', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    const order = Array.from(document.body.querySelectorAll('[data-testid^="accordion-"]')).map(
      (el) => el.getAttribute('data-testid'),
    )
    expect(order).toEqual(['accordion-basics', 'accordion-personality', 'accordion-tools', 'accordion-skills', 'accordion-advanced'])
  })

  it('mobile accordion trigger order for a subagent_3p agent: Basics, Personality, Runtime, Advanced (no Tools/Skills/Heartbeat)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      id: 'external-worker',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    const order = Array.from(document.body.querySelectorAll('[data-testid^="accordion-"]')).map(
      (el) => el.getAttribute('data-testid'),
    )
    expect(order).toEqual(['accordion-basics', 'accordion-personality', 'accordion-runtime', 'accordion-advanced'])
  })

  it('mobile accordion trigger order with workspace context (Heartbeat shown): Basics, Personality, Tools, Skills, Heartbeat, Advanced', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    vi.mocked(fetchWorkspace).mockResolvedValue(mockWorkspace)
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    await waitFor(() => {
      expect(screen.getByTestId('accordion-heartbeat')).toBeInTheDocument()
    })
    const order = Array.from(document.body.querySelectorAll('[data-testid^="accordion-"]')).map(
      (el) => el.getAttribute('data-testid'),
    )
    expect(order).toEqual(['accordion-basics', 'accordion-personality', 'accordion-tools', 'accordion-skills', 'accordion-heartbeat', 'accordion-advanced'])
    // Reset store for subsequent tests in this file.
    useUiStore.setState({ editAgentWorkspaceId: null })
  })

  it('clicking the Personality tab activates the personality content', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Click the Personality tab and verify the soul textarea (BehaviorFields)
    // is now in the active panel. Radix Tabs render the active panel and
    // the inactive ones are kept in the DOM but hidden via data-state.
    switchTab('tab-personality')
    // The soul textarea has testid "agent-soul" in BehaviorFields.
    expect(await screen.findByTestId('agent-soul')).toBeInTheDocument()
  })
})

// Wave 5 / spec §6 BDD #13 — Locked agents (core + locked) show the
// locked-banner with the spec copy at the top of the body.
describe('AgentProfile — locked banner (spec §6 BDD #13)', () => {
  it('renders the locked-banner for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    const banner = screen.getByTestId('locked-banner')
    expect(banner).toHaveAttribute('role', 'alert')
    expect(banner).toHaveTextContent(/built-in core agent/i)
    expect(banner).toHaveTextContent(/most fields are read-only/i)
  })

  it('does NOT render the locked-banner for a non-locked agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.queryByTestId('locked-banner')).toBeNull()
  })
})

// ADR-052 FR-038 (soul/rubric unification) + FR-039 (memory_enabled).
describe('AgentProfile — ADR-052 soul unification + memory toggle (FR-038/FR-039)', () => {
  it('renders the soul textarea read-only for a locked core agent (no dead edits)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    switchTab('tab-personality')
    const soulTextarea = await screen.findByTestId('agent-soul')
    expect((soulTextarea as HTMLTextAreaElement).disabled).toBe(true)
    expect((soulTextarea as HTMLTextAreaElement).readOnly).toBe(true)
    // The "no dead code" guarantee: a locked agent's soul edit must never be
    // silently discarded — making the field read-only (rather than editable
    // but stripped from the PUT payload) is how that's enforced.
  })

  it('renders the System-agent banner naming identity as locked and soul (with model/provider) as editable (ADR-052 Fix-Wave-2 carve-out)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockJudgeAgent)
    renderProfile('judge')
    await screen.findByText('Judge')
    const banner = screen.getByTestId('locked-banner')
    expect(banner).toHaveTextContent(/system agent/i)
    expect(banner).toHaveTextContent(/identity/i)
    expect(banner).toHaveTextContent(/is locked/i)
    expect(banner).toHaveTextContent(/soul/i)
    expect(banner).toHaveTextContent(/editable/i)
    expect(banner).not.toHaveTextContent(/rubric/i)
    // The old "soul editing isn't available yet" copy must be gone — the
    // backend now genuinely accepts it (updateAgent's IsSystem() carve-out).
    expect(banner).not.toHaveTextContent(/isn.t available yet/i)
  })

  it('renders the Judge soul textarea EDITABLE (ADR-052 Fix-Wave-2 carve-out) — identity stays locked, soul does not', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockJudgeAgent)
    renderProfile('judge')
    await screen.findByText('Judge')
    switchTab('tab-personality')
    const soulTextarea = await screen.findByTestId('agent-soul')
    expect((soulTextarea as HTMLTextAreaElement).disabled).toBe(false)
    expect((soulTextarea as HTMLTextAreaElement).readOnly).toBe(false)
    expect((soulTextarea as HTMLTextAreaElement).value).toBe(mockJudgeAgent.soul)
    expect(screen.queryByText(/rubric/i)).toBeNull()
  })

  it('persists a Judge soul edit through updateAgent (System Agent soul carve-out, backend PUT allows it)', async () => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockJudgeAgent)
    const editedSoul = 'You are the Judge. Operator-edited: require a green CI run before PASS.'
    vi.mocked(updateAgent).mockReset().mockResolvedValue({ ...mockJudgeAgent, soul: editedSoul })
    renderProfile('judge')
    await screen.findByText('Judge')
    switchTab('tab-personality')
    const soulTextarea = await screen.findByTestId('agent-soul')

    vi.useFakeTimers()
    fireEvent.change(soulTextarea, { target: { value: editedSoul } })
    await act(async () => { vi.advanceTimersByTime(1600) })
    vi.useRealTimers()

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith(
        'judge',
        expect.objectContaining({ soul: editedSoul }),
      )
    }, { timeout: 6000 })
    // The still-locked identity fields must never ride along on this save —
    // only soul (plus whatever else the autosave payload already includes
    // for every agent, e.g. model) is exempted for a System Agent.
    const [, payload] = vi.mocked(updateAgent).mock.calls[0]
    expect(payload).not.toHaveProperty('name')
    expect(payload).not.toHaveProperty('description')
    expect(payload).not.toHaveProperty('color')
    expect(payload).not.toHaveProperty('icon')
    expect(payload).not.toHaveProperty('skills')
  })

  it('shows the Memory toggle OFF and disabled for the Judge (System agent)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockJudgeAgent)
    renderProfile('judge')
    await screen.findByText('Judge')
    switchTab('tab-personality')
    const memorySwitch = await screen.findByTestId('memory-toggle')
    expect(memorySwitch).toHaveAttribute('data-state', 'unchecked')
    expect(memorySwitch).toBeDisabled()
    expect(screen.getByTestId('memory-toggle-row')).toHaveTextContent(/verifier agents always run with memory off/i)
  })

  it('shows a live, editable Memory toggle (default ON) for an ordinary agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-personality')
    const memorySwitch = await screen.findByTestId('memory-toggle')
    expect(memorySwitch).toHaveAttribute('data-state', 'checked')
    expect(memorySwitch).not.toBeDisabled()
  })

  it('persists a Memory toggle-off through updateAgent for an ordinary (non-locked) agent', async () => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockCoreAgent)
    vi.mocked(updateAgent).mockReset().mockResolvedValue({ ...mockCoreAgent, memory_enabled: false })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-personality')
    const memorySwitch = await screen.findByTestId('memory-toggle')
    fireEvent.click(memorySwitch)
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith(
        'general-assistant',
        expect.objectContaining({ memory_enabled: false }),
      )
    }, { timeout: 6000 })
  })

  it('persists a Memory toggle-on for a LOCKED core agent (memory_enabled is allowed on all agents, unlike soul/name)', async () => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue({ ...mockLockedCoreAgent, memory_enabled: false })
    vi.mocked(updateAgent).mockReset().mockResolvedValue({ ...mockLockedCoreAgent, memory_enabled: true })
    renderProfile('mia')
    await screen.findByText('Mia')
    switchTab('tab-personality')
    const memorySwitch = await screen.findByTestId('memory-toggle')
    expect(memorySwitch).not.toBeDisabled()
    fireEvent.click(memorySwitch)
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith(
        'mia',
        expect.objectContaining({ memory_enabled: true }),
      )
    }, { timeout: 6000 })
  })
})

// Wave 5 / spec §6.1 — Footer: last-saved-indicator (left) + delete-agent-button
// (right) + AlertDialog confirm. Locked agents hide the delete button.
describe('AgentProfile — Wave 5 footer (spec §6.1)', () => {
  it('renders last-saved-indicator and delete-agent-button for an unlocked agent', async () => {
    // Traces to: agent-form-requirements.md §9.3 — "Edit slide-over footer shows Last saved indicator and Delete"
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getByTestId('last-saved-indicator')).toBeInTheDocument()
    const deleteBtn = screen.getByTestId('delete-agent-button')
    expect(deleteBtn).toBeInTheDocument()
  })

  it('hides delete-agent-button for a locked core agent', async () => {
    // Traces to: agent-form-requirements.md §9.3 — locked agents are read-only
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    expect(screen.queryByTestId('delete-agent-button')).toBeNull()
  })

  it('opens an AlertDialog with the agent name when Delete is clicked', async () => {
    // Traces to: agent-form-requirements.md §9.3 — "Tapping Delete opens a confirmation modal"
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    fireEvent.click(screen.getByTestId('delete-agent-button'))
    // The dialog renders the title with the agent name. Use findByText
    // because the dialog mounts asynchronously after state update.
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent(/Delete General Assistant/i)
    expect(dialog).toHaveTextContent(/cannot be undone/i)
  })

  it('calls deleteAgent when the destructive Delete is confirmed', async () => {
    // Traces to: agent-form-requirements.md §9.3 — destructive Delete flow
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    vi.mocked(deleteAgent).mockReset().mockResolvedValue(undefined)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    fireEvent.click(screen.getByTestId('delete-agent-button'))
    // The confirm button is the destructive one inside the dialog. Find
    // it by its role+text — there is one Delete button (the confirm) and
    // one Cancel button.
    const dialog = await screen.findByRole('alertdialog')
    const confirmBtn = dialog.querySelector('button.bg-\\[var\\(--color-error\\)\\]') as HTMLElement | null
    expect(confirmBtn).toBeTruthy()
    fireEvent.click(confirmBtn!)
    await waitFor(() => expect(deleteAgent).toHaveBeenCalledWith('general-assistant'), { timeout: 3000 })
  })
})

// Wave 5 / spec §6.4 — Runtime tab renders for subagent_3p agents only.
describe('AgentProfile — Runtime tab (spec §6.4)', () => {
  it('shows CLI path / env overrides / CLI args in the Runtime tab for subagent_3p', async () => {
    // Traces to: agent-form-requirements.md §9.3 — "the Runtime tab shows CLI (locked), CLI path, Env overrides, CLI args"
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      id: 'external-worker',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    // Click the Runtime tab.
    switchTab('tab-runtime')
    // The CLI is rendered as a read-only badge.
    expect(await screen.findByTestId('profile-cli-locked')).toBeInTheDocument()
    // The CLI path / env overrides / CLI args rows are in the panel.
    expect(screen.getByTestId('profile-cli-path')).toBeInTheDocument()
    expect(screen.getByTestId('profile-env-overrides')).toBeInTheDocument()
    expect(screen.getByTestId('profile-cli-args')).toBeInTheDocument()
  })
})

// FW-4 fix: rows keyed by `${k}-${idx}` (the KEY text itself, embedded in the
// React key) remounted the row on every keystroke in the KEY field — dropping
// focus mid-word — and renaming a key to match an existing one silently
// MERGED the two rows (Object.fromEntries on a colliding key just keeps the
// last write). Rows are now keyed by a synthetic id that never changes.
describe('AgentProfile — Environment overrides editor (focus-loss / duplicate-key fix)', () => {
  it('typing multiple characters into a KEY field keeps DOM focus on the same node', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      id: 'external-worker',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code', env_overrides: { FOO: 'bar' } },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const keyInput = (await screen.findByDisplayValue('FOO')) as HTMLInputElement
    keyInput.focus()
    expect(document.activeElement).toBe(keyInput)

    for (const next of ['F', 'FO', 'FOO', 'FOOX', 'FOOXY']) {
      fireEvent.change(keyInput, { target: { value: next } })
      // The SAME DOM node must stay focused after every keystroke — the old
      // bug keyed each row by its live KEY text, so a rename remounted the
      // row (a fresh React key) and bounced focus back to <body>.
      expect(document.activeElement).toBe(keyInput)
    }
    expect(keyInput).toHaveValue('FOOXY')
  })

  it('flags a duplicate key inline on blur and does not silently merge the two rows', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      id: 'external-worker',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code', env_overrides: { FOO: 'bar', BAZ: 'qux' } },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    expect(screen.getByTestId('profile-env-row-0')).toBeInTheDocument()
    expect(screen.getByTestId('profile-env-row-1')).toBeInTheDocument()

    const bazKeyInput = screen.getByDisplayValue('BAZ') as HTMLInputElement
    fireEvent.change(bazKeyInput, { target: { value: 'FOO' } })
    fireEvent.blur(bazKeyInput)

    // Inline duplicate error shown...
    expect(await screen.findByTestId('profile-env-duplicate-1')).toHaveTextContent(/duplicate key/i)
    // ...and BOTH rows are still present — no merge into one.
    expect(screen.getByTestId('profile-env-row-0')).toBeInTheDocument()
    expect(screen.getByTestId('profile-env-row-1')).toBeInTheDocument()
    // Row 0's original FOO=bar entry is untouched by row 1's blocked rename.
    expect(screen.getByDisplayValue('bar')).toBeInTheDocument()
    expect(screen.getByDisplayValue('qux')).toBeInTheDocument()

    // The blocked rename must never reach the autosave payload — give the
    // debounce (1500ms — raised from the 500ms default; see item 9a) a beat
    // and confirm no PUT ever collapsed the pair down to a single key. This
    // assertion loop tolerates zero calls too, so it doesn't strictly need
    // to outlast the debounce to be meaningful — widened anyway for
    // consistency with the other real-timer negative windows in this file.
    await new Promise((r) => setTimeout(r, 1800))
    for (const call of vi.mocked(updateAgent).mock.calls) {
      const body = call[1] as { executor?: { env_overrides?: Record<string, string> } }
      if (body.executor?.env_overrides) {
        expect(Object.keys(body.executor.env_overrides)).toHaveLength(2)
      }
    }
  })
})

// ── 409 Conflict UI ────────────────────────────────────────────────────────────

describe('AgentProfile — 409 conflict handling', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockCoreAgent)
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockCoreAgent)
    vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  })

  it('surfaces a toast with Refresh action on 409 conflict', async () => {
    // `mockRejectedValue` (persistent), not `...Once`: a 409 leaves
    // useAutoSave's `hasPendingChanges()` true (a failed save never
    // advances `lastSavedJsonRef`), so unmounting this component (via this
    // file's global `cleanup()` in `afterEach`) fires an extra, fire-and-
    // forget flush call into the SAME shared `updateAgent` mock — see
    // useAutoSave.ts's unmount-cleanup effect. That extra call can land
    // asynchronously in a LATER test's window and, with a "...Once" queue,
    // consume ITS queued value instead of this test's own edit. Persistent
    // rejection makes this test robust to that extra call regardless of
    // how many times `updateAgent` actually fires during its run.
    vi.mocked(updateAgent).mockRejectedValue(new ApiError(409, 'conflict'))

    renderProfile('general-assistant')
    await screen.findByText('General Assistant')

    // Capture toasts via the store's subscribe mechanism
    const { useUiStore } = await import('@/store/ui')
    // Track EXISTING toast ids (not just a count) before triggering our own
    // conflict. A plain `toasts.length` snapshot is racy here: toasts
    // auto-dismiss after 4000ms (see `addToast` in store/ui.ts), and this
    // file's earlier tests can leave "changed elsewhere" toasts behind that
    // auto-dismiss WHILE this test's own debounced save (now 1500ms) is
    // still in flight — an old toast disappearing at the same moment a new
    // one appears keeps the raw count identical, so a `length > before`
    // check can miss a genuinely-added toast entirely. Identity (id) is not
    // subject to that race.
    const idsBefore = new Set(useUiStore.getState().toasts.map((t) => t.id))

    // Trigger a save by changing a field
    const nameInputs = screen.getAllByDisplayValue('General Assistant')
    fireEvent.change(nameInputs[0], { target: { value: 'Changed Name' } })

    // Wait for the debounced auto-save to fire and call updateAgent
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    // Wait for the 409 catch block to add a toast — identified by NOT being
    // in `idsBefore`, so a concurrently-auto-dismissing stale toast can
    // never mask it.
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts
      const newOnes = toasts.filter((t) => !idsBefore.has(t.id))
      expect(newOnes.some((t) => /changed elsewhere/i.test(t.message))).toBe(true)
    }, { timeout: 6000 })

    // Verify the Refresh action is attached
    const state = useUiStore.getState()
    const conflictToast = state.toasts.find((t) => !idsBefore.has(t.id) && /changed elsewhere/i.test(t.message))
    expect(conflictToast?.action?.label).toBe('Refresh')
  })

  it('Refresh after a 409 re-hydrates from the fresher server snapshot — the updated_at staleness guard must not block it (7-reviewer-gate regression)', async () => {
    // Regression for an interaction between two guards added in the same
    // fix wave: `lastIncorporatedUpdatedAtRef` (the monotonic `updated_at`
    // hydration guard, AgentProfile.tsx) and the pre-existing 409-conflict
    // "Refresh" flow. On a 409, the failed `updateAgent` call's `resp` is
    // never assigned, so `lastIncorporatedUpdatedAtRef.current` is left at
    // whatever it was BEFORE this failed save attempt (here: the initial
    // fetch's `updated_at`, t0) — never advanced to reflect the rejected
    // edit. The subsequent `refetchAgent()` (fired by clicking Refresh)
    // resolves with the "changed elsewhere" server snapshot at t1 > t0, so
    // the guard must accept it and re-hydrate the form. Verified by test,
    // not just by reading the code, per the pr-test-analyzer review finding
    // — two guards interacting in a surprising way is exactly the class of
    // bug (P-F2) this whole track exists to fix.
    const t0 = '2026-01-01T00:00:00.000Z'
    const t1 = '2026-01-01T00:05:00.000Z' // strictly newer — "changed elsewhere"

    vi.mocked(fetchAgent).mockReset()
      .mockResolvedValueOnce({ ...mockCoreAgent, updated_at: t0 })
      .mockResolvedValue({ ...mockCoreAgent, updated_at: t1, description: 'Changed elsewhere by another operator' })
    // `mockRejectedValue` (persistent), not `...Once` — see the sibling 409
    // test's comment above: a failed save leaves useAutoSave's
    // `hasPendingChanges()` true, so this component's unmount (cleanup()
    // in afterEach) fires an extra flush call into this SAME shared mock.
    // Persistent rejection keeps this test correct regardless of how many
    // times `updateAgent` actually fires.
    vi.mocked(updateAgent).mockReset().mockRejectedValue(new ApiError(409, 'conflict'))

    const { useUiStore } = await import('@/store/ui')
    // Toasts live in a module-level zustand store that persists across
    // tests in this file (the preceding 409 test in this same describe
    // block leaves its own "changed elsewhere" toast behind, un-dismissed).
    // Track EXISTING toast ids (not just a count) before triggering our own
    // conflict — see the sibling 409 test's comment above: a raw
    // `toasts.length` snapshot is racy against the 4000ms auto-dismiss
    // timer once the debounce (now 1500ms) pushes the whole edit-to-toast
    // chain far enough into real wall-clock time that an OLD leftover toast
    // can auto-dismiss at roughly the same moment this test's own toast
    // appears, keeping the raw count identical. Identity (id) sidesteps
    // that race, and also keeps the original intent: grabbing a stale toast
    // object from an earlier test's (unmounted) component would invoke a
    // `refetchAgent` closure bound to that OTHER test's query client, which
    // would never touch the component instance under test here.
    const idsBefore = new Set(useUiStore.getState().toasts.map((t) => t.id))

    renderProfile('general-assistant')
    await screen.findByText('General Assistant')

    // Trigger a save by changing a field — this is the edit that will 409.
    // Desktop Tabs and mobile Accordion both render the field (see the
    // fallback-summary tests above for the same pattern) — use the first.
    fireEvent.change(screen.getAllByTestId('agent-name-input')[0], { target: { value: 'My Rejected Local Edit' } })

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    await waitFor(() => {
      const newOnes = useUiStore.getState().toasts.filter((t) => !idsBefore.has(t.id))
      expect(newOnes.some((t) => /changed elsewhere/i.test(t.message))).toBe(true)
    }, { timeout: 6000 })

    const newToasts = useUiStore.getState().toasts.filter((t) => !idsBefore.has(t.id))
    const conflictToast = newToasts.find((t) => /changed elsewhere/i.test(t.message))
    expect(conflictToast?.action?.label).toBe('Refresh')
    conflictToast?.action?.onClick()

    // The Refresh action's refetchAgent() resolves with the fresher (t1)
    // snapshot — the hydration effect must accept it (t1 > t0), NOT reject
    // it as "not strictly newer" and leave the form stuck on the rejected
    // local edit.
    await waitFor(() => {
      expect(screen.getAllByTestId('agent-description-input')[0]).toHaveValue('Changed elsewhere by another operator')
    }, { timeout: 6000 })
    expect(screen.getAllByTestId('agent-name-input')[0]).not.toHaveValue('My Rejected Local Edit')
  })
})

// ── updated_at staleness guard: warn-on-reject + fail-CLOSED on NaN ──────────
// 7-reviewer-gate finding (3 independent reviewers): the lastIncorporatedUpdatedAtRef
// guard used to (1) reject a stale hydration with a bare early-return (no
// signal at all) and (2) FAIL OPEN (silently proceed to hydrate) whenever
// either timestamp failed to parse, since `NaN <= NaN` is `false` in JS. Both
// are backwards for a guard whose whole purpose is "never silently show
// stale/unordered data". These tests drive the guard directly through the
// component (it has no standalone export) rather than just reading the code.

describe('AgentProfile — updated_at staleness guard', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockReset()
    vi.mocked(updateAgent).mockReset()
    vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  })

  it('Wave-3 hotfix: an EQUAL incoming updated_at (already-incorporated echo) is a silent no-op — no rejected-hydration telemetry', async () => {
    // Regression for the bogus telemetry finding: the guard used to treat
    // `incoming <= incorporated` as one "reject + warn" branch, so a
    // same-timestamp echo (the normal, expected shape of an
    // `invalidateQueries` background refetch landing right after a save
    // already applied that exact snapshot via `setQueryData`) fired
    // `agentProfileUpdatedAtGuardRejectedHydration` on every profile open
    // AND after every successful autosave — a false positive with no real
    // conflict. Equal must still SKIP re-hydrating (nothing changed) but
    // must NOT log anything.
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const t0 = '2026-02-01T00:00:00.000Z'

    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, updated_at: t0 })
    // A save that SUCCEEDS but whose response — and the STATIC fetchAgent
    // mock used by the subsequent invalidateQueries background refetch —
    // both carry the SAME (not strictly newer) updated_at as what's already
    // incorporated. Mirrors the "fallback_models passive-repro round 2"
    // scenario this guard was built for.
    vi.mocked(updateAgent).mockResolvedValue({ ...mockCoreAgent, name: 'Edited Name', updated_at: t0 })

    renderProfile('general-assistant')
    await screen.findByText('General Assistant')

    fireEvent.change(screen.getAllByTestId('agent-name-input')[0], { target: { value: 'Edited Name' } })

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    // Give the save-success `setQueryData` patch and the `invalidateQueries`
    // background refetch (both landing at the same t0) a moment to run
    // through the hydration effect, then assert the rejected-hydration
    // telemetry event never fired.
    await waitFor(() => {
      expect(screen.getAllByTestId('agent-name-input')[0]).toHaveValue('Edited Name')
    }, { timeout: 6000 })
    expect(warnSpy).not.toHaveBeenCalledWith(
      'agentProfile.updatedAtGuardRejectedHydration',
      expect.anything(),
    )
  })

  it('a genuinely STALE incoming updated_at (strictly older than incorporated) still warns via console.warn + logDiagnostic', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const tOld = '2026-02-01T00:00:00.000Z'
    const tNew = '2026-02-01T00:05:00.000Z' // strictly newer

    // Every fetchAgent call — the initial load AND the invalidateQueries
    // background refetch fired after the save below — resolves with the
    // OLDER timestamp, simulating a stale/lagging refetch race (network
    // reordering, read-replica lag). The save's own PUT response carries
    // the NEWER timestamp, so `lastIncorporatedUpdatedAtRef.current` is
    // advanced to tNew directly by the save-success handler before the
    // stale background refetch's snapshot (tOld) lands.
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, updated_at: tOld })
    vi.mocked(updateAgent).mockResolvedValue({ ...mockCoreAgent, name: 'Edited Name', updated_at: tNew })

    renderProfile('general-assistant')
    await screen.findByText('General Assistant')

    fireEvent.change(screen.getAllByTestId('agent-name-input')[0], { target: { value: 'Edited Name' } })

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    // The invalidateQueries background refetch resolves with the STALE
    // (tOld < tNew) snapshot — a genuine conflict, still rejected with a
    // telemetry breadcrumb.
    await waitFor(() => {
      expect(warnSpy).toHaveBeenCalledWith(
        'agentProfile.updatedAtGuardRejectedHydration',
        expect.objectContaining({ agentId: 'general-assistant', incoming: tOld, incorporated: tNew }),
      )
    }, { timeout: 6000 })
  })

  // The `<` (genuinely stale) case remains load-bearing and must stay
  // noisy — covered by "AgentProfile — guard-noise fix: stale vs identical
  // updated_at" below (the sibling describe block introduced alongside
  // this fix), which drives it directly via a synthetic stale
  // `setQueryData` poke rather than a real save + refetch race.

  it('fails CLOSED (rejects the hydration, does not silently adopt it) and warns when updated_at cannot be parsed', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const t0 = '2026-02-01T00:00:00.000Z'
    const t1 = '2026-02-01T00:05:00.000Z' // strictly newer, and validly parseable

    vi.mocked(fetchAgent).mockReset()
      .mockResolvedValueOnce({ ...mockCoreAgent, updated_at: t0, description: 'Original description' })
      // The background refetch fired by invalidateQueries after the save
      // below — a fresh, VALIDLY-parseable, strictly newer snapshot. Must
      // still be REJECTED because the INCORPORATED side (set from the
      // save's own malformed response, below) cannot be parsed, so the
      // guard cannot confidently order the two and fails closed.
      .mockResolvedValue({ ...mockCoreAgent, updated_at: t1, description: 'Fresher description from elsewhere' })
    // The save response's `updated_at` is malformed — this is what gets
    // written into `lastIncorporatedUpdatedAtRef.current` directly by the
    // save-success handler (AgentProfile.tsx, `if (resp.updated_at) …`).
    vi.mocked(updateAgent).mockResolvedValue({
      ...mockCoreAgent,
      name: 'Edited Name',
      description: 'Original description',
      updated_at: 'not-a-real-timestamp',
    })

    renderProfile('general-assistant')
    await screen.findByText('General Assistant')

    fireEvent.change(screen.getAllByTestId('agent-name-input')[0], { target: { value: 'Edited Name' } })

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    // The background refetch (fetchAgent's 2nd+ resolution, t1) must be
    // rejected — the "unparseable" branch, not silently adopted.
    await waitFor(() => {
      expect(warnSpy).toHaveBeenCalledWith(
        'agentProfile.updatedAtGuardUnparseable',
        expect.objectContaining({ agentId: 'general-assistant' }),
      )
    }, { timeout: 6000 })

    // The fresher-but-unorderable snapshot's description must NOT have
    // silently replaced what's in the form — proves fail-CLOSED, not
    // fail-OPEN. (The form still shows the user's own locally-known
    // description, never touched by this edit.)
    expect(screen.getAllByTestId('agent-description-input')[0]).not.toHaveValue('Fresher description from elsewhere')
    expect(screen.getAllByTestId('agent-description-input')[0]).toHaveValue('Original description')
  })
})

// ── SOUL echo-race regression (P2 'Text input Auto save') ──────────────────
//
// Root cause: the hydration effect (guarded by isDirtyRef + the updated_at
// monotonic check) is sound, but the OLD saveFn cleared `isDirtyRef.current
// = false` UNCONDITIONALLY on every successful save — BEFORE its own
// (un-awaited) `invalidateQueries` refetch landed. If the operator typed
// into SOUL again while the PUT was still in flight, that unconditional
// clear let a LATER refetch — one that still carries the OLD (pre-second-
// edit) soul text but a genuinely newer `updated_at` (so the separate
// monotonic guard alone does not reject it) — revert the newer keystrokes.
// Fixed via useAutoSave's `onSaved(saved, isCurrent)`: dirty now clears
// only when the save snapshot still equals the live draft.
describe('AgentProfile — SOUL echo-race (autosave hydration must never revert an in-flight draft)', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockReset()
    vi.mocked(updateAgent).mockReset()
    vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  })

  it('typing into SOUL during the save round-trip is never reverted by the invalidate-refetch echo (stale text + newer updated_at), and the newer text still persists', async () => {
    const t0 = '2026-04-01T00:00:00.000Z'
    const t1 = '2026-04-01T00:00:05.000Z' // save #1's own PUT response
    const t2 = '2026-04-01T00:00:10.000Z' // save #1's invalidate-refetch echo: STALE soul, but a strictly NEWER updated_at (passes the monotonic guard on its own — isDirtyRef must be what stops it)
    const t3 = '2026-04-01T00:00:15.000Z' // save #2's own PUT response
    const t4 = '2026-04-01T00:00:20.000Z' // save #2's invalidate-refetch: fresh, matching

    vi.mocked(fetchAgent)
      .mockResolvedValueOnce({ ...mockCoreAgent, soul: '', updated_at: t0 })
      .mockResolvedValueOnce({ ...mockCoreAgent, soul: 'First soul text', updated_at: t2 })
      .mockResolvedValue({ ...mockCoreAgent, soul: 'First soul text Second soul text', updated_at: t4 })

    // Both PUTs are manually controlled so the test can inspect the exact
    // moment save #1's stale echo lands WHILE save #2 is still queued/in
    // flight — the precise race window this fix closes.
    let resolvePut1!: (v: Agent) => void
    const putPromise1 = new Promise<Agent>((r) => { resolvePut1 = r })
    let resolvePut2!: (v: Agent) => void
    const putPromise2 = new Promise<Agent>((r) => { resolvePut2 = r })
    vi.mocked(updateAgent)
      .mockReturnValueOnce(putPromise1)
      .mockReturnValueOnce(putPromise2)

    const queryClient = makeClient()
    renderProfile('general-assistant', queryClient)
    await screen.findByText('General Assistant')

    switchTab('tab-personality')
    const soulTextarea = await screen.findByTestId('agent-soul')

    vi.useFakeTimers()

    // First edit — debounce fires (1500ms, raised from the 500ms default —
    // see AgentProfile.tsx's main useAutoSave call), PUT #1 goes out and
    // stays in flight.
    fireEvent.change(soulTextarea, { target: { value: 'First soul text' } })
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(updateAgent).toHaveBeenCalledTimes(1)

    // Second edit — the operator keeps typing while PUT #1 is still
    // unresolved. This debounce cycle fires too, but useAutoSave's own
    // serialization (isSavingRef) queues it (rerunPendingRef) instead of
    // dispatching a second, concurrent PUT.
    fireEvent.change(soulTextarea, { target: { value: 'First soul text Second soul text' } })
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(updateAgent).toHaveBeenCalledTimes(1)
    expect(soulTextarea).toHaveValue('First soul text Second soul text')

    vi.useRealTimers()

    // Resolve PUT #1. Its own `setQueryData(['agent', agentId], resp)`
    // synchronously seeds the cache with (soul: 'First soul text',
    // updated_at: t1); the subsequent (un-awaited) `invalidateQueries`
    // refetch (mocked above) then resolves with the STALE soul but a
    // strictly NEWER updated_at (t2) — passing the updated_at monotonic
    // guard on its own. Save #2 (the queued rerun) starts right behind it
    // and immediately blocks on the still-unresolved `putPromise2`, giving
    // a clean checkpoint to inspect the draft mid-race.
    await act(async () => {
      resolvePut1({ ...mockCoreAgent, soul: 'First soul text', updated_at: t1 })
    })

    await waitFor(() => {
      expect(fetchAgent).toHaveBeenCalledTimes(2)
    })
    // Save #2 must already be in flight (queued via useAutoSave's own
    // serialization) by the time save #1's echo has landed.
    await waitFor(() => {
      expect(updateAgent).toHaveBeenCalledTimes(2)
    })

    // Reviewer F3 hardening (item 9b): `fetchAgent` being CALLED twice only
    // proves the queryFn was invoked, not that its resolved value has
    // actually landed in the query cache yet. Poll the cache itself for the
    // stale echo's `updated_at` (t2) before the load-bearing assertion
    // below, so this test can't pass on a lucky timing coincidence where
    // the mid-race snapshot hadn't actually landed.
    await waitFor(() => {
      const cached = queryClient.getQueryData<Agent>(['agent', 'general-assistant'])
      expect(cached?.updated_at).toBe(t2)
    })

    // THE regression assertion: even though the (updated_at-guard-passing)
    // refetch echo just landed, the textarea must still show the NEWEST
    // typed text — isDirtyRef stayed armed because useAutoSave's onSaved
    // reported isCurrent=false for save #1 (the live draft had already
    // moved on when it resolved).
    expect(soulTextarea).toHaveValue('First soul text Second soul text')

    // Resolve PUT #2 — the queued re-save persists the newer text, and its
    // own invalidate-refetch (mocked to return the now-genuinely-current
    // value) settles cleanly.
    await act(async () => {
      resolvePut2({ ...mockCoreAgent, soul: 'First soul text Second soul text', updated_at: t3 })
    })

    await waitFor(() => {
      expect(fetchAgent).toHaveBeenCalledTimes(3)
    })
    expect(updateAgent).toHaveBeenNthCalledWith(
      1, 'general-assistant', expect.objectContaining({ soul: 'First soul text' }),
    )
    expect(updateAgent).toHaveBeenNthCalledWith(
      2, 'general-assistant', expect.objectContaining({ soul: 'First soul text Second soul text' }),
    )
    expect(soulTextarea).toHaveValue('First soul text Second soul text')
  })
})

// ── Sheet-close flush regression (item 1: MERGE-BLOCKER sheet-close data loss) ──
//
// Root cause: AgentProfile is mounted at the route level and does NOT
// unmount when the sheet closes (only the store's `editAgentId` flips to
// null) — so useAutoSave's own unmount-flush never fires on close. A
// debounce timer armed less than 1500ms before close instead fires AFTER
// close, lands in saveFn's `agentId === null` early-return, and useAutoSave
// records that as a SUCCESS — the edit is silently lost even though nothing
// was ever sent to the server. Fixed by flushing explicitly in the sheet's
// onClose handler (`handleCloseAgentSheet`), before the store write that
// clears `editAgentId`.
//
// This suite renders <AgentProfile /> with NO `agentId` prop (unlike every
// other test in this file) so `agentId` is driven purely by the store's
// `editAgentId`, exactly like the real app's route-level mount — a
// prop-driven render can never observe the close transition, since the
// prop always wins over the store value.
describe('AgentProfile — sheet-close flush (item 1)', () => {
  beforeEach(() => {
    useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null })
  })

  it('typing into SOUL then closing the sheet within the debounce window still persists the typed text', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, soul: '' })
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockCoreAgent)

    useUiStore.setState({ editAgentId: 'general-assistant' })
    render(
      <QueryClientProvider client={makeClient()}>
        <AgentProfile />
      </QueryClientProvider>,
    )

    await screen.findByText('General Assistant')
    switchTab('tab-personality')
    const soulTextarea = await screen.findByTestId('agent-soul')

    vi.useFakeTimers()
    fireEvent.change(soulTextarea, { target: { value: 'Typed just before close' } })

    // Advance WELL SHORT of the 1500ms debounce — the timer is still armed,
    // unfired, when we close.
    await act(async () => { vi.advanceTimersByTime(200) })
    expect(updateAgent).not.toHaveBeenCalled()

    // Close via the sheet's own dismiss affordance (the Radix `X` button —
    // the same `onOpenChange(false)` path Escape / backdrop-click also
    // drive), exercising `handleCloseAgentSheet` for real rather than
    // calling the store action directly.
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    // The close handler's flush calls saveNow() synchronously, which starts
    // doSave() up to its first await (the mocked, already-resolved
    // updateAgent promise) — but doSave()'s OWN post-await bookkeeping
    // (status→'saved', and the 2s "fade to idle" setTimeout it arms) is not
    // gated by any timer here, so a plain sync `advanceTimersByTime` has
    // nothing to advance. Flush the pending microtasks while STILL on fake
    // timers (advanceTimersByTimeAsync(0) is this codebase's established
    // idiom for exactly this — see browserWebRTC.test.ts) so that
    // continuation — including the fade-timer arm — settles as a FAKE timer
    // before switching to real ones below. Without this, the arm can race
    // past the vi.useRealTimers() switch on unlucky microtask orderings,
    // leaking a genuine setTimeout that fires ~2s later during a LATER test
    // and throws on this already-unmounted component. See useAutoSave.ts's
    // fade-timer comment.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    vi.useRealTimers()

    await waitFor(() => {
      expect(updateAgent).toHaveBeenCalledWith(
        'general-assistant',
        expect.objectContaining({ soul: 'Typed just before close' }),
      )
    })

    // The sheet actually closed (store cleared) — confirms this exercised
    // the real close path, not a debounce coincidence.
    expect(useUiStore.getState().editAgentId).toBeNull()
  })
})

// ── Finding A (7-reviewer-gate, fix/uat-v0.1.1-defects) — reachability probe ──
//
// `handleCloseAgentSheet` only flushes a pending edit when the sheet closes
// via ITS OWN `onClose` (the Sheet's dismiss affordance / Escape / backdrop
// click). The deep-link route (`src/routes/_app/agents.$agentId.tsx`) does
// NOT go through that handler — it calls `useUiStore.getState().
// openEditAgentSlideOver(newAgentId)` directly, a raw Zustand setter with no
// flush logic of its own (`src/store/ui.ts`). Since AgentProfile is mounted
// once at the layout level and does NOT unmount between agents, switching
// `editAgentId` this way while a DIFFERENT agent's sheet is open with a
// genuinely pending (debounced, not yet sent) edit bypasses the flush
// entirely — this test proves that reachability empirically by simulating
// exactly what the deep-link route does (calling the store action directly)
// rather than clicking the sheet's own close affordance.
describe('AgentProfile — Finding A: store-driven agent switch bypasses the close-flush', () => {
  beforeEach(() => {
    useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null })
  })

  it('REACHABILITY PROBE: switching editAgentId directly (as the deep-link route does) while a SOUL edit is still debounced loses the edit — never sent to the server, and gone from the UI on revisit', async () => {
    vi.mocked(fetchAgent).mockImplementation((id: string) =>
      Promise.resolve(id === 'mia' ? mockLockedCoreAgent : { ...mockCoreAgent, soul: '' }),
    )
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockCoreAgent)

    useUiStore.setState({ editAgentId: 'general-assistant' })
    render(
      <QueryClientProvider client={makeClient()}>
        <AgentProfile />
      </QueryClientProvider>,
    )

    await screen.findByText('General Assistant')
    switchTab('tab-personality')
    const soulTextarea = await screen.findByTestId('agent-soul')

    vi.useFakeTimers()
    fireEvent.change(soulTextarea, { target: { value: 'Typed just before switch' } })

    // Advance WELL SHORT of the 1500ms debounce — the timer is still armed,
    // unfired.
    await act(async () => { vi.advanceTimersByTime(200) })
    expect(updateAgent).not.toHaveBeenCalled()

    // Simulate the deep-link route's bypass: call the RAW store action
    // directly instead of the sheet's own close affordance. This is exactly
    // what `AgentProfileRoute` (agents.$agentId.tsx) does on mount — no
    // `handleCloseAgentSheet`, no flush.
    await act(async () => {
      useUiStore.getState().openEditAgentSlideOver('mia')
      // Let the debounce timer that was already armed for general-assistant
      // run its full course — the route-bypass fix (if any) needs to survive
      // this, not merely "hasn't fired yet".
      await vi.advanceTimersByTimeAsync(2000)
    })
    vi.useRealTimers()

    await screen.findByText('Mia')

    // The pending SOUL edit for general-assistant must never have reached
    // the server — this assertion is expected to FAIL on unfixed code only
    // if a naive "flush on switch" were added with a stale closure; on
    // TODAY's code (no flush at all on this path) it currently passes
    // vacuously (updateAgent is never called for ANY reason), which is the
    // silent-data-loss symptom itself. The revisit assertion below is what
    // actually distinguishes "lost silently" from "handled correctly".
    for (const call of vi.mocked(updateAgent).mock.calls) {
      expect(call[1]).not.toMatchObject({ soul: 'Typed just before switch' })
    }

    // Revisit general-assistant. If the edit had been durably captured
    // anywhere (server or otherwise), it would reappear. It does not — the
    // form re-hydrates to the server's original (untouched) soul, proving
    // the edit is gone, not merely "not yet sent".
    await act(async () => {
      useUiStore.getState().openEditAgentSlideOver('general-assistant')
    })
    await screen.findByText('General Assistant')
    switchTab('tab-personality')
    const revisitedSoulTextarea = await screen.findByTestId('agent-soul')
    expect(revisitedSoulTextarea).not.toHaveValue('Typed just before switch')
    expect(revisitedSoulTextarea).toHaveValue('')
  })

  // REVERT-PROOF: this is the actual fix under test (not just the
  // reachability probe above). Fails on code with no Finding-A guard at
  // all (no toast is ever raised — the loss stays silent); passes once the
  // `lostPendingAgentEditRef` + no-deps effect in AgentProfile.tsx surfaces
  // it. See the reachability probe above for why an automatic re-flush was
  // deliberately NOT chosen.
  it('surfaces a toast + diagnostic when a store-driven switch discards a pending edit (the fix under test)', async () => {
    vi.mocked(fetchAgent).mockImplementation((id: string) =>
      Promise.resolve(id === 'mia' ? mockLockedCoreAgent : { ...mockCoreAgent, soul: '' }),
    )
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockCoreAgent)
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    useUiStore.setState({ editAgentId: 'general-assistant' })
    render(
      <QueryClientProvider client={makeClient()}>
        <AgentProfile />
      </QueryClientProvider>,
    )

    await screen.findByText('General Assistant')
    switchTab('tab-personality')
    const soulTextarea = await screen.findByTestId('agent-soul')

    const idsBefore = new Set(useUiStore.getState().toasts.map((t) => t.id))

    vi.useFakeTimers()
    fireEvent.change(soulTextarea, { target: { value: 'Typed just before switch' } })
    await act(async () => { vi.advanceTimersByTime(200) })

    await act(async () => {
      useUiStore.getState().openEditAgentSlideOver('mia')
      await vi.advanceTimersByTimeAsync(0)
    })
    vi.useRealTimers()

    await screen.findByText('Mia')

    await waitFor(() => {
      const newOnes = useUiStore.getState().toasts.filter((t) => !idsBefore.has(t.id))
      expect(newOnes.some((t) => /not saved before you switched/i.test(t.message))).toBe(true)
    })
    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('unsaved pending edit'),
      expect.objectContaining({ agentId: 'general-assistant' }),
    )

    warnSpy.mockRestore()
  })

  // Guards against the false-positive this fix could easily introduce: an
  // agent that is STILL LOADING (never hydrated — `disabled` was true the
  // entire time) has no real baseline yet, so `hasPendingChanges()` would
  // trivially read `true` against the hardcoded-default form state. Without
  // the `formHydrated` guard, switching away from a still-loading agent
  // would falsely report a "lost edit" that never existed.
  it('does NOT warn when switching away from an agent that never finished loading (no real edit could have existed)', async () => {
    let resolveFetch: ((agent: Agent) => void) | undefined
    vi.mocked(fetchAgent).mockImplementation((id: string) => {
      if (id === 'general-assistant') {
        return new Promise((resolve) => { resolveFetch = resolve })
      }
      return Promise.resolve(mockLockedCoreAgent)
    })
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    useUiStore.setState({ editAgentId: 'general-assistant' })
    render(
      <QueryClientProvider client={makeClient()}>
        <AgentProfile />
      </QueryClientProvider>,
    )

    // Still loading — never hydrated.
    expect(screen.getByText(/loading agent/i)).toBeInTheDocument()

    const idsBefore = new Set(useUiStore.getState().toasts.map((t) => t.id))

    await act(async () => {
      useUiStore.getState().openEditAgentSlideOver('mia')
    })
    await screen.findByText('Mia')

    // Let the never-resolved general-assistant fetch's promise linger
    // harmlessly (this test doesn't need it to resolve).
    void resolveFetch

    await new Promise((r) => setTimeout(r, 50))
    const newOnes = useUiStore.getState().toasts.filter((t) => !idsBefore.has(t.id))
    expect(newOnes.some((t) => /not saved before you switched/i.test(t.message))).toBe(false)
    expect(warnSpy).not.toHaveBeenCalledWith(
      expect.stringContaining('unsaved pending edit'),
      expect.anything(),
    )
    warnSpy.mockRestore()
  })
})

// ── subagent_3p payload restriction ───────────────────────────────────────────

describe('AgentProfile — subagent_3p payload restriction', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue({
      ...mockWorkerAgent,
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    vi.mocked(updateAgent).mockReset().mockResolvedValue({
      ...mockWorkerAgent,
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  })

  it('omits forbidden fields from the PUT payload for subagent_3p', async () => {
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')

    // Change a field to trigger auto-save
    const nameInputs = screen.getAllByDisplayValue('Web Researcher')
    fireEvent.change(nameInputs[0], { target: { value: 'Renamed Worker' } })

    // Wait for the debounced auto-save to fire and call updateAgent
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    const callArgs = vi.mocked(updateAgent).mock.calls[0]
    const payload = callArgs[1] as Record<string, unknown>

    // Forbidden fields must NOT be present
    expect(payload).not.toHaveProperty('tools_cfg')
    expect(payload).not.toHaveProperty('skills')
    expect(payload).not.toHaveProperty('shell_policy')
    expect(payload).not.toHaveProperty('fallback_models')
    expect(payload).not.toHaveProperty('model_params')
    // agent-types-field-matrix.md, Decisions #1 (resolved 2026-07-03):
    // excluded — max_tool_iterations is excluded for subagent_3p.
    expect(payload).not.toHaveProperty('max_tool_iterations')

    // Allowed fields SHOULD be present
    expect(payload).toHaveProperty('name')
    expect(payload).toHaveProperty('executor')
    expect(payload).toHaveProperty('updated_at')
    // timeout_seconds STAYS for subagent_3p (operator decision: keep).
  })
})

// ── FR-016 / US-5: Heartbeat tab — conditional on workspace context ───────────
//
// TDD plan tests T16 (AgentProfile.heartbeatTab.test.tsx — wired here for co-
// location with the existing AgentProfile suite):
//   - Tab present with workspaceId context, absent without it
//   - Workers get no tab even with workspace context (FR-025)
//   - Personality tab has no heartbeat fields (FR-017)
//   - Save goes to the workspace mutation, NOT agent autosave (A2/F-09)

const mockWorkspace: Workspace = {
  id: 'ws-1',
  name: 'Test Workspace',
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  core_team: ['general-assistant'],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  member_configs: {
    'general-assistant': {
      heartbeat: {
        enabled: true,
        interval_minutes: 30,
        body: 'Summarise overnight CI results.',
      },
    },
  },
}

/**
 * Render AgentProfile with a workspace context set in the UI store.
 * This simulates the flow of opening the slide-over FROM a workspace Team tab
 * (which calls openEditAgentSlideOver(agentId, workspaceId)).
 */
function renderProfileWithWorkspace(agentId: string, workspaceId: string) {
  // Set the store BEFORE rendering so the component reads the workspaceId.
  useUiStore.setState({
    editAgentId: agentId,
    editAgentWorkspaceId: workspaceId,
  })
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentProfile agentId={agentId} />
    </QueryClientProvider>
  )
}

describe('AgentProfile — Heartbeat tab (FR-016 / US-5)', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    vi.mocked(fetchSkills).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue(mockWorkspace)
    // Clear call history so tests don't bleed call-count into each other.
    vi.mocked(updateWorkspace).mockReset().mockResolvedValue(mockWorkspace)
    vi.mocked(updateAgent).mockClear()
    // Reset store between tests so workspace context doesn't bleed.
    useUiStore.setState({ editAgentWorkspaceId: null })
  })

  it('shows Heartbeat tab when opened from a workspace Team tab (US-5.AC1)', async () => {
    // Traces to: US-5.AC1 — heartbeat tab present in workspace context
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    expect(screen.getByTestId('tab-heartbeat')).toBeInTheDocument()
  })

  it('does NOT show Heartbeat tab when opened globally (US-5.AC2)', async () => {
    // Traces to: US-5.AC2 — no heartbeat tab on global Agents screen
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.queryByTestId('tab-heartbeat')).toBeNull()
  })

  it('does NOT show Heartbeat tab for worker agents even with workspace context (FR-025 / E9)', async () => {
    // Traces to: FR-025 — workers have no heartbeat concept
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfileWithWorkspace('web-researcher', 'ws-1')
    await screen.findByText('Web Researcher')
    expect(screen.queryByTestId('tab-heartbeat')).toBeNull()
  })

  it('Heartbeat tab renders enabled toggle, interval, and body from workspace member_configs (US-5.AC1)', async () => {
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    // Open the Heartbeat tab
    switchTab('tab-heartbeat')
    // The enabled switch should be checked (mocked workspace has enabled:true)
    const enabledSwitch = await screen.findByTestId('heartbeat-enabled-switch')
    expect(enabledSwitch).toBeInTheDocument()
    // Body should be hydrated from member_configs
    const bodyTextarea = await screen.findByTestId('heartbeat-body-textarea')
    expect((bodyTextarea as HTMLTextAreaElement).value).toBe('Summarise overnight CI results.')
    // Interval should be hydrated
    const intervalInput = await screen.findByTestId('heartbeat-interval-input')
    expect((intervalInput as HTMLInputElement).value).toBe('30')
  })

  it('Heartbeat tab save calls updateWorkspace, NOT updateAgent (A2/F-09)', async () => {
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    switchTab('tab-heartbeat')
    // Wait for the heartbeat panel to hydrate
    const saveButton = await screen.findByTestId('heartbeat-save-button')
    // Click the explicit Save button on the Heartbeat tab
    fireEvent.click(saveButton)
    await waitFor(() => expect(updateWorkspace).toHaveBeenCalledWith('ws-1', expect.objectContaining({
      member_configs: expect.objectContaining({
        'general-assistant': expect.objectContaining({
          heartbeat: expect.objectContaining({ enabled: true }),
        }),
      }),
    })))
    // The agent autosave must NOT have fired as a result of this action.
    expect(updateAgent).not.toHaveBeenCalled()
  })

  it('Personality tab has no heartbeat fields (FR-017 / US-5.AC4)', async () => {
    // Traces to: FR-017 — heartbeat is workspace-scoped now, removed from Personality
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-personality')
    // Wait for tab content to appear
    await new Promise((r) => setTimeout(r, 50))
    // The old heartbeat fields must not be present in the Personality tab
    expect(screen.queryByTestId('wizard-heartbeat')).toBeNull()
    expect(screen.queryByText(/enable periodic heartbeat/i)).toBeNull()
    expect(screen.queryByText(/heartbeat body/i)).toBeNull()
  })

  it('shows validation error when enabled=true with empty body (FR-005b)', async () => {
    // Traces to: FR-005b — body required when enabled.
    // Use a workspace with enabled=true + empty body so the form hydrates
    // directly into the invalid state without needing to interact with the switch.
    const workspaceEnabledNoBody: Workspace = {
      ...mockWorkspace,
      member_configs: {
        'general-assistant': {
          heartbeat: { enabled: true, interval_minutes: 30, body: '' },
        },
      },
    }
    vi.mocked(fetchWorkspace).mockResolvedValue(workspaceEnabledNoBody)
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    switchTab('tab-heartbeat')
    // Wait for the heartbeat panel to hydrate — body must be '' from the mock.
    const saveButton = await screen.findByTestId('heartbeat-save-button')
    await waitFor(() => {
      const bodyTextarea = screen.getByTestId('heartbeat-body-textarea')
      expect((bodyTextarea as HTMLTextAreaElement).value).toBe('')
    })
    // Click save with enabled=true + empty body — validation must block the mutation.
    fireEvent.click(saveButton)
    await new Promise((r) => setTimeout(r, 100))
    // The workspace mutation must NOT have been called (body required when enabled).
    expect(updateWorkspace).not.toHaveBeenCalled()
  })

  // fix #5 (TEST): body-required inline hint appears when enabled + empty body
  it('shows heartbeat-body-required-hint when enabled and body is empty (FR-005b inline hint)', async () => {
    // Traces to: FR-005b — inline hint renders for enabled + empty body
    const workspaceEnabledNoBody: Workspace = {
      ...mockWorkspace,
      member_configs: {
        'general-assistant': {
          heartbeat: { enabled: true, interval_minutes: 30, body: '' },
        },
      },
    }
    vi.mocked(fetchWorkspace).mockResolvedValue(workspaceEnabledNoBody)
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    switchTab('tab-heartbeat')
    // Wait for the heartbeat panel to hydrate with enabled=true + body=''
    await waitFor(() => {
      const bodyTextarea = screen.getByTestId('heartbeat-body-textarea')
      expect((bodyTextarea as HTMLTextAreaElement).value).toBe('')
    })
    // The inline hint must be visible because enabled=true and body is empty
    expect(await screen.findByTestId('heartbeat-body-required-hint')).toBeInTheDocument()
  })

  // fix #5 (TEST): I-3 invalidation — saving heartbeat invalidates ['sessions']
  it('I-3: invalidates sessions cache after a successful heartbeat save', async () => {
    const client = makeClient()
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries')

    useUiStore.setState({ editAgentId: 'general-assistant', editAgentWorkspaceId: 'ws-1' })
    render(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="general-assistant" />
      </QueryClientProvider>,
    )

    await screen.findByText('General Assistant')
    switchTab('tab-heartbeat')

    const saveButton = await screen.findByTestId('heartbeat-save-button')
    fireEvent.click(saveButton)

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: ['sessions'] }),
      )
    }, { timeout: 3000 })
  })

  // fix #5 (TEST): DATA-LOSS guard — Save disabled when workspace query errors
  it('disables the Save button when the workspace query fails to load', async () => {
    vi.mocked(fetchWorkspace).mockRejectedValue(new Error('Network error'))
    renderProfileWithWorkspace('general-assistant', 'ws-1')
    await screen.findByText('General Assistant')
    switchTab('tab-heartbeat')

    // Wait for the workspace query to settle into error state
    await waitFor(() => {
      expect(screen.getByTestId('heartbeat-workspace-error')).toBeInTheDocument()
    }, { timeout: 3000 })

    const saveButton = screen.getByTestId('heartbeat-save-button')
    expect((saveButton as HTMLButtonElement).disabled).toBe(true)
  })
})

describe('AgentProfile — max tool calls per turn (zero-clobber P0 fix)', () => {
  // The input auto-saves; backing it directly with Number(e.target.value)
  // meant clearing the field committed Number('') === 0 mid-keystroke and
  // persisted it (live install ended up with five zeroed agents + a zeroed
  // global default). The draft pattern must never autosave an empty/invalid
  // value, and blur restores the last committed number.
  it('clearing the field to type never persists 0; a valid value persists', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, max_tool_iterations: 200 })
    renderProfile(mockCoreAgent.id)
    await screen.findByText(mockCoreAgent.name)
    if (!screen.queryByTestId('agent-max-tool-calls-input')) {
      switchTab('tab-advanced')
    }
    const input = (await screen.findByTestId('agent-max-tool-calls-input')) as HTMLInputElement
    expect(input.value).toBe('200')

    vi.mocked(updateAgent).mockClear()

    // Clear the field (the first thing a user does before typing a new value).
    fireEvent.change(input, { target: { value: '' } })
    expect(input.value).toBe('')

    // Type the new value.
    fireEvent.change(input, { target: { value: '350' } })

    await waitFor(
      () => {
        expect(updateAgent).toHaveBeenCalled()
      },
      { timeout: 6000 },
    )
    // NO call may ever carry 0 — and the final persisted value is 350.
    for (const call of vi.mocked(updateAgent).mock.calls) {
      expect(call[1].max_tool_iterations).not.toBe(0)
    }
    const last = vi.mocked(updateAgent).mock.calls.at(-1)!
    expect(last[1].max_tool_iterations).toBe(350)
  })

  it('blur with an empty draft restores the last committed value', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, max_tool_iterations: 200 })
    renderProfile(mockCoreAgent.id)
    await screen.findByText(mockCoreAgent.name)
    if (!screen.queryByTestId('agent-max-tool-calls-input')) {
      switchTab('tab-advanced')
    }
    const input = (await screen.findByTestId('agent-max-tool-calls-input')) as HTMLInputElement

    fireEvent.change(input, { target: { value: '' } })
    fireEvent.blur(input)
    expect(input.value).toBe('200')
  })

  it('the help copy states the per-turn semantics and the 200 default', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, max_tool_iterations: 200 })
    renderProfile(mockCoreAgent.id)
    await screen.findByText(mockCoreAgent.name)
    if (!screen.queryByTestId('agent-max-tool-calls-input')) {
      switchTab('tab-advanced')
    }
    await screen.findByTestId('agent-max-tool-calls-input')
    expect(screen.getByText(/Max tool calls per turn/i)).toBeInTheDocument()
    expect(screen.getByText(/Per single turn/i)).toBeInTheDocument()
    expect(screen.getByText(/Default: 200/i)).toBeInTheDocument()
  })
})

describe('AgentProfile — skills visibility by agent kind (field matrix, W2c)', () => {
  it('a subagent_3p (truly external, type-based) agent gets NO Skills section', async () => {
    // External CLI runners (claude-code / codex / opencode) can never load
    // Omnipus skills — offering the mapping was a lie. The gate is now
    // TYPE-based (isExternal from agentKindFlags), not executor-based —
    // only a genuine `type: 'subagent_3p'` agent hides this section.
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    // The whole Tools tab is omitted for subagent_3p, so the Skills section
    // has no surface to render on at all.
    await waitFor(() => {
      expect(screen.queryByTestId('tab-tools')).toBeNull()
      expect(screen.queryByText(/^Skills$/i)).toBeNull()
    })
  })

  it('a native Subagent (type: Subagent) DOES get a Skills section (matrix: optional/inherit)', async () => {
    // The old (!isWorkerAgent) gate over-corrected and hid Skills for every
    // worker, including native Subagents that may legitimately be granted
    // skills. mockWorkerAgent is `type: 'Subagent'` — always native per
    // agentKindFlags, regardless of its (legacy-shorthand) executor value.
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    switchTab('tab-skills')
    // "Skills" is also the literal tab/accordion trigger label now (item 2
    // reorg) — scope to the active tabpanel so this matches only the
    // section heading, not the triggers.
    const panel = await screen.findByRole('tabpanel')
    expect(within(panel).getByText(/^Skills$/i)).toBeInTheDocument()
  })
})

// W2c — Tools & Permissions section visibility by agent kind (field matrix).
// subagent_3p hides it entirely (the external runner has its own tools; the
// old read-only-collapse path for zero-override native workers is retired —
// a fresh native Subagent now gets the LIVE editor, not a summary box).
describe('AgentProfile — Tools & Permissions visibility by agent kind (field matrix, W2c)', () => {
  it('hides Tools & Permissions for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    // Stronger than a hidden section: the Tools tab itself is omitted.
    await waitFor(() => {
      expect(screen.queryByTestId('tab-tools')).toBeNull()
      expect(screen.queryByText(/Tools.*Permissions/i)).toBeNull()
    })
  })

  it('shows the LIVE Tools & Permissions editor for a native Subagent (no read-only collapse)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    switchTab('tab-tools')
    expect(await screen.findByText(/Tools.*Permissions/i)).toBeInTheDocument()
    // The retired read-only-collapse summary must never render.
    expect(screen.queryByTestId('native-worker-tools-readonly')).toBeNull()
  })
})

// W2c — Fallback models section visibility by agent kind (field matrix).
// subagent_3p hides it (the runner manages its own retries); every other
// kind (including Main) keeps it. Item 1 reorg: fallback models now lives
// on Basics (the default-open tab), directly below the Model section.
describe('AgentProfile — Fallback models visibility by agent kind (field matrix, W2c)', () => {
  it('hides Fallback models for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    // The Fallback models section itself is gated on !isExternalAgent
    // within Basics — never renders for subagent_3p regardless of tab.
    await waitFor(() => {
      expect(screen.queryByText(/^Fallback models$/i)).toBeNull()
    })
  })

  it('shows Fallback models for a Main agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Basics is the default-open tab — retarget here (item 1 reorg).
    // Desktop Tabs + mobile Accordion both render it simultaneously
    // (established pattern in this file), so use the *All* variant.
    switchTab('tab-basics')
    await waitFor(() => {
      expect(screen.getAllByText(/^Fallback models$/i).length).toBeGreaterThanOrEqual(1)
    })
  })
})

// W2c — Max tool calls per turn visibility by agent kind (field matrix,
// agent-types-field-matrix.md, Decisions #1 (resolved 2026-07-03): excluded
// — subagent_3p EXCLUDES max_tool_iterations).
describe('AgentProfile — Max tool calls per turn visibility by agent kind (field matrix, W2c)', () => {
  it('hides the Max tool calls input for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    switchTab('tab-advanced')
    await waitFor(() => {
      expect(screen.queryByTestId('agent-max-tool-calls-input')).toBeNull()
    })
    // Turn timeout STAYS for subagent_3p (operator decision: keep).
    expect(screen.getByTestId('agent-timeout-input')).toBeInTheDocument()
  })

  it('shows the Max tool calls input for a native Subagent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    switchTab('tab-advanced')
    expect(await screen.findByTestId('agent-max-tool-calls-input')).toBeInTheDocument()
  })

  it('never PUTs max_tool_iterations for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockSubagent3pAgent)
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    const nameInputs = screen.getAllByDisplayValue('External Researcher')
    fireEvent.change(nameInputs[0], { target: { value: 'Renamed External' } })
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })
    const payload = vi.mocked(updateAgent).mock.calls[0][1] as Record<string, unknown>
    expect(payload).not.toHaveProperty('max_tool_iterations')
    // timeout_seconds stays allowed on the wire for subagent_3p.
    expect(payload).toHaveProperty('name', 'Renamed External')
  })
})

// W2c — locked core agents: description/color/icon become visible READ-ONLY
// (previously hidden entirely), and Sampling/Rate Limits/Execution become
// EDITABLE (previously hidden entirely). Operator decision 2026-07-03.
describe('AgentProfile — locked core agent identity fields: visible read-only (W2c)', () => {
  it('shows the description as a disabled, read-only textarea', async () => {
    // Desktop Tabs AND mobile Accordion both default to "basics" and are
    // simultaneously mounted (established pattern in this file — see the
    // locked core agent shell_policy persistence test's use of getAllBy*),
    // so use the *All* query variant.
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    const descriptions = await screen.findAllByTestId('agent-description-input')
    expect(descriptions.length).toBeGreaterThanOrEqual(1)
    const description = descriptions[0] as HTMLTextAreaElement
    expect(description.disabled).toBe(true)
    expect(description.readOnly).toBe(true)
    expect(description.value).toBe(mockLockedCoreAgent.description)
  })

  it('shows a static read-only avatar color swatch (not the interactive picker)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockLockedCoreAgent, color: '#D4AF37' })
    renderProfile('mia')
    await screen.findByText('Mia')
    expect((await screen.findAllByTestId('avatar-color-readonly')).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByTestId('avatar-color-Forge Gold')).toBeNull()
  })

  it('shows a static read-only avatar icon (not the interactive picker)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    expect((await screen.findAllByTestId('avatar-icon-readonly')).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByTestId('avatar-icon-trigger')).toBeNull()
  })
})

describe('AgentProfile — locked core agent Sampling/Execution: editable (W2c)', () => {
  it('shows an interactive Sampling parameters disclosure for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    const toggles = await screen.findAllByText(/Sampling parameters/i)
    fireEvent.click(toggles[0])
    const tempLabels = await screen.findAllByText(/^Temperature$/i)
    expect(tempLabels.length).toBeGreaterThanOrEqual(1)
  })

  it('shows an interactive Execution section (timeout + max tool calls) for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    switchTab('tab-advanced')
    const timeoutInput = await screen.findByTestId('agent-timeout-input')
    expect((timeoutInput as HTMLInputElement).disabled).toBe(false)
    const maxToolCallsInput = await screen.findByTestId('agent-max-tool-calls-input')
    expect((maxToolCallsInput as HTMLInputElement).disabled).toBe(false)
  })

  it('persists a sampling-parameter edit through updateAgent for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockLockedCoreAgent)
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    const toggles = await screen.findAllByText(/Sampling parameters/i)
    fireEvent.click(toggles[0])
    const tempSliders = await screen.findAllByTestId('range-field-temperature')
    fireEvent.change(tempSliders[0], { target: { value: '1.5' } })
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 6000 })
    const last = vi.mocked(updateAgent).mock.calls.at(-1)!
    expect((last[1] as Record<string, unknown>).model_params).toMatchObject({ temperature: 1.5 })
  })
})

// Live-bug fix (two independent reviewers): the Sampling parameters
// disclosure rendered for subagent_3p with no gate, but the subagent_3p
// formData branch never sends model_params — an operator could "edit" and
// "save" temperature/max-tokens/top-P and watch it silently revert on the
// next refetch. Mirrors the sibling hide/show pattern used for Max tool
// calls per turn / Fallback models / Tools & Permissions / Skills above.
describe('AgentProfile — Sampling parameters visibility by agent kind (field matrix, live-bug fix)', () => {
  it('hides the Sampling parameters disclosure for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    await waitFor(() => {
      expect(screen.queryAllByText(/Sampling parameters/i).length).toBe(0)
    })
  })

  it('shows the Sampling parameters disclosure for a native Subagent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    expect((await screen.findAllByText(/Sampling parameters/i)).length).toBeGreaterThanOrEqual(1)
  })
})

// Every section inside toolsPanel is out of scope for external CLI workers,
// so the Tools TAB itself (and its mobile accordion item) must not render
// for subagent_3p — an unconditional trigger left an empty tab (live UAT
// finding, 2026-07-03).
describe('AgentProfile — Tools tab visibility by agent kind (field matrix)', () => {
  it('omits the Tools tab entirely for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    await waitFor(() => {
      expect(screen.queryByTestId('tab-tools')).toBeNull()
      expect(screen.queryByTestId('accordion-tools')).toBeNull()
    })
  })

  it('shows the Tools tab for a native Subagent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    expect(await screen.findByTestId('tab-tools')).toBeTruthy()
  })
})

// External CLI workers resolve their model through the RUNNER's own provider
// and auth (--model, ADR-032) — the connected-provider catalogue is the wrong
// universe for them, so the edit profile renders a free-text input instead of
// the constrained ModelSelector (operator finding, 2026-07-03).
describe('AgentProfile — Model input kind by agent kind (external = free text)', () => {
  it('renders a free-text model input (no catalogue selector) for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    const inputs = await screen.findAllByTestId('external-model-input')
    expect(inputs.length).toBeGreaterThanOrEqual(1)
    // The constrained catalogue selector must not render for this type.
    expect(screen.queryAllByLabelText(/Model selector/i).length).toBe(0)
  })

  it('renders the constrained ModelSelector (not free text) for a native Subagent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    await waitFor(() => {
      expect(screen.queryAllByTestId('external-model-input').length).toBe(0)
    })
    expect(screen.getAllByLabelText(/Model selector/i).length).toBeGreaterThanOrEqual(1)
  })
})

// Live-bug fix: the locked-agent strip-list in the useAutoSave save function
// stripped shell_policy from the PUT payload, but the backend's locked
// reject-set (pkg/gateway/rest.go ~2458) is only name/description/soul/
// color/icon/skills — shell_policy was never rejected. The shell deny-
// patterns editor rendered interactive for a locked core agent but every
// edit was silently discarded before it reached the wire.
describe('AgentProfile — locked core agent shell_policy persistence (live-bug fix)', () => {
  it('SENDS shell_policy in the PUT payload for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockLockedCoreAgent)
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')

    // Item 3 reorg: Shell deny patterns moved from the default-open Basics
    // tab into Advanced — switch there first.
    switchTab('tab-advanced')

    // Open the Shell deny patterns section (editable for locked core
    // agents too) and add a pattern.
    const shellToggles = await screen.findAllByText(/Shell deny patterns/i)
    fireEvent.click(shellToggles[0])
    const textareas = await screen.findAllByTestId('shell-deny-patterns-textarea')
    fireEvent.change(textareas[0], { target: { value: 'rm -rf /' } })

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    const payload = vi.mocked(updateAgent).mock.calls[0][1] as Record<string, unknown>
    expect(payload).toHaveProperty('shell_policy')
    expect(payload.shell_policy).toMatchObject({ custom_deny_patterns: ['rm -rf /'] })
  })
})

// Test-coverage gap (test-analyzer): the Default-agent toggle row is gated
// on `!isWorkerAgent` only — NOT `!isLocked` (operator decision 2026-07-03:
// locked core agents keep it editable/visible). No prior test asserted
// either fact for a locked core agent, nor its absence for a worker.
describe('AgentProfile — Default-agent toggle visibility (field matrix, W2c)', () => {
  it('shows the Default-agent toggle row for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    expect((await screen.findAllByTestId('default-toggle-row')).length).toBeGreaterThanOrEqual(1)
  })

  it('hides the Default-agent toggle row for a worker (Subagent)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    await waitFor(() => {
      expect(screen.queryAllByTestId('default-toggle-row').length).toBe(0)
    })
  })
})

// Test-coverage gap (test-analyzer): every existing isLocked test asserts
// the LOCKED (read-only) side. Nothing asserted the unlocked side, so a
// regression that made `isLocked` accidentally evaluate `true` for every
// agent (not just locked core ones) would slip through undetected.
describe('AgentProfile — unlocked Main agent: interactive identity fields render (isLocked regression guard, W2c)', () => {
  it('renders the interactive avatar color and icon pickers (not the read-only swatch) for an editable Main agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main', locked: false, color: '#D4AF37' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect((await screen.findAllByTestId('avatar-color-Forge Gold')).length).toBeGreaterThanOrEqual(1)
    expect((await screen.findAllByTestId('avatar-icon-trigger')).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryAllByTestId('avatar-color-readonly').length).toBe(0)
    expect(screen.queryAllByTestId('avatar-icon-readonly').length).toBe(0)
  })

  it('does NOT disable the description textarea for an editable Main agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main', locked: false })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    const descriptions = await screen.findAllByTestId('agent-description-input')
    expect(descriptions.length).toBeGreaterThanOrEqual(1)
    const description = descriptions[0] as HTMLTextAreaElement
    expect(description.disabled).toBe(false)
    expect(description.readOnly).toBe(false)
  })
})
