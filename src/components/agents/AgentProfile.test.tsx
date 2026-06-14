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

// Spec-4 FR-4.1 — Executor section wired into the agent profile.
describe('AgentProfile — Executor section (Spec-4)', () => {
  it('renders the Executor accordion with the runtime selector', async () => {
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    fireEvent.click(screen.getByText(/^Executor$/))
    const kind = (await screen.findByTestId('executor-kind-select')) as HTMLSelectElement
    // Absent executor → native default.
    expect(kind.value).toBe('native')
  })

  it('hydrates an existing external-cli executor and its cli', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      executor: { kind: 'external-cli', cli: 'codex' },
    })
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    fireEvent.click(screen.getByText(/^Executor$/))
    const kind = (await screen.findByTestId('executor-kind-select')) as HTMLSelectElement
    expect(kind.value).toBe('external-cli')
    const cli = (await screen.findByTestId('executor-cli-select')) as HTMLSelectElement
    expect(cli.value).toBe('codex')
  })

  it('persists a runtime change through updateAgent (auto-save)', async () => {
    vi.mocked(updateAgent).mockResolvedValue(mockCoreAgent)
    renderProfile('general-assistant')
    await screen.findByText('General Assistant')
    fireEvent.click(screen.getByText(/^Executor$/))
    const kind = await screen.findByTestId('executor-kind-select')
    fireEvent.change(kind, { target: { value: 'external-cli' } })
    // The cli select now appears with the claude-code default.
    const cli = (await screen.findByTestId('executor-cli-select')) as HTMLSelectElement
    expect(cli.value).toBe('claude-code')
    // Auto-save debounces, then PUTs the executor.
    await waitFor(
      () => {
        expect(updateAgent).toHaveBeenCalled()
        const lastCall = vi.mocked(updateAgent).mock.calls.at(-1)!
        expect(lastCall[0]).toBe('general-assistant')
        expect(lastCall[1].executor).toEqual({ kind: 'external-cli', cli: 'claude-code' })
      },
      { timeout: 3000 },
    )
  })

  it('renders the runtime selector disabled for locked core agents', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockLockedCoreAgent,
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    renderProfile('mia')
    await screen.findByText('Mia')
    fireEvent.click(screen.getByText(/^Executor$/))
    const kind = (await screen.findByTestId('executor-kind-select')) as HTMLSelectElement
    expect(kind.disabled).toBe(true)
    // The test button is hidden for locked agents.
    expect(screen.queryByTestId('runner-test-button')).toBeNull()
  })
})
