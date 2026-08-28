import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentListScreen } from './AgentListScreen'
import { useUiStore } from '@/store/ui'
import type { Agent, CliDetect } from '@/lib/api'

// W4 of agent-form-requirements: the roster's "+ Add Subagent (External)"
// disclosure probes /api/v1/system/cli-detect on mount to grey-out missing
// CLIs. The endpoint is out-of-scope for this wave — every test pins the
// fetch mock to a deterministic shape (all available by default) so the
// suite is stable whether or not the probe endpoint is wired up. Tests
// that exercise the disabled-state path override the mock via
// `makeCliDetect({ codex: false })`.
// cli-detect now goes through the authed `fetchCliDetect()` (UAT fix) rather
// than a raw `fetch`, so we mock that function instead of stubbing global fetch.
const fetchSpy = vi.fn()
vi.stubGlobal('fetch', fetchSpy)

// CliDetect (external-executor-cli-path-detection spec, FR-001) is a per-CLI
// object — `{ claude, codex, opencode }: { installed, path, source } }` —
// not the old `{ hasClaude, hasCodex, hasOpencode }` booleans. This helper
// builds a full object with sane defaults so tests can override just the
// installed flag(s) they care about.
function makeCliDetect(installed: { claude?: boolean; codex?: boolean; opencode?: boolean } = {}): CliDetect {
  const entry = (isInstalled: boolean) => ({
    installed: isInstalled,
    path: isInstalled ? '/usr/local/bin/mock-cli' : null,
    source: isInstalled ? ('path' as const) : null,
  })
  return {
    claude: entry(installed.claude ?? true),
    codex: entry(installed.codex ?? true),
    opencode: entry(installed.opencode ?? true),
  }
}

// Wave 2 — the Agents roster splits into two sections: "Base agents"
// (type !== 'worker') and "Sub-agent workers" (type === 'worker').

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    Link: ({ children, ...rest }: { children: React.ReactNode } & Record<string, unknown>) => (
      <a {...rest}>{children}</a>
    ),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn(), fetchWorkspaces: vi.fn(), updateAgent: vi.fn(), testAgentRunner: vi.fn(), fetchCliDetect: vi.fn() }
})

// CreateAgentModal pulls in heavy deps; stub it to keep the screen test focused.
vi.mock('@/components/agents/CreateAgentModal', () => ({
  CreateAgentModal: () => null,
}))

// MIN-9: AgentListScreen defers cli-detect until auth token is present.
// Stub the auth store to return a token so the useEffect fires in tests.
vi.mock('@/store/auth', () => ({
  useAuthStore: (selector: (s: { token: string | null; role: string | null; username: string | null }) => unknown) =>
    selector({ token: 'test-token', role: 'admin', username: 'admin' }),
}))

import { fetchAgents, fetchWorkspaces, fetchCliDetect } from '@/lib/api'
import type { Workspace } from '@/lib/api'

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'mia',
    name: 'Mia',
    type: 'core',
    locked: false,
    needs_model: false,
    status: 'active',
    model: 'anthropic/claude-3.5-haiku',
    description: 'Assistant',
    soul: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
    ...overrides,
  }
}

function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'ws-1',
    name: 'Alpha Workspace',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    core_team: [],
    ...overrides,
  }
}

function renderScreen() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AgentListScreen />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgents).mockReset()
  vi.mocked(fetchWorkspaces).mockResolvedValue([])
  // Default: optimistic detection — every CLI available. Tests that need a
  // missing-CLI scenario override this in their body.
  fetchSpy.mockReset()
  vi.mocked(fetchCliDetect).mockReset()
  vi.mocked(fetchCliDetect).mockResolvedValue(makeCliDetect())
})

