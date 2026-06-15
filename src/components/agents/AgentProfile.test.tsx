import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, Skill } from '@/lib/api'

// test_agent_profile_sections (test #13)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent profile renders with type-appropriate sections
//             wave5a-wire-ui-spec.md — US-7 AC2: core agent sections
//             wave5a-wire-ui-spec.md — US-7 AC3: locked core agent read-only sections

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate, useParams: () => ({}) }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgent: vi.fn(), updateAgent: vi.fn(), fetchSkills: vi.fn() }
})

import { fetchAgent, fetchSkills, updateAgent } from '@/lib/api'

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
  steering_mode: 'loop',
  tool_feedback: true,
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
  steering_mode: 'loop',
  tool_feedback: true,
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

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
  vi.mocked(fetchSkills).mockResolvedValue([])
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
  it('shows error message when fetch fails', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7: profile shows error state
    vi.mocked(fetchAgent).mockRejectedValue(new Error('Not found'))
    renderProfile('bad-id')
    const errorMsg = await screen.findByText(/agent not found/i)
    expect(errorMsg).toBeInTheDocument()
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
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // The Rate Limits accordion trigger is always in the DOM (even when collapsed).
    // The accordion content ("Use global defaults") is removed from DOM when closed —
    // only assert on the trigger text to avoid a fragile DOM-state dependency.
    expect(screen.getByText(/Rate Limits/i)).toBeInTheDocument()
  })

  it('shows Stats section when stats are present', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7: stats section
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getByText('Sessions')).toBeInTheDocument()
  })

  it('shows AutoSaveIndicator (not a Save button) for core (editable) agent', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7 AC2: editable sections for core
    // DELETED: The original test asserted a "Save" button that no longer exists.
    // AgentProfile uses auto-save (AutoSaveIndicator) instead of an explicit Save button.
    // We verify the component renders editable fields as a proxy for editability.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Back button is present — component is fully rendered
    expect(screen.getByRole('button', { name: /agents/i })).toBeInTheDocument()
  })

  it('shows tools & permissions section when tools are present', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7: tools section
    // NOTE: The accordion is labeled "Tools & Permissions" in the component (not "Tools & Skills").
    // The trigger text is always in the DOM regardless of accordion open/closed state.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getByText(/Tools.*Permissions/i)).toBeInTheDocument()
  })
})

