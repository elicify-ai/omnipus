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
  return { ...actual, fetchAgent: vi.fn(), updateAgent: vi.fn(), deleteAgent: vi.fn(), fetchSkills: vi.fn(), fetchProviders: vi.fn(), testAgentRunner: vi.fn() }
})

import { fetchAgent, fetchSkills, updateAgent, deleteAgent, fetchProviders, testAgentRunner } from '@/lib/api'
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
  heartbeat: '',
  instructions: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  steering_mode: 'one-at-a-time',
  heartbeat_enabled: false,
  heartbeat_interval: 300,
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
  heartbeat: '',
  instructions: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  steering_mode: 'one-at-a-time',
  heartbeat_enabled: false,
  heartbeat_interval: 300,
}

// Tier-branched form fixtures (Spec-4 FR-4.1 + locked concept in
// `.preview-doc/agents.html`): workers carry an executor; base agents do not.
// `mockWorkerAgent` is the canonical editable worker; `mockLockedWorkerAgent`
// is a worker that has been locked down (e.g. a marketplace-supplied worker).
const mockWorkerAgent: Agent = {
  ...mockCoreAgent,
  id: 'web-researcher',
  name: 'Web Researcher',
  type: 'worker',
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
    expect(screen.getByText(/core/i)).toBeInTheDocument()
    expect(screen.getByText(/Model/)).toBeInTheDocument()
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

  it('does NOT show Rate Limits for locked core agents', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7 AC3: locked agents hide rate limits
    // Wave 5: open the Advanced tab and confirm the "Use global defaults"
    // copy is absent (the entire Rate Limits section is omitted for
    // locked agents).
    renderProfile('mia')
    await screen.findByText('Mia')
    switchTab('tab-advanced')
    // Use a small wait to let Radix activate the panel.
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.queryByText(/Use global defaults/i)).toBeNull()
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

// Wave 6 / G10 — when the worker's executor is external-cli, Omnipus'
// sandbox_profile is ignored at runtime; the operator needs a visible
// callout so the chosen profile isn't mistaken for an enforcement guarantee.
// Worker accordions are default-open (W6-B1 / I1) so the Sandbox block is
// rendered on mount; no extra click is needed.
describe('AgentProfile — Sandbox callout when executor is external-cli (G10)', () => {
  it('renders the "sandbox ignored" callout for external-cli workers with a non-off profile', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockWorkerAgent,
      executor: { kind: 'external-cli', cli: 'claude-code' },
      sandbox_profile: 'workspace',
    })
    renderProfile('web-researcher')
    const callout = await screen.findByTestId('sandbox-external-cli-ignored-callout')
    expect(callout).toHaveAttribute('role', 'note')
    expect(callout).toHaveAttribute('aria-live', 'polite')
    expect(callout).toHaveTextContent(/sandbox profile is ignored when executor\.kind=external-cli/i)
    expect(callout).toHaveTextContent(/external CLI manages its own isolation/i)
  })

  it('does NOT render the callout for native workers even with a non-off profile', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockWorkerAgent,
      executor: { kind: 'native' },
      sandbox_profile: 'workspace',
    })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    expect(screen.queryByTestId('sandbox-external-cli-ignored-callout')).toBeNull()
  })

  it('does NOT render the callout when sandbox_profile is "off" (warning would be redundant)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockWorkerAgent,
      executor: { kind: 'external-cli', cli: 'claude-code' },
      sandbox_profile: 'off',
    })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    expect(screen.queryByTestId('sandbox-external-cli-ignored-callout')).toBeNull()
  })
})

// Tier-branched form (locked concept: `.preview-doc/agents.html`).
// A worker is a delegation-only labour agent — never a chat target, no
// heartbeat, never the default. The form reflects that by HIDE-ing the
// worker-irrelevant accordions and relabelling the soul field as an
// optional "Task prompt". Base agents get the full set.
describe('AgentProfile — tier-branched form (worker vs base)', () => {
  it('worker form: shows Executor, hides Heartbeat, hides Schedules, shows optional Task prompt', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, soul: '' })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    // Worker-only: Executor section is inside the Advanced tab.
    switchTab('tab-advanced')
    expect(await screen.findByText(/^Executor$/)).toBeInTheDocument()
    // Base-only: no Schedules section in Advanced (workers never own a schedule)
    expect(screen.queryByText(/^Schedules$/)).toBeNull()
    // Open the Personality tab and assert the worker relabel + no heartbeat
    switchTab('tab-personality')
    const taskPrompt = await screen.findByTestId('worker-task-prompt')
    // Optional: not required by the browser, no aria-required="true"
    expect((taskPrompt as HTMLTextAreaElement).required).toBe(false)
    expect(taskPrompt.getAttribute('aria-required')).not.toBe('true')
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

  it('base (core) form: hides Executor, shows Schedules, shows Heartbeat when expanded', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Base: Schedules is in the Advanced tab. Switch to it first.
    switchTab('tab-advanced')
    // Base: no Executor section (only workers have one)
    expect(screen.queryByText(/^Executor$/)).toBeNull()
    // Base: Schedules section IS present
    expect(await screen.findByText(/^Schedules$/)).toBeInTheDocument()
    // Heartbeat is in the Personality tab.
    switchTab('tab-personality')
    expect(await screen.findByText(/Enable heartbeat/i)).toBeInTheDocument()
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

  it('custom (base) form: also hides the Executor and shows Schedules', async () => {
    // Custom agents are base-tier too — they run native only, never
    // external CLI. The split is worker vs non-worker, not core vs custom.
    // Wire contract: user-created chat agents use type='Main' (the
    // 'custom' enum value was retired in the Wave 1 spec).
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'Main' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Both Executor and Schedules live in the Advanced tab now.
    switchTab('tab-advanced')
    expect(screen.queryByText(/^Executor$/)).toBeNull()
    expect(await screen.findByText(/^Schedules$/)).toBeInTheDocument()
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

  it('hides the fallback editor entirely for locked core agents', async () => {
    // Locked agents (see `.preview-doc/agents.html` for the current
    // roster — Mia · Assistant ⭐, etc.) cannot edit their model
    // configuration — the editor is rendered in `canEdit` blocks and
    // must not surface for the locked roster. This guards against a
    // regression where the provider-aware editor leaks into the locked path.
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
    renderProfile('mia')
    await screen.findByText('Mia')
    // The locked agent renders the model selector (read-only) but NOT
    // the fallback editor. The accordion may be open or closed; either
    // way the add-trigger and chips must be absent.
    expect(screen.queryByTestId('fallback-chip-model-claude-opus-4-6')).toBeNull()
    expect(screen.queryByTestId('fallback-add-trigger')).toBeNull()
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
    expect(await screen.findByTestId('fallback-summary-locked')).toBeInTheDocument()
    expect(screen.getByText(/inherited from the locked core config/i)).toBeInTheDocument()
    // The summary lists the configured fallback (model + provider).
    expect(screen.getByTestId('fallback-summary-model-claude-opus-4-6')).toHaveTextContent(/claude-opus-4-6/)
    expect(screen.getByTestId('fallback-summary-provider-claude-opus-4-6')).toHaveTextContent(/anthropic/i)
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
    expect(await screen.findByTestId('fallback-summary-locked')).toBeInTheDocument()
    expect(screen.getByText(/no fallback chain configured/i)).toBeInTheDocument()
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

  it('renders 5 tabs including Runtime for a subagent_3p agent', async () => {
    // Traces to: agent-form-requirements.md §9.3 — "Edit slide-over (Subagent External) shows 5 tabs"
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
    expect(screen.getByTestId('tab-tools')).toBeInTheDocument()
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