describe('AgentListScreen — base/worker partition', () => {
  it('renders a base agent under "Main agents" and a worker under "Sub-agent workers"', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core' }),
      makeAgent({
        id: 'worker-1',
        name: 'General Worker',
        type: 'Subagent',
        executor: { kind: 'native' },
      }),
    ])
    renderScreen()

    const baseHeading = await screen.findByRole('heading', { name: /^main agents$/i })
    const workerHeading = await screen.findByRole('heading', { name: /^sub-agent workers$/i })
    expect(baseHeading).toBeInTheDocument()
    expect(workerHeading).toBeInTheDocument()

    // The worker renders under the workers section as a worker-card, the base
    // agent as a normal agent-card.
    await waitFor(() => {
      expect(screen.getByTestId('agent-card-mia')).toBeInTheDocument()
      expect(screen.getByTestId('worker-card-worker-1')).toBeInTheDocument()
    })

    // The worker card omits the default-★ "Set as default" control.
    const workerCard = screen.getByTestId('worker-card-worker-1').closest('div.relative')!
    expect(within(workerCard as HTMLElement).queryByRole('button', { name: /set .* as default/i })).not.toBeInTheDocument()
  })

  it('omits the workers section entirely when there are no workers', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    await screen.findByRole('heading', { name: /^main agents$/i })
    // Both section headers are now always rendered so the "New…" affordance
    // is reachable on a fresh install. The worker section is still in the
    // DOM (header + empty-state), not omitted.
    expect(screen.queryByRole('heading', { name: /^sub-agent workers$/i })).toBeInTheDocument()
  })

  it('shows the workers section subtitle explaining delegation-only role', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'w', name: 'W', type: 'Subagent', executor: { kind: 'native' } }),
    ])
    renderScreen()
    expect(await screen.findByText(/invoked by other agents, not chat targets/i)).toBeInTheDocument()
  })
})

// Agents-screen IA fix: two independent UAT testers stopped at the (empty)
// Main agents / Sub-agent workers cards — 100% of what a fresh install shows
// there — and concluded there was no built-in roster, because it previously
// rendered LAST, below both empty sections. Fix: Built-in roster now renders
// FIRST.
describe('AgentListScreen — Agents-screen IA: Built-in roster renders first', () => {
  it('renders the Built-in roster section BEFORE Main agents and Sub-agent workers, in DOM order', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core', locked: true }),
      makeAgent({ id: 'custom-1', name: 'Custom Main', type: 'Main' }),
      makeAgent({ id: 'worker-1', name: 'Worker One', type: 'Subagent', executor: { kind: 'native' } }),
    ])
    renderScreen()

    const builtInSection = await screen.findByTestId('built-in-agents-section')
    const baseSection = await screen.findByTestId('base-agents-section')
    const workerSection = await screen.findByTestId('worker-agents-section')

    // DOCUMENT_POSITION_FOLLOWING (4) on the result means the argument node
    // comes AFTER the node compareDocumentPosition was called on.
    expect(
      builtInSection.compareDocumentPosition(baseSection) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(
      builtInSection.compareDocumentPosition(workerSection) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    // Main agents still precedes Sub-agent workers — only the built-in
    // roster's position moved.
    expect(
      baseSection.compareDocumentPosition(workerSection) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('updates the Main-agents empty-state copy to point "above" (not "below") now that Built-in roster renders first', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core', locked: true }),
    ])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    expect(within(baseSection).getByTestId('base-agents-empty')).toHaveTextContent(
      /use a built-in above/i,
    )
    expect(within(baseSection).getByTestId('base-agents-empty')).not.toHaveTextContent(
      /use a built-in below/i,
    )
  })
})