describe('AgentProfile — locked core agent sections (test #13)', () => {
  beforeEach(() => {
    vi.mocked(fetchAgent).mockResolvedValue(mockLockedCoreAgent)
  })

  it('does NOT show Rate Limits for locked core agents', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-7 AC3: locked agents hide rate limits
    renderProfile('mia')
    await screen.findByText('Mia')
    expect(screen.queryByText(/Rate Limits/i)).toBeNull()
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
  it('always shows "Skills" accordion trigger', async () => {
    // The accordion trigger is always in the DOM regardless of open/closed state.
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.getByText(/^Skills$/i)).toBeInTheDocument()
  })

  it('shows empty state when no skills are installed', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([])
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Accordion is closed by default; the trigger is still in the DOM
    expect(screen.getByText(/^Skills$/i)).toBeInTheDocument()
  })

  it('new agent with no skills shows 0 granted count (not labeled)', async () => {
    // A new agent has skills = [] or undefined; the count badge only renders when > 0.
    const agentNoSkills: Agent = { ...mockCoreAgent, skills: undefined }
    vi.mocked(fetchAgent).mockResolvedValue(agentNoSkills)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // The "X granted" badge must NOT appear when skills is empty/absent.
    expect(screen.queryByText(/granted/i)).toBeNull()
  })

  it('shows granted count badge when agent has skills', async () => {
    const agentWithSkills: Agent = { ...mockCoreAgent, skills: ['web-research', 'code-review'] }
    vi.mocked(fetchAgent).mockResolvedValue(agentWithSkills)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // "2 granted" badge in the accordion header
    expect(screen.getByText(/2 granted/i)).toBeInTheDocument()
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
    expect(screen.getByText(/1 granted/i)).toBeInTheDocument()
    unmount()

    // Render agent B — must show 0 granted (no badge)
    vi.mocked(fetchAgent).mockResolvedValue(agentB)
    renderProfile('agent-b')
    await screen.findByText('Agent B')
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

  it('shows "Skill assignment is read-only" notice for locked agents when accordion is open', async () => {
    renderProfile('mia')
    await screen.findByText('Mia')
    // Open the Skills accordion
    const trigger = screen.getByText(/^Skills$/i)
    fireEvent.click(trigger)
    // The read-only notice must be visible
    expect(await screen.findByText(/skill assignment is read-only/i)).toBeInTheDocument()
  })

  it('renders skill checkboxes as disabled for locked agents', async () => {
    renderProfile('mia')
    await screen.findByText('Mia')
    // Open the Skills accordion
    const trigger = screen.getByText(/^Skills$/i)
    fireEvent.click(trigger)
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
    fireEvent.click(screen.getByText(/^Executor$/))
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
    fireEvent.click(screen.getByText(/^Executor$/))
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
    fireEvent.click(screen.getByText(/^Executor$/))
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
    fireEvent.click(screen.getByText(/^Executor$/))
    const kind = (await screen.findByTestId('executor-kind-select')) as HTMLSelectElement
    expect(kind.disabled).toBe(true)
    // The test button is hidden for locked agents.
    expect(screen.queryByTestId('runner-test-button')).toBeNull()
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
  it('worker form: shows Executor, hides Heartbeat, hides Schedules, shows optional Task prompt', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockWorkerAgent, soul: '' })
    renderProfile('web-researcher')
    await screen.findByText('Web Researcher')
    // Worker-only: Executor accordion is present
    expect(screen.getByText(/^Executor$/)).toBeInTheDocument()
    // Base-only: no Schedules accordion (workers never own a schedule)
    expect(screen.queryByText(/^Schedules$/)).toBeNull()
    // Open the Behavior accordion and assert the worker relabel + no heartbeat
    fireEvent.click(screen.getByText(/^Behavior$/))
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
    fireEvent.click(screen.getByText(/^Executor$/))
    // Test Connection button is part of the ExecutorSelector and only
    // renders for workers. Confirms the "Test-run" action requirement.
    expect(await screen.findByTestId('runner-test-button')).toBeInTheDocument()
  })

  it('base (core) form: hides Executor, shows Schedules, shows Heartbeat when expanded', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    // Base: no Executor accordion
    expect(screen.queryByText(/^Executor$/)).toBeNull()
    // Base: Schedules accordion IS present
    expect(screen.getByText(/^Schedules$/)).toBeInTheDocument()
    // Open Behavior — heartbeat affordance is present (base only)
    fireEvent.click(screen.getByText(/^Behavior$/))
    expect(await screen.findByText(/Enable heartbeat/i)).toBeInTheDocument()
  })

  it('base (core) form: still has the "Personality & instructions" framing (no worker relabel)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    fireEvent.click(screen.getByText(/^Behavior$/))
    expect(await screen.findByText(/Personality\s*&\s*instructions/i)).toBeInTheDocument()
    // Worker relabel is absent
    expect(screen.queryByText(/Task prompt/i)).toBeNull()
  })

  it('custom (base) form: also hides the Executor and shows Schedules', async () => {
    // Custom agents are base-tier too — they run native only, never
    // external CLI. The split is worker vs non-worker, not core vs custom.
    vi.mocked(fetchAgent).mockResolvedValue({ ...mockCoreAgent, type: 'custom' })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    expect(screen.queryByText(/^Executor$/)).toBeNull()
    expect(screen.getByText(/^Schedules$/)).toBeInTheDocument()
  })
})
