import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
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

// test_agent_profile_sections (test #13)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent profile renders with type-appropriate sections
//             wave5a-wire-ui-spec.md — US-7 AC2: core agent sections
//             wave5a-wire-ui-spec.md — US-7 AC3: locked core agent read-only sections

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => ({}),
    // W6-C1 / G2: AgentProfile now renders a <Link to="/agents/trust"> so
    // the operator can jump to the delegation graph. TanStack Router's
    // real <Link> calls useLinkProps → useRouter, which throws without
    // a RouterProvider; stub it with a plain anchor so the screen renders
    // in isolation (these tests assert content, not navigation behaviour).
    Link: ({ children, to, ...rest }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
    ),
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
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'General purpose assistant',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  steering_mode: 'one-at-a-time',
  rate_limits: { use_global_defaults: true },
  stats: { total_sessions: 5, total_tokens: 12000, total_cost: 0.05 },
}

const mockLockedCoreAgent: Agent = {
  id: 'mia',
  name: 'Mia',
  type: 'core',
  locked: true,
  status: 'active',
  model: 'claude-opus-4-6',
  description: 'Core agent with compiled prompt — identity is read-only',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  steering_mode: 'one-at-a-time',
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

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderProfile(agentId: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
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
    { id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter', status: 'connected', models: ['z-ai/glm-5.2', 'z-ai/glm-5-turbo'] },
    { id: 'anthropic', name: 'anthropic', display_name: 'Anthropic', status: 'connected', models: ['claude-sonnet-4-6', 'claude-opus-4-6'] },
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

// US-E6: per-agent skill assignment tests
// Traces to: nontech-ux-hardening-spec.md §6.5, F-06
describe('AgentProfile — Skills picker (US-E6)', () => {
  it('always shows "Skills" section heading', async () => {
    // Wave 5: Skills now lives inside the Tools tab. Open the tab and
    // assert the section heading is present (the heading is always
    // visible inside the tab panel).
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-tools')
    expect(await screen.findByText(/^Skills$/i)).toBeInTheDocument()
  })

  it('shows empty state when no skills are installed', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([])
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-tools')
    expect(await screen.findByText(/^Skills$/i)).toBeInTheDocument()
  })

  it('new agent with no skills shows 0 granted count (not labeled)', async () => {
    // A new agent has skills = [] or undefined; the count badge only renders when > 0.
    const agentNoSkills: Agent = { ...mockCoreAgent, skills: undefined }
    vi.mocked(fetchAgent).mockResolvedValue(agentNoSkills)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-tools')
    await screen.findByText(/^Skills$/i)
    // The "X granted" badge must NOT appear when skills is empty/absent.
    expect(screen.queryByText(/granted/i)).toBeNull()
  })

  it('shows granted count badge when agent has skills', async () => {
    const agentWithSkills: Agent = { ...mockCoreAgent, skills: ['web-research', 'code-review'] }
    vi.mocked(fetchAgent).mockResolvedValue(agentWithSkills)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-tools')
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
    switchTab('tab-tools')
    expect(await screen.findByText(/1 granted/i)).toBeInTheDocument()
    unmount()

    // Render agent B — must show 0 granted (no badge)
    vi.mocked(fetchAgent).mockResolvedValue(agentB)
    renderProfile('agent-b')
    await screen.findByText('Agent B')
    switchTab('tab-tools')
    await screen.findByText(/^Skills$/i)
    expect(screen.queryByText(/granted/i)).toBeNull()
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
    // Open the Tools tab where Skills now lives
    switchTab('tab-tools')
    // The read-only notice must be visible
    expect(await screen.findByText(/skill assignment is read-only/i)).toBeInTheDocument()
  })

  it('renders skill checkboxes as disabled for locked agents', async () => {
    renderProfile('mia')
    await screen.findByText('Mia')
    // Open the Tools tab where Skills now lives
    switchTab('tab-tools')
    // Wait for skill to appear
    const checkbox = await screen.findByTestId('skill-checkbox-web-research')
    expect((checkbox as HTMLInputElement).disabled).toBe(true)
  })
})

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
    const kind = (await screen.findByTestId('executor-kind-select')) as HTMLSelectElement
    // Absent executor → native default.
    expect(kind.value).toBe('native')
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
    const kind = (await screen.findByTestId('executor-kind-select')) as HTMLSelectElement
    expect(kind.value).toBe('external-cli')
    const cli = (await screen.findByTestId('executor-cli-select')) as HTMLSelectElement
    expect(cli.value).toBe('codex')
  })

  it('persists a worker runtime change through updateAgent (auto-save)', async () => {
    vi.mocked(updateAgent).mockResolvedValue(mockWorkerAgent)
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, executor: undefined })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    if (!screen.queryByTestId('executor-kind-select')) {
      switchTab('tab-advanced')
    }
    const kind = await screen.findByTestId('executor-kind-select')
    fireEvent.change(kind, { target: { value: 'external-cli' } })
    // The cli select now appears with the claude-code default.
    const cli = (await screen.findByTestId('executor-cli-select')) as HTMLSelectElement
    expect(cli.value).toBe('claude-code')
    // Auto-save debounces, then PUTs the executor with the worker id.
    await waitFor(
      () => {
        expect(updateAgent).toHaveBeenCalled()
        const lastCall = vi.mocked(updateAgent).mock.calls.at(-1)!
        expect(lastCall[0]).toBe('web-researcher')
        expect(lastCall[1].executor).toEqual({ kind: 'external-cli', cli: 'claude-code' })
      },
      { timeout: 3000 },
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
    const kind = (await screen.findByTestId('executor-kind-select')) as HTMLSelectElement
    expect(kind.disabled).toBe(true)
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
    const kind = await screen.findByTestId('executor-kind-select')
    fireEvent.change(kind, { target: { value: 'external-cli' } })
    await waitFor(() => {
      expect(testAgentRunner).toHaveBeenCalledWith('web-researcher')
    }, { timeout: 3000 })
    // And the save still commits (test returned ok).
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 3000 })
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
    const kind = await screen.findByTestId('executor-kind-select')
    fireEvent.change(kind, { target: { value: 'external-cli' } })
    await waitFor(() => expect(testAgentRunner).toHaveBeenCalled(), { timeout: 3000 })
    // Give the auto-save a chance to attempt the save.
    await new Promise((r) => setTimeout(r, 800))
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
  // The Model Configuration accordion must be opened before the fallback
  // editor is visible. Helper that mounts the profile and opens the
  // accordion, returning the underlying container.
  async function openFallbackEditor(agent: typeof mockCoreAgent = mockCoreAgent) {
    vi.mocked(fetchAgent).mockResolvedValue(agent)
    renderProfile(agent.id)
    await screen.findByText(agent.name)
    // The fallback section heading is "Fallback models (...)" inside the panel.
    // Wave 5 / spec §6.2: the fallback editor lives inside the Tools tab
    // (per the spec, fallbacks are a "tooling surface"). If the Tools tab
    // is not already active, click the trigger to switch to it.
    if (!screen.queryByText(/Fallback models/i)) {
      switchTab('tab-tools')
    }
    await screen.findByText(/Fallback models/i)
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
    // Each model name appears as a chip
    expect(screen.getByTestId('fallback-chip-model-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-model-claude-sonnet-4-6')).toBeInTheDocument()
    // Each chip has a provider badge (FR-005)
    expect(screen.getByTestId('fallback-chip-provider-z-ai/glm-5-turbo')).toHaveTextContent(/openrouter/i)
    expect(screen.getByTestId('fallback-chip-provider-claude-sonnet-4-6')).toHaveTextContent(/anthropic/i)
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
    expect(screen.getByTestId('fallback-chip-model-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-model-claude-sonnet-4-6')).toBeInTheDocument()
    // Click the remove button on the first chip
    fireEvent.click(screen.getByTestId('fallback-chip-remove-z-ai/glm-5-turbo'))
    // First chip is gone, second remains
    expect(screen.queryByTestId('fallback-chip-model-z-ai/glm-5-turbo')).toBeNull()
    expect(screen.getByTestId('fallback-chip-model-claude-sonnet-4-6')).toBeInTheDocument()
  })

  it('adds a fallback via the ModelSelector with provider tracking', async () => {
    // Provider tracking on add: picking a model in the dropdown must record
    // both model AND the provider that owns it (per spec, the fallback can
    // route through a different provider).
    await openFallbackEditor({ ...mockCoreAgent, fallback_models: [] })

    // The "add fallback" ModelSelector is mounted. Open it and pick a model.
    // The trigger button is identified by data-testid on the add-fallback
    // selector (the primary model selector above uses a different testid).
    const addTrigger = screen.getByTestId('fallback-add-trigger')
    fireEvent.click(addTrigger)

    // The CommandItem for claude-sonnet-4-6 lives inside the popover content.
    // OpenAI / Anthropic models appear under their provider heading.
    const item = await screen.findByTestId('fallback-add-item-claude-sonnet-4-6')
    fireEvent.click(item)

    // The new chip should now appear with both model and provider
    expect(await screen.findByTestId('fallback-chip-model-claude-sonnet-4-6')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-provider-claude-sonnet-4-6')).toHaveTextContent(/anthropic/i)
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
    fireEvent.click(screen.getByTestId('fallback-chip-remove-z-ai/glm-5-turbo'))
    // Wait for the auto-save debounce + flush
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls
        expect(calls.length).toBeGreaterThan(0)
      },
      { timeout: 3000 },
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

  it('locked core agents: read-only fallback summary, no add-trigger, no provider picker (G6)', async () => {
    // W6-C2 / G6: `fallback_models` is wire-allowed for locked core
    // agents but the editor strips it via `canEdit`. Pre-C2 operators
    // had no way to see what the locked core compiled with; now we
    // surface the configured chain as a read-only summary so the
    // operator can verify the inherited fallback.
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
    // The provider select is rendered for each chip.
    const providerSelect = screen.getByTestId('fallback-provider-select-z-ai/glm-5-turbo')
    expect(providerSelect).toBeInTheDocument()
    expect((providerSelect as HTMLSelectElement).value).toBe('openrouter')
    // Connected providers are populated as options.
    expect(screen.getByTestId('fallback-provider-option-openrouter-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-provider-option-anthropic-z-ai/glm-5-turbo')).toBeInTheDocument()
    // An "empty" option exists so the user can clear the provider
    // (which surfaces the persistent warning — I11).
    expect(screen.getByTestId('fallback-provider-option-empty-z-ai/glm-5-turbo')).toBeInTheDocument()
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
    fireEvent.change(screen.getByTestId('fallback-provider-select-claude-sonnet-4-6'), {
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
      { timeout: 3000 },
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
    expect(screen.getByTestId('fallback-chip-up-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-down-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-up-claude-sonnet-4-6')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-down-claude-sonnet-4-6')).toBeInTheDocument()
    // Up is disabled for the first chip.
    expect((screen.getByTestId('fallback-chip-up-z-ai/glm-5-turbo') as HTMLButtonElement).disabled).toBe(true)
    // Down is disabled for the last chip.
    expect((screen.getByTestId('fallback-chip-down-claude-sonnet-4-6') as HTMLButtonElement).disabled).toBe(true)
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
    fireEvent.click(screen.getByTestId('fallback-chip-down-z-ai/glm-5-turbo'))
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls.filter(
          ([id]) => id === mockCoreAgent.id,
        )
        expect(calls.length).toBeGreaterThan(0)
      },
      { timeout: 3000 },
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
    const warning = screen.getByTestId('fallback-chip-warning-some-unconnected-slug')
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
    fireEvent.change(screen.getByTestId('fallback-provider-select-z-ai/glm-5-turbo'), {
      target: { value: '' },
    })
    expect(
      await screen.findByTestId('fallback-chip-warning-z-ai/glm-5-turbo'),
    ).toBeInTheDocument()
  })

  // W6-C2 / G6: read-only summary for locked core agents. Operators
  // can see what the locked core compiled with but cannot edit it.
  it('locked core agents: read-only fallback summary, no add-trigger, no provider picker (G6)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockLockedCoreAgent,
      fallback_models: [
        { model: 'claude-opus-4-6', provider: 'anthropic' },
      ],
    })
    renderProfile('mia')
    await screen.findByText('Mia')
    // Fallback editor is in the Tools tab. Switch to it first.
    switchTab('tab-tools')
    // The summary panel is present (G6), the locked note is visible.
    expect(await screen.findByTestId('fallback-summary-locked-tools')).toBeInTheDocument()
    expect(screen.getAllByText(/inherited from the locked core config/i).length).toBeGreaterThanOrEqual(1)
    // The summary lists the configured fallback (model + provider).
    screen.getAllByTestId('fallback-summary-model-claude-opus-4-6').forEach((el) => {
      expect(el).toHaveTextContent(/claude-opus-4-6/)
    })
    screen.getAllByTestId('fallback-summary-provider-claude-opus-4-6').forEach((el) => {
      expect(el).toHaveTextContent(/anthropic/i)
    })
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
    // Fallback editor is in the Tools tab.
    switchTab('tab-tools')
    expect(await screen.findByTestId('fallback-summary-locked-tools')).toBeInTheDocument()
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
    // The provider select is rendered for each chip.
    const providerSelect = screen.getByTestId('fallback-provider-select-z-ai/glm-5-turbo')
    expect(providerSelect).toBeInTheDocument()
    expect((providerSelect as HTMLSelectElement).value).toBe('openrouter')
    // Connected providers are populated as options.
    expect(screen.getByTestId('fallback-provider-option-openrouter-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-provider-option-anthropic-z-ai/glm-5-turbo')).toBeInTheDocument()
    // An "empty" option exists so the user can clear the provider
    // (which surfaces the persistent warning — I11).
    expect(screen.getByTestId('fallback-provider-option-empty-z-ai/glm-5-turbo')).toBeInTheDocument()
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
    fireEvent.change(screen.getByTestId('fallback-provider-select-claude-sonnet-4-6'), {
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
      { timeout: 3000 },
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

  it('clearing the provider combobox flips the chip into the warning state (I11 + I9 integration)', async () => {
    // Sanity: an explicit user action — picking "—" on the provider
    // select — must trigger the warning indicator on that chip.
    await openFallbackEditor({
      ...mockCoreAgent,
      fallback_models: [
        { model: 'z-ai/glm-5-turbo', provider: 'openrouter' },
      ],
    })
    expect(screen.queryByTestId('fallback-chip-warning-z-ai/glm-5-turbo')).toBeNull()
    fireEvent.change(screen.getByTestId('fallback-provider-select-z-ai/glm-5-turbo'), {
      target: { value: '' },
    })
    expect(
      await screen.findByTestId('fallback-chip-warning-z-ai/glm-5-turbo'),
    ).toBeInTheDocument()
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
    expect(screen.getByTestId('fallback-chip-up-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-down-z-ai/glm-5-turbo')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-up-claude-sonnet-4-6')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-chip-down-claude-sonnet-4-6')).toBeInTheDocument()
    // Up is disabled for the first chip.
    expect((screen.getByTestId('fallback-chip-up-z-ai/glm-5-turbo') as HTMLButtonElement).disabled).toBe(true)
    // Down is disabled for the last chip.
    expect((screen.getByTestId('fallback-chip-down-claude-sonnet-4-6') as HTMLButtonElement).disabled).toBe(true)
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
    fireEvent.click(screen.getByTestId('fallback-chip-down-z-ai/glm-5-turbo'))
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls.filter(
          ([id]) => id === mockCoreAgent.id,
        )
        expect(calls.length).toBeGreaterThan(0)
      },
      { timeout: 3000 },
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
  // fallback would silently fail at runtime. The chip must surface
  // a persistent indicator with a canonical accessible name; the
  // pre-C2 dash was unexplained (the ticket).
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
    const warning = screen.getByTestId('fallback-chip-warning-some-unconnected-slug')
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
})

// Wave 5 / spec §6 — Edit slide-over body uses a 4-5 tab layout (Basics,
// Personality, Tools, Advanced [+Runtime for subagent_3p]) instead of the
// prior 10-section Accordion. The `tab-basics` / `tab-personality` /
// `tab-tools` / `tab-advanced` / `tab-runtime` testids anchor the tab bar;
// tests below exercise the spec BDDs (#15/#16/#17 from agent-form-requirements.md).
describe('AgentProfile — Wave 5 tab structure (spec §6.2-§6.4)', () => {
  it('renders 4 tabs (basics, personality, tools, advanced) for a Main agent', async () => {
    // Traces to: agent-form-requirements.md §9.3 — "Edit slide-over (Main) shows 4 tabs"
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getByTestId('tab-basics')).toBeInTheDocument()
    expect(screen.getByTestId('tab-personality')).toBeInTheDocument()
    expect(screen.getByTestId('tab-tools')).toBeInTheDocument()
    expect(screen.getByTestId('tab-advanced')).toBeInTheDocument()
    // No Runtime tab for Main
    expect(screen.queryByTestId('tab-runtime')).toBeNull()
  })

  it('renders 4 tabs including Runtime (and NO Tools tab) for a subagent_3p agent', async () => {
    // Traces to: agent-form-requirements.md §9.3, amended by the field
    // matrix: the Tools tab is omitted for external CLI workers (every
    // toolsPanel section is out of scope for them), so the external edit
    // slide-over shows Basics / Personality / Runtime / Advanced.
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
    expect(screen.getByTestId('tab-runtime')).toBeInTheDocument()
    expect(screen.getByTestId('tab-advanced')).toBeInTheDocument()
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

// ── 409 Conflict UI ────────────────────────────────────────────────────────────

describe('AgentProfile — 409 conflict handling', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockCoreAgent)
    vi.mocked(updateAgent).mockReset().mockResolvedValue(mockCoreAgent)
    vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  })

  it('surfaces a toast with Refresh action on 409 conflict', async () => {
    vi.mocked(updateAgent).mockRejectedValueOnce(new ApiError(409, 'conflict'))

    renderProfile('general-assistant')
    await screen.findByText('General Assistant')

    // Capture toasts via the store's subscribe mechanism
    const { useUiStore } = await import('@/store/ui')
    const toastsBefore = useUiStore.getState().toasts.length

    // Trigger a save by changing a field
    const nameInputs = screen.getAllByDisplayValue('General Assistant')
    fireEvent.change(nameInputs[0], { target: { value: 'Changed Name' } })

    // Wait for the debounced auto-save to fire and call updateAgent
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalled()
    }, { timeout: 5000 })

    // Wait for the 409 catch block to add a toast
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts
      expect(toasts.length).toBeGreaterThan(toastsBefore)
      expect(toasts.some((t: { message: string }) => /changed elsewhere/i.test(t.message))).toBe(true)
    }, { timeout: 3000 })

    // Verify the Refresh action is attached
    const state = useUiStore.getState()
    const conflictToast = state.toasts.find((t: { message: string }) => /changed elsewhere/i.test(t.message))
    expect(conflictToast?.action?.label).toBe('Refresh')
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
    expect(payload).not.toHaveProperty('delegation_policy')
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
      { timeout: 3000 },
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
    switchTab('tab-tools')
    expect(await screen.findByText(/^Skills$/i)).toBeInTheDocument()
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
// kind (including Main) keeps it.
describe('AgentProfile — Fallback models visibility by agent kind (field matrix, W2c)', () => {
  it('hides Fallback models for a subagent_3p agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockSubagent3pAgent)
    renderProfile('external-researcher')
    await screen.findByText('External Researcher')
    // The Tools tab (the fallback editor's home) is omitted for 3p.
    await waitFor(() => {
      expect(screen.queryByTestId('tab-tools')).toBeNull()
      expect(screen.queryByText(/^Fallback models$/i)).toBeNull()
    })
  })

  it('shows Fallback models for a Main agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    switchTab('tab-tools')
    expect(await screen.findByText(/^Fallback models$/i)).toBeInTheDocument()
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
    }, { timeout: 3000 })
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

// Test-coverage gap (test-analyzer): the Default-agent toggle row and the
// delegation-policy summary are gated on `!isWorkerAgent` only — NOT
// `!isLocked` (operator decision 2026-07-03: locked core agents keep these
// editable/visible). No prior test asserted either fact for a locked core
// agent, nor their absence for a worker.
describe('AgentProfile — Default-agent toggle and delegation-policy summary visibility (field matrix, W2c)', () => {
  it('shows the Default-agent toggle row for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    expect((await screen.findAllByTestId('default-toggle-row')).length).toBeGreaterThanOrEqual(1)
  })

  it('shows the delegation-policy summary for a locked core agent', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    expect((await screen.findAllByTestId('delegation-policy-summary')).length).toBeGreaterThanOrEqual(1)
  })

  it('hides the Default-agent toggle row and delegation-policy summary for a worker (Subagent)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockWorkerAgent)
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    await waitFor(() => {
      expect(screen.queryAllByTestId('default-toggle-row').length).toBe(0)
    })
    expect(screen.queryAllByTestId('delegation-policy-summary').length).toBe(0)
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