// Per-section "New…" buttons (v0.3 worker roster split). Each section
// header carries its own affordance, and each opens the modal pre-set to
// the matching tier (createAgentModalType='custom' | 'worker') so the
// CreateAgentModal can render the right form shape.
describe('AgentListScreen — per-section New buttons', () => {
  beforeEach(() => {
    // Reset the store so the test starts with a clean modal state.
    // The store's createAgentModalType union is 'Main' | 'Subagent' | 'subagent_3p'
    // per W4 (see src/store/ui.ts:32). closeCreateAgentModal resets to 'Main'
    // so we don't need to set it explicitly here.
    act(() => { useUiStore.setState({ createAgentModalOpen: false }) })
  })

  it('shows a "+ New Main" button in the base section header', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const newBaseButton = within(baseSection).getByTestId('add-main-button')
    expect(newBaseButton).toBeInTheDocument()
    expect(newBaseButton).toHaveTextContent(/new main/i)
  })

  it('shows a "+ New Subagent" button in the worker section header', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', type: 'core' }),
      makeAgent({ id: 'w1', name: 'W1', type: 'Subagent', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    const newWorkerButton = within(workerSection).getByTestId('add-subagent-button')
    expect(newWorkerButton).toBeInTheDocument()
    expect(newWorkerButton).toHaveTextContent(/new subagent/i)
  })

  it('clicking the base + New Main button sets modal type to Main and opens the modal', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    fireEvent.click(within(baseSection).getByTestId('add-main-button'))
    const state = useUiStore.getState()
    expect(state.createAgentModalOpen).toBe(true)
    expect(state.createAgentModalType).toBe('Main')
  })

  it('clicking the worker + New Subagent button sets modal type to Subagent and opens the modal', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', type: 'core' }),
      makeAgent({ id: 'w1', name: 'W1', type: 'Subagent', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    fireEvent.click(within(workerSection).getByTestId('add-subagent-button'))
    const state = useUiStore.getState()
    expect(state.createAgentModalOpen).toBe(true)
    expect(state.createAgentModalType).toBe('Subagent')
  })

  it('does not render a top-of-page generic New Agent button', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    // The (now-removed) top-level "New Agent" button was a generic CTA
    // outside any section. The per-section buttons are the only "New…"
    // affordances and live INSIDE the base/worker sections, identified by
    // their data-testid. Asserting on the section-scoped testid count
    // (exactly one + New Main in the base section when only base agents
    // exist) proves the per-section split.
    await screen.findByTestId('base-agents-section')
    const baseSection = screen.getByTestId('base-agents-section')
    // Exactly one + New Main button in the base section.
    const newMainButtons = within(baseSection).getAllByRole('button', { name: /new main/i })
    expect(newMainButtons).toHaveLength(1)
    // And no + New Subagent button when there are no workers.
    expect(within(baseSection).queryByRole('button', { name: /new subagent/i })).not.toBeInTheDocument()
  })
})

// Per-section empty-state affordances (regression: a fresh install
// historically hid the worker section entirely, so "New worker" was
// unreachable). The fix renders both section headers (with their "+ New …"
// buttons) on every render, even when the section body is empty.
describe('AgentListScreen — empty-state per-section buttons', () => {
  beforeEach(() => {
    act(() => { useUiStore.setState({ createAgentModalOpen: false }) })
  })

  it('shows the worker section + New Subagent button even with no workers (fresh install)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    // The + New Subagent button lives in the worker section header, always
    // rendered. This is the regression: previously the entire section was
    // hidden when workerAgents.length === 0.
    const newWorkerButton = within(workerSection).getByTestId('add-subagent-button')
    expect(newWorkerButton).toBeInTheDocument()
    expect(newWorkerButton).toHaveTextContent(/new subagent/i)
  })

  it('shows the base section + New Main button even with no base agents (fresh install)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'w1', name: 'W1', type: 'Subagent', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const newBaseButton = within(baseSection).getByTestId('add-main-button')
    expect(newBaseButton).toBeInTheDocument()
    expect(newBaseButton).toHaveTextContent(/new main/i)
  })

  it('renders an empty-state message in each empty section', async () => {
    // The two-tier "fresh install" realistic case: at least one base agent
    // exists (so the page-level "No agents yet" CTA is bypassed), but
    // workers are empty. The base section renders its cards, the worker
    // section renders its empty-state message + New worker button.
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const workerSection = await screen.findByTestId('worker-agents-section')
    // No worker cards → empty-state affordance.
    expect(within(workerSection).getByTestId('worker-agents-empty')).toBeInTheDocument()
    // Base section is NOT empty here (mia is present), so no empty-state.
    expect(within(baseSection).queryByTestId('base-agents-empty')).not.toBeInTheDocument()
    // Each section still has its New button, even with the worker section empty.
    expect(within(baseSection).getByTestId('add-main-button')).toBeInTheDocument()
    expect(within(workerSection).getByTestId('add-subagent-button')).toBeInTheDocument()
  })

  it('renders the base empty-state when only workers exist (inverse fresh install)', async () => {
    // Inverse of the previous case: at least one worker exists, no base
    // agents. The base section renders its empty-state message + New agent
    // button — that button was previously unreachable in this scenario.
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'w1', name: 'W1', type: 'Subagent', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const workerSection = await screen.findByTestId('worker-agents-section')
    expect(within(baseSection).getByTestId('base-agents-empty')).toBeInTheDocument()
    expect(within(workerSection).queryByTestId('worker-agents-empty')).not.toBeInTheDocument()
    expect(within(baseSection).getByTestId('add-main-button')).toBeInTheDocument()
    expect(within(workerSection).getByTestId('add-subagent-button')).toBeInTheDocument()
  })
})

