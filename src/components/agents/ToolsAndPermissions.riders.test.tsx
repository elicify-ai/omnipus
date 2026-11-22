// ToolsAndPermissions.riders.test.tsx — order 32 rider tests (S-76 tab half,
// S-77, S-78). Oracle: skills-tools-redesign-spec scenarios S-76/S-77/S-78
// (riders #1297/#1298/#1411). The get_agent halves of S-76/S-77 are pinned on
// the Go side by pkg/gateway/wu3_r3_god_mode_wiring_test.go (production God
// Mode wiring).
//
// Mount: the real ToolsAndPermissions tab. Collapsed category/server
// sections are opened the way a person does — the section's disclosure
// control is found by role and name and clicked (team-lead ruling, part 4).
// The tab's rows live in ToolPolicyEditor sections; a section trigger's
// accessible name is "<Category label><summary pill>" (e.g.
// "CommunicationAsk", observed in the rendered DOM).
//
// RED today: nothing on the tab path consults the runtime God Mode state
// (fetchGodMode, src/lib/api/security.ts), so the row keeps showing the
// stored value where S-76 requires "effective Allow … matching what the run
// does" (God Mode floors the policy to allow — Dataset D12 row 12 traces the
// God floor to S-76).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { RegistryTool, AgentToolsCfg } from '@/lib/api'

vi.mock('@/lib/api', () => ({
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchGlobalToolPolicies: vi.fn(),
  fetchProviders: vi.fn(),
  fetchSkills: vi.fn().mockResolvedValue([]),
  fetchGodMode: vi.fn(),
  isApiError: vi.fn(() => false),
}))

vi.mock('@/hooks/useAutoSave', () => ({
  useAutoSave: vi.fn(() => ({ status: 'idle', error: undefined, lastSavedAt: undefined, saveNow: vi.fn() })),
}))

vi.mock('@/components/ui/AutoSaveIndicator', () => ({
  AutoSaveIndicator: () => null,
}))

vi.mock('./MCPServerPicker', () => ({
  MCPServerPicker: () => null,
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

import * as api from '@/lib/api'
import { ToolsAndPermissions } from './ToolsAndPermissions'

const SEND_EMAIL: RegistryTool = {
  name: 'send_email',
  scope: 'general',
  category: 'communication',
  description: 'Send an email.',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}

const cfg = (policies: Record<string, 'allow' | 'ask' | 'deny'>): AgentToolsCfg => ({
  builtin: { policies },
})

const revision = '0'.repeat(64)

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeQueryClient()}>{children}</QueryClientProvider>
}

// The row's currently-shown value: the PolicyBadge with aria-pressed=true.
function pressedPolicy(row: HTMLElement): string | null {
  for (const name of ['allow', 'ask', 'deny']) {
    const badge = within(row).getByRole('button', { name: new RegExp(`^${name}$`, 'i') })
    if (badge.getAttribute('aria-pressed') === 'true') return name
  }
  return null
}

// Open a collapsed section the way a person does: its disclosure control by
// role and name, then click. The trigger only exists once the tab's queries
// have resolved, so the caller awaits a rendered marker first.
async function openSection(nameMarker: RegExp) {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: nameMarker }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([SEND_EMAIL])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([SEND_EMAIL])
  vi.mocked(api.fetchAgentTools).mockResolvedValue({
    revision,
    override_names: [],
    config: cfg({ send_email: 'ask' }),
    tools: [],
    skills: [],
  })
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({ policies: {} })
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  // Runtime God Mode: enabled (S-76's Given). The tab must consult it —
  // fetchGodMode reads the live runtime state ({ enabled, available,
  // supported }) per src/lib/api/security.ts.
  vi.mocked(api.fetchGodMode).mockResolvedValue({ enabled: true, persisted: true, available: true, supported: true })
})

describe('Rider #1297 (S-76): the Tools tab shows what the run does under God Mode', () => {
  it('consults the live God Mode state and shows the effective Allow the run uses', async () => {
    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg({ send_email: 'ask' })} onChange={() => {}} />,
      { wrapper },
    )
    // The section trigger renders once the queries resolve.
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const row = await screen.findByTestId('tool-row-send_email')
    // A readout that never asks what the runtime does cannot promise
    // "matching what the run does" (S-76's Then). God Mode is on; the run
    // allows; the row must show the effective Allow, not the stored Ask.
    expect(api.fetchGodMode).toHaveBeenCalled()
    expect(pressedPolicy(row)).toBe('allow')
  })
})

describe('Rider #1298 (S-77): assigned service tools are listed with their values', () => {
  it('lists all five assigned github service tools, each with its value', async () => {
    const names = ['mcp_github_search', 'mcp_github_issues', 'mcp_github_pulls', 'mcp_github_repos', 'mcp_github_gists']
    const githubTools: RegistryTool[] = names.map(name => ({
      ...SEND_EMAIL,
      name,
      description: `${name} via MCP`,
      source: 'mcp',
      category: 'web',
    }))
    vi.mocked(api.fetchRegistryTools).mockResolvedValue(githubTools)
    vi.mocked(api.fetchBuiltinTools).mockResolvedValue(githubTools)
    // Mia is assigned service `github` (S-77's Given); its tools carry
    // server_id so the grouping is unambiguous.
    vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([
      { id: 'svc-github', name: 'github' },
    ] as never)
    const policies: Record<string, 'allow' | 'ask' | 'deny'> = {}
    for (const n of names) policies[n] = 'allow'
    // The agent's stored values hydrate editorValue from the dedicated GET
    // response (ToolsAndPermissions hydrates from fetchAgentTools), so the
    // assigned values live there.
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: cfg(policies),
      tools: [],
      skills: [],
    })

    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg(policies)} onChange={() => {}} />,
      { wrapper },
    )
    await screen.findByTestId('tool-policy-editor')
    // MCP tools group under their server's section; open it like a person.
    await openSection(/github/i)
    for (const n of names) {
      const row = await screen.findByTestId(`tool-row-${n}`)
      expect(pressedPolicy(row)).toBe('allow')
    }
  })
})

describe('Rider #1411 (S-78): reopening the editor shows the saved value without a page reload', () => {
  it('a reopen shows the saved value, never a stale pre-save cache', async () => {
    const user = userEvent.setup()
    // Server truth before the change: Ask; the person saves Allow, so the
    // server afterwards holds Allow (the mock keeps returning saved state).
    vi.mocked(api.fetchAgentTools)
      .mockResolvedValueOnce({ revision, override_names: [], config: cfg({ send_email: 'ask' }), tools: [], skills: [] })
      .mockResolvedValue({ revision, override_names: [], config: cfg({ send_email: 'allow' }), tools: [], skills: [] })
    vi.mocked(api.updateAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: cfg({ send_email: 'allow' }),
      tools: [],
      skills: [],
    })

    const { unmount } = render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg({ send_email: 'ask' })} onChange={() => {}} />,
      { wrapper },
    )
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const row = await screen.findByTestId('tool-row-send_email')
    expect(pressedPolicy(row)).toBe('ask')

    // The person sets the value to Allow (the local change the save carries).
    await user.click(within(row).getByRole('button', { name: /^allow$/i }))
    unmount()

    // Reopen WITHOUT a page reload: the editor shows the saved value.
    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg({ send_email: 'allow' })} onChange={() => {}} />,
      { wrapper },
    )
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const reopened = await screen.findByTestId('tool-row-send_email')
    await waitFor(() => {
      expect(pressedPolicy(reopened)).toBe('allow')
    })
  })
})
