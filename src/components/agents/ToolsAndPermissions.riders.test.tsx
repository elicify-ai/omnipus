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
// CHECK round 1 findings 9 and 10 (W4):
//   - S-76 now copies the scenario's exact Given — God Mode on, GLOBAL
//     send_email Ask, MIA'S OWN value Allow — and expects the effective
//     Allow (without the God floor, strictest-wins shows Ask). Two negative
//     controls pin C-0/C-26: the agent's OWN Ask and OWN Deny stay shown
//     under God Mode, because God floors the global side only. The named
//     fetch call is no longer an oracle; the effective readout is.
//   - S-78 keeps the REAL useAutoSave (only the transport is mocked) and ONE
//     QueryClient across close/reopen (app lifetime). The controlled
//     transport stores the change ONLY after the exact save payload is
//     accepted; the acknowledged save is asserted BEFORE the unmount; the
//     reopen reads the server through fetchAgentTools while the STALE
//     pre-save props still say Ask, so only a persisted write can make the
//     reopened row show Allow. The row is the scenario's send_message.
//
// RED today: S-76's own-Ask/own-Deny controls show the God-lifted value
// where the spec preserves the agent's own value (or the tab never consults
// God Mode at all — the stored value shows where S-76 requires the effective
// Allow); S-78's save never carries the exact payload through the real
// autosave path, so the exact-payload/acknowledged assertions fail.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { RegistryTool, AgentToolsCfg } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchProviders: vi.fn(),
  fetchSkills: vi.fn().mockResolvedValue([]),
  fetchGodMode: vi.fn(),
  isApiError: vi.fn(() => false),
}))

// CHECK round 1 finding 10 / W4: the useAutoSave mock is GONE — the real
// autosave hook runs, and only the transport (updateAgentTools) is doubled.

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
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {}, skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}))
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

const SEND_MESSAGE: RegistryTool = {
  ...SEND_EMAIL,
  name: 'send_message',
  description: 'Send a chat message.',
}

const cfg = (policies: Record<string, 'allow' | 'ask' | 'deny'>): AgentToolsCfg => ({
  builtin: { policies },
})

const revision = '0'.repeat(64)

// ONE QueryClient per test, shared across close/reopen (S-78's app
// lifetime). A fresh client per mount would erase the cache the rider
// guards (CHECK round 1 finding 10).
function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
}

function wrapperFor(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
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
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([SEND_EMAIL, SEND_MESSAGE])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([SEND_EMAIL, SEND_MESSAGE])
  vi.mocked(api.fetchAgentTools).mockResolvedValue({
    revision,
    override_names: [],
    config: cfg({ send_email: 'ask' }),
    tools: [],
    skills: [],
  })
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  // Runtime God Mode: enabled (S-76's Given). The tab must consult it —
  // fetchGodMode reads the live runtime state ({ enabled, available,
  // supported }) per src/lib/api/security.ts.
  vi.mocked(api.fetchGodMode).mockResolvedValue({ enabled: true, persisted: true, available: true, supported: true })
})