// W4 of agent-form-requirements: the roster's third "+ Add" button is now
// a disclosure that lists the 3 supported CLIs (claude-code / codex /
// opencode). Each sub-option calls openCreateAgentModal('subagent_3p', cli)
// so the wizard pre-fills the CLI choice in Step 1 and Step 3.
//
// NB: Radix Popover content is portaled to document.body (see
// src/components/ui/popover.tsx — <PopoverPrimitive.Portal>). All
// CLI menu items live in the portal, not inside the worker section's
// DOM subtree, so the assertions query `screen` (not `within(workerSection)`)
// once the disclosure is open.
describe('AgentListScreen — external CLI sub-picker disclosure', () => {
  beforeEach(() => {
    act(() => {
      useUiStore.setState({
        createAgentModalOpen: false,
        createAgentModalType: 'Main',
        createAgentModalCli: null,
      })
    })
  })

  it('renders a single "+ Add Subagent (External) ▾" trigger in the worker section', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    const trigger = within(workerSection).getByTestId('add-external-trigger')
    expect(trigger).toBeInTheDocument()
    // Disclosure is collapsed by default — the per-CLI menu items should
    // NOT be in the DOM yet (Radix Popover mounts on open).
    expect(screen.queryByTestId('add-external-claude-code')).not.toBeInTheDocument()
    expect(screen.queryByTestId('add-external-codex')).not.toBeInTheDocument()
    expect(screen.queryByTestId('add-external-opencode')).not.toBeInTheDocument()
  })

  it('clicking the trigger opens the disclosure showing all 3 CLI sub-options', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    const trigger = within(workerSection).getByTestId('add-external-trigger')
    fireEvent.click(trigger)
    // All three CLIs become visible (mock returns all-available). The Radix
    // Popover portaled content mounts to document.body so `screen.findBy*`
    // is the right scope (within(workerSection) would only see the trigger).
    await waitFor(() => {
      expect(screen.getByTestId('add-external-claude-code')).toBeInTheDocument()
      expect(screen.getByTestId('add-external-codex')).toBeInTheDocument()
      expect(screen.getByTestId('add-external-opencode')).toBeInTheDocument()
    })
  })

  it('clicking the claude-code sub-option calls openCreateAgentModal("subagent_3p", "claude-code")', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    fireEvent.click(within(workerSection).getByTestId('add-external-trigger'))
    const claude = await screen.findByTestId('add-external-claude-code')
    fireEvent.click(claude)
    const state = useUiStore.getState()
    expect(state.createAgentModalOpen).toBe(true)
    expect(state.createAgentModalType).toBe('subagent_3p')
    expect(state.createAgentModalCli).toBe('claude-code')
  })

  it('clicking the codex sub-option passes the codex CLI through to the store', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    fireEvent.click(within(workerSection).getByTestId('add-external-trigger'))
    const codex = await screen.findByTestId('add-external-codex')
    fireEvent.click(codex)
    const state = useUiStore.getState()
    expect(state.createAgentModalOpen).toBe(true)
    expect(state.createAgentModalType).toBe('subagent_3p')
    expect(state.createAgentModalCli).toBe('codex')
  })

  it('clicking the opencode sub-option passes the opencode CLI through to the store', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    fireEvent.click(within(workerSection).getByTestId('add-external-trigger'))
    const opencode = await screen.findByTestId('add-external-opencode')
    fireEvent.click(opencode)
    const state = useUiStore.getState()
    expect(state.createAgentModalOpen).toBe(true)
    expect(state.createAgentModalType).toBe('subagent_3p')
    expect(state.createAgentModalCli).toBe('opencode')
  })

  it('closeCreateAgentModal resets createAgentModalCli to null', () => {
    act(() => {
      useUiStore.setState({ createAgentModalCli: 'codex', createAgentModalOpen: true })
    })
    expect(useUiStore.getState().createAgentModalCli).toBe('codex')
    act(() => {
      useUiStore.getState().closeCreateAgentModal()
    })
    expect(useUiStore.getState().createAgentModalCli).toBeNull()
  })

  it('opens the disclosure on click and re-opens after Escape dismissal', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    const trigger = within(workerSection).getByTestId('add-external-trigger')

    // Click path (primary — Radix Popover opens via the trigger's click
    // handler on both desktop and mobile).
    fireEvent.click(trigger)
    await waitFor(() => {
      expect(screen.getByTestId('add-external-claude-code')).toBeInTheDocument()
    })

    // Dismiss with Escape (Radix Popover binds this to the trigger's
    // keydown when open — covers the keyboard path).
    fireEvent.keyDown(trigger, { key: 'Escape' })
    await waitFor(() => {
      expect(screen.queryByTestId('add-external-claude-code')).not.toBeInTheDocument()
    })

    // Re-open via the same click path to confirm the disclosure is
    // idempotent across open/close cycles.
    fireEvent.click(trigger)
    await waitFor(() => {
      expect(screen.getByTestId('add-external-claude-code')).toBeInTheDocument()
    })
  })

  it('renders the codex sub-option as disabled with a tooltip when hostClis.codex.installed is false', async () => {
    // Override the probe to report codex missing.
    vi.mocked(fetchCliDetect).mockReset()
    vi.mocked(fetchCliDetect).mockResolvedValue(makeCliDetect({ codex: false }))
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    // Wait for the authed probe to settle before asserting (the useEffect is async).
    await waitFor(() => {
      expect(fetchCliDetect).toHaveBeenCalled()
    })
    fireEvent.click(within(workerSection).getByTestId('add-external-trigger'))
    const codex = await screen.findByTestId('add-external-codex')
    expect(codex).toBeDisabled()
    expect(codex).toHaveAttribute('title', expect.stringMatching(/codex/i))
    // The other two CLIs remain enabled.
    const claude = screen.getByTestId('add-external-claude-code')
    const opencode = screen.getByTestId('add-external-opencode')
    expect(claude).not.toBeDisabled()
    expect(opencode).not.toBeDisabled()
  })

  it('keeps optimistic defaults when the cli-detect endpoint is missing and surfaces a warning', async () => {
    // Endpoint failure → optimistic defaults → all CLIs enabled, plus a warning.
    vi.mocked(fetchCliDetect).mockReset()
    vi.mocked(fetchCliDetect).mockRejectedValue(new Error('not found'))
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    fireEvent.click(within(workerSection).getByTestId('add-external-trigger'))
    await waitFor(() => {
      expect(screen.getByTestId('add-external-codex')).toBeInTheDocument()
    })
    expect(screen.getByTestId('add-external-codex')).not.toBeDisabled()
    expect(screen.getByTestId('cli-detect-warning')).toBeInTheDocument()
  })
})

// Two-tab view (Agents library + Workspace Teams).
describe('AgentListScreen — two-tab view', () => {
  it('renders both tab triggers: "Agents" and "Workspace Teams"', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    renderScreen()
    // Tabs render immediately (no async wait needed; they're part of the static shell).
    await screen.findByTestId('agents-tab-library')
    expect(screen.getByTestId('agents-tab-library')).toBeInTheDocument()
    expect(screen.getByTestId('agents-tab-teams')).toBeInTheDocument()
  })

  it('shows the agents library by default (library tab is active on mount)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    renderScreen()
    // The base-agents-section is in the library tab content which is visible by default.
    await screen.findByTestId('base-agents-section')
    expect(screen.getByTestId('base-agents-section')).toBeInTheDocument()
  })

  it('switching to the Workspace Teams tab shows the teams panel and hides the library roster', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-alpha', name: 'Alpha' }),
    ])
    renderScreen()
    // Wait for agents to load (confirms the library tab content is ready).
    await screen.findByTestId('base-agents-section')
    // Radix Tabs triggers on mousedown + click — fire both events.
    const teamsTab = screen.getByTestId('agents-tab-teams')
    fireEvent.mouseDown(teamsTab)
    fireEvent.click(teamsTab)
    // The workspace team row becomes visible once the workspaces query resolves.
    await screen.findByTestId('workspace-team-row-ws-alpha')
    expect(screen.getByTestId('workspace-team-row-ws-alpha')).toBeInTheDocument()
    // The roster sections are no longer visible (library tab is inactive).
    expect(screen.queryByTestId('base-agents-section')).not.toBeInTheDocument()
  })

  it('workspace team rows link to /workspaces/$workspaceId/team', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-beta', name: 'Beta' }),
    ])
    renderScreen()
    await screen.findByTestId('base-agents-section')
    const teamsTab = screen.getByTestId('agents-tab-teams')
    fireEvent.mouseDown(teamsTab)
    fireEvent.click(teamsTab)
    const row = await screen.findByTestId('workspace-team-row-ws-beta')
    // The row is an anchor (Link) element — navigates to the workspace team tab.
    // The router mock renders Link as <a {...rest}> so `to` passes as an attribute.
    expect(row.tagName.toLowerCase()).toBe('a')
    expect(row).toHaveAttribute('to', '/workspaces/$workspaceId/team')
  })

  it('shows empty state when there are no workspaces in the teams tab', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    renderScreen()
    await screen.findByTestId('base-agents-section')
    const teamsTab = screen.getByTestId('agents-tab-teams')
    fireEvent.mouseDown(teamsTab)
    fireEvent.click(teamsTab)
    // The teams tab content renders; empty state appears once workspaces query resolves.
    await screen.findByText(/no workspaces yet/i)
    expect(screen.getByText(/no workspaces yet/i)).toBeInTheDocument()
  })
})