describe('Rider #1297 (S-76): the Tools tab shows what the run does under God Mode', () => {
  it('with the exact Given (God on, global Ask, own Allow) the row shows the effective Allow', async () => {
    // S-76's Given, copied exactly: global send_email Ask, Mia's OWN Allow.
    // Strictest-wins alone would show Ask; only the God floor of the GLOBAL
    // side lets the effective value be Allow.
    permissions().stored.policies = { send_email: 'ask' }
    // The editor hydrates from the dedicated GET — it must carry the same
    // own value the props do (CHECK finding 9: the fixture states the Given).
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: cfg({ send_email: 'allow' }),
      tools: [],
      skills: [],
    })
    const client = makeQueryClient()
    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg({ send_email: 'allow' })} onChange={() => {}} />,
      { wrapper: wrapperFor(client) },
    )
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const row = await screen.findByTestId('tool-row-send_email')
    // The effective readout is the oracle (CHECK finding 9: no named-fetch
    // assertion) — it can only be Allow if the tab consulted the live God
    // Mode state.
    expect(pressedPolicy(row)).toBe('allow')
  })

  it('control: the same values with God Mode OFF show Ask — only the floor can show Allow', async () => {
    // The discriminator for "matching what the run does": identical stored
    // values, God off. Strictest-wins (global ask × own allow) is Ask. A
    // readout that never consults the runtime God Mode state shows Allow in
    // BOTH legs and fails here.
    permissions().stored.policies = { send_email: 'ask' }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: cfg({ send_email: 'allow' }),
      tools: [],
      skills: [],
    })
    vi.mocked(api.fetchGodMode).mockResolvedValue({ enabled: false, persisted: true, available: true, supported: true })
    const client = makeQueryClient()
    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg({ send_email: 'allow' })} onChange={() => {}} />,
      { wrapper: wrapperFor(client) },
    )
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const row = await screen.findByTestId('tool-row-send_email')
    expect(pressedPolicy(row)).toBe('ask')
  })

  it('control: the agent OWN Ask is preserved under God Mode (God floors the global side only)', async () => {
    permissions().stored.policies = { send_email: 'ask' }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: cfg({ send_email: 'ask' }),
      tools: [],
      skills: [],
    })
    const client = makeQueryClient()
    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg({ send_email: 'ask' })} onChange={() => {}} />,
      { wrapper: wrapperFor(client) },
    )
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const row = await screen.findByTestId('tool-row-send_email')
    expect(pressedPolicy(row)).toBe('ask')
  })

  it('control: the agent OWN Deny is preserved under God Mode', async () => {
    permissions().stored.policies = { send_email: 'ask' }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision,
      override_names: [],
      config: cfg({ send_email: 'deny' }),
      tools: [],
      skills: [],
    })
    const client = makeQueryClient()
    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg({ send_email: 'deny' })} onChange={() => {}} />,
      { wrapper: wrapperFor(client) },
    )
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const row = await screen.findByTestId('tool-row-send_email')
    expect(pressedPolicy(row)).toBe('deny')
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

    const client = makeQueryClient()
    render(
      <ToolsAndPermissions agentId="agent-mia" agentType="Main" tools={cfg(policies)} onChange={() => {}} />,
      { wrapper: wrapperFor(client) },
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
  it('a reopen shows the saved value because the autosave carried it, never a stale pre-save cache', async () => {
    const user = userEvent.setup()
    // Server truth: Ask until the controlled transport ACCEPTS the exact
    // save payload (CHECK finding 10: only a persisted write moves the
    // server).
    let serverPolicies: Record<string, 'allow' | 'ask' | 'deny'> = { send_message: 'ask' }
    const serverResponse = () => ({
      revision,
      override_names: [],
      config: cfg(serverPolicies),
      tools: [],
      skills: [],
    })
    vi.mocked(api.fetchAgentTools).mockImplementation(() => Promise.resolve(serverResponse()))
    const expectedPut = {
      revision, override_names: ['send_message'], config: { builtin: { policies: { send_message: 'allow' } } },
    }
    vi.mocked(api.updateAgentTools).mockImplementation(async (agentId, request) => {
      expect(agentId).toBe('agent-mia')
      expect(request).toEqual(expectedPut)
      serverPolicies = { ...request.config?.builtin?.policies }
      return serverResponse()
    })

    // ONE client for the whole rider — close and reopen share it.
    const client = makeQueryClient()
    const wrapper = wrapperFor(client)
    // STALE props on BOTH mounts: only the fetched server state can show
    // Allow, so the preordained-props bypass is closed.
    const staleProps = { agentId: 'agent-mia', agentType: 'Main' as const, tools: cfg({ send_message: 'ask' }), onChange: () => {} }

    const { unmount } = render(<ToolsAndPermissions {...staleProps} />, { wrapper })
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const row = await screen.findByTestId('tool-row-send_message')
    expect(pressedPolicy(row)).toBe('ask')

    // The person sets the value to Allow; the REAL autosave carries it.
    await user.click(within(row).getByRole('button', { name: /^allow$/i }))
    // The save request with the EXACT payload happens and is acknowledged
    // BEFORE the editor closes (CHECK finding 10: no silent unsaved close).
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledTimes(1)
    }, { timeout: 3000 })
    const [, sentRequest] = vi.mocked(api.updateAgentTools).mock.calls[0]
    expect(sentRequest).toEqual(expectedPut)
    await waitFor(() => {
      expect(serverPolicies.send_message).toBe('allow')
    }, { timeout: 3000 })

    unmount()

    // Reopen WITHOUT a page reload, on the SAME client, with the STALE
    // props: the editor hydrates from the server, whose state only moved
    // because the save was accepted.
    render(<ToolsAndPermissions {...staleProps} />, { wrapper })
    await screen.findByTestId('category-pill-communication')
    await openSection(/communication/i)
    const reopened = await screen.findByTestId('tool-row-send_message')
    await waitFor(() => {
      expect(pressedPolicy(reopened)).toBe('allow')
    })
  })
})