// Library workspace filter (filter by workspace team membership).
describe('AgentListScreen — library filter by workspace', () => {
  it('renders the workspace filter trigger when workspaces exist', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha' }),
    ])
    renderScreen()
    await screen.findByTestId('workspace-filter-trigger')
    expect(screen.getByTestId('workspace-filter-trigger')).toBeInTheDocument()
  })

  it('filter menu lists all workspaces and an "All agents" option', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha' }),
      makeWorkspace({ id: 'ws-2', name: 'Beta' }),
    ])
    renderScreen()
    const trigger = await screen.findByTestId('workspace-filter-trigger')
    fireEvent.click(trigger)
    // The filter menu opens — Radix Popover portals to body.
    await waitFor(() => {
      expect(screen.getByTestId('workspace-filter-all')).toBeInTheDocument()
      expect(screen.getByTestId('workspace-filter-ws-1')).toBeInTheDocument()
      expect(screen.getByTestId('workspace-filter-ws-2')).toBeInTheDocument()
    })
  })

  it('filtering by workspace shows only agents whose id is in that workspace core_team', async () => {
    const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core', locked: true })
    const jim = makeAgent({ id: 'jim', name: 'Jim', type: 'core', locked: true })
    vi.mocked(fetchAgents).mockResolvedValue([mia, jim])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha', core_team: ['mia'] }),
    ])
    renderScreen()
    const trigger = await screen.findByTestId('workspace-filter-trigger')
    fireEvent.click(trigger)
    const wsOption = await screen.findByTestId('workspace-filter-ws-1')
    fireEvent.click(wsOption)
    // After filtering: mia's card is visible; jim's card is not.
    await waitFor(() => {
      expect(screen.getByTestId('agent-card-mia')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('agent-card-jim')).not.toBeInTheDocument()
  })

  it('clear filter button resets to all agents', async () => {
    const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core', locked: true })
    const jim = makeAgent({ id: 'jim', name: 'Jim', type: 'core', locked: true })
    vi.mocked(fetchAgents).mockResolvedValue([mia, jim])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha', core_team: ['mia'] }),
    ])
    renderScreen()
    // Apply the filter.
    const trigger = await screen.findByTestId('workspace-filter-trigger')
    fireEvent.click(trigger)
    fireEvent.click(await screen.findByTestId('workspace-filter-ws-1'))
    // Wait for filter to apply.
    await waitFor(() => {
      expect(screen.queryByTestId('agent-card-jim')).not.toBeInTheDocument()
    })
    // Click the clear button.
    const clearBtn = screen.getByTestId('workspace-filter-clear')
    fireEvent.click(clearBtn)
    // Both agents should now be visible (after Accordion expands the built-in roster).
    await waitFor(() => {
      expect(screen.queryByTestId('workspace-filter-clear')).not.toBeInTheDocument()
    })
  })
})
