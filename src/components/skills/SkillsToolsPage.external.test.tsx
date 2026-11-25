// SkillsToolsPage.external.test.tsx — the External tools tab through the imported
// page, with the network edge doubled. Written in Skills & Tools fix round 4:
//
//   CHECK round 4, candidate 11 — the five retired SecuritySection cases "MCP
//   tools in GlobalToolPoliciesSection" (rows listed per service, kept out of the
//   built-in grid, and a Deny click saving the real tool key). Their replacement:
//     S-03 / User Story 1 AS3: the External tools tab lists a service's tools under
//         its own group, and a row's choice saves "actual tool names";
//     S-45: the tabs split the registry — a service's tools are External tools,
//         not Omnipus tools;
//     C-2: a save names only the clicked tool's own key.
//
//   CHECK round 4, candidate 21 — the Add-service UI to later-tool inheritance
//   path (the Go rows cover the server's key, not what the person sees):
//     S-59 / User Story 8 AS1 / C-7: after Add service every tool of the service
//         shows Ask, and a tool it reports later shows Ask too;
//     C-18 (last paragraph) / User Story 8 AS1: under a broader global rule such as
//         `mcp_*: deny`, nothing is written and the rows show that rule's value;
//     S-04: every row shows exactly one of Allow, Ask, Deny and "Unset" appears
//         nowhere.
//
//   Fix round 4, Part C item 3 — the service groups, ported from the retired
//   SkillsScreen MCP tab (SkillsScreen.missing-secret-1277.test.tsx, the "MCP empty
//   state is the kit Empty" case of SkillsScreen.copy-dialog.test.tsx, and the G7
//   describe of McpServerModal.test.tsx); spec line 72 and FR-065 retire that screen:
//     S-61 / User Story 8 AS3 / FR-046 (#1277): a service that cannot connect shows
//         its exact reason inside its own group with Retry and Edit; a connected
//         sibling shows none;
//     UI States table (External tools tab, Partial): a service that is not connected
//         says "Not connected" in its header (the old card's "error" badge);
//     User Story 8 AS2 / FR-046 (G7): the group header carries Test; pressing it tests
//         that service and a toast reports the result. The toast wording is not in
//         the spec, so the test pins the service's own message and the toast's kind
//         (the old exact-equality on the whole message belonged to the old toast);
//     UI States table (External tools tab, Empty): "No external services yet" with
//         "Add service", drawn as the kit Empty (CHECK round 4 finding F3).
//
// The service's own effect is the documented server behaviour (C-7: Add service
// writes `mcp_<S>_*` = ask unless a global key already covers the service, C-18
// "covered" decided by the resolver — here the one broader rule the scenarios use,
// `mcp_*`). The Go test TestMcpAdd_WritesAskWildcard is that server contract's
// proof; this file proves only what the page shows from it. The permissions
// double rejects every request except GET and PATCH /security/permissions.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { McpServer, RegistryTool } from '@/lib/api'
import type { ComponentType } from 'react'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec S-03, S-04, S-45, S-59 and C-2, C-7, C-18',
  ).toContain('/src/components/skills/SkillsToolsPage.tsx')
  const mod = (await pageModules['/src/components/skills/SkillsToolsPage.tsx']()) as Record<string, unknown>
  expect(typeof mod.SkillsToolsPage, 'BLOCKED: SkillsToolsPage public export').toBe('function')
  return mod.SkillsToolsPage as ComponentType
}

let SkillsToolsPage: ComponentType
beforeEach(async () => {
  SkillsToolsPage = await requireSkillsToolsPage()
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn(),
    fetchRegistryTools: vi.fn(),
    fetchBuiltinTools: vi.fn(),
    fetchProviders: vi.fn(),
    fetchSkillMarketplaceStatus: vi.fn(),
    searchSkills: vi.fn(),
    fetchMcpServers: vi.fn(),
    addMcpServer: vi.fn(),
    testMcpServer: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { expectPermissionPatches, useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

const permissions = useSkillPermissionNetwork(() => ({
  policies: { send_email: 'ask' },
  skill_policies: {},
  visibility: { send_email: 'full' },
  skill_visibility: {},
  skill_trust: 'ask',
}))

function makeTool(name: string, overrides: Partial<RegistryTool> = {}): RegistryTool {
  return {
    name,
    description: `${name} description`,
    scope: 'general',
    category: 'communication',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
    ...overrides,
  }
}
const SEND_EMAIL = makeTool('send_email')
// ToolRegistryEntry.server_id (contract) and the spec's Edge Cases ("An external
// service whose id contains underscores: its Ask key uses the id the registry
// reports, never a prefix parsed from tool names"): a service's tools carry the
// id of their service.
function serviceTools(server: string, names: string[]): RegistryTool[] {
  return names.map((name) => makeTool(`mcp_${server}_${name}`, { category: 'search', source: 'mcp', server_id: server }))
}
function service(id: string, tools: RegistryTool[]): McpServer {
  return {
    id, name: id, transport: 'sse', status: 'connected', enabled: true,
    tool_count: tools.length, tools: tools.map((tool) => tool.name),
  }
}

// What the connected services currently report, as the server would answer.
const world: { servers: McpServer[]; tools: RegistryTool[] } = { servers: [], tools: [] }

function pageWrapper({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.testMcpServer).mockReset()
  useUiStore.setState({ toasts: [] })
  world.servers = []
  world.tools = [SEND_EMAIL]
  vi.mocked(api.fetchSkills).mockResolvedValue([])
  vi.mocked(api.fetchRegistryTools).mockImplementation(async () => structuredClone(world.tools))
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockImplementation(async () => structuredClone(world.servers))
})

function pressedPolicies(row: HTMLElement): string[] {
  return ['allow', 'ask', 'deny'].filter(
    (name) => within(row).getByRole('button', { name: new RegExp(`^${name}$`, 'i') }).getAttribute('aria-pressed') === 'true',
  )
}

async function openTab(name: RegExp) {
  await userEvent.setup().click(await screen.findByRole('tab', { name }))
}

async function openGroup(label: RegExp) {
  const trigger = await waitFor(
    () => {
      const found = screen.getAllByRole('button', { name: label }).find((button) => button.hasAttribute('aria-expanded'))
      expect(found, `the group disclosure named ${label} is present`).toBeDefined()
      return found as HTMLElement
    },
    { timeout: 3000 },
  )
  if (trigger.getAttribute('aria-expanded') === 'false') await userEvent.setup().click(trigger)
}

// Opens every collapsed group disclosure of the visible tab (menu triggers, which
// carry aria-haspopup, are not group disclosures).
async function expandEveryGroup() {
  const user = userEvent.setup()
  for (const trigger of screen.getAllByRole('button')) {
    if (trigger.getAttribute('aria-expanded') === 'false' && !trigger.hasAttribute('aria-haspopup')) await user.click(trigger)
  }
}

const NOTION_TOOLS = ['search', 'fetch', 'create_page', 'update_page']
const NOTION_LATER_TOOL = 'archive_page'

// The person adds service `notion` through the page: Add service, the name, a
// network address, Add. The double answers as the server does (C-7, C-18).
async function addNotionThroughThePage() {
  const user = userEvent.setup()
  vi.mocked(api.addMcpServer).mockImplementation(async (data) => {
    world.tools = [SEND_EMAIL, ...serviceTools(data.name, NOTION_TOOLS)]
    world.servers = [service(data.name, serviceTools(data.name, NOTION_TOOLS))]
    const covered = 'mcp_*' in permissions().stored.policies // C-18: a broader rule already covers the service
    if (!covered) {
      permissions().stored.policies[`mcp_${data.name}_*`] = 'ask' // C-7
      permissions().stored.visibility[`mcp_${data.name}_*`] = 'full'
    }
    return world.servers[0]
  })
  await openTab(/external tools/i)
  await user.click(await screen.findByRole('button', { name: 'Add service' }))
  await user.type(await screen.findByPlaceholderText('my-mcp-server'), 'notion')
  await user.type(screen.getByTestId('network-url'), 'https://mcp.example.com/sse')
  await user.click(screen.getByTestId('submit-add'))
  await waitFor(() => expect(api.addMcpServer).toHaveBeenCalledTimes(1), { timeout: 3000 })
  expect(vi.mocked(api.addMcpServer).mock.calls[0][0]).toMatchObject({ name: 'notion' })
}

function rowOf(toolName: string): HTMLElement {
  return screen.getByTestId(`tool-row-${toolName}`)
}

describe('SkillsToolsPage — External tools tab', () => {
  it('S-03 / S-45 / C-2: a service lists under External tools and not Omnipus tools, and Deny on one row saves that tool\'s own key', async () => {
    world.tools = [SEND_EMAIL, ...serviceTools('testsvr', ['list', 'read'])]
    world.servers = [service('testsvr', serviceTools('testsvr', ['list', 'read']))]
    permissions().stored.policies['mcp_testsvr_*'] = 'ask'
    permissions().stored.visibility['mcp_testsvr_*'] = 'full'
    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    // The service's tools are rows of its own group on the External tools tab.
    await openTab(/external tools/i)
    await openGroup(/^testsvr/i)
    const list = await screen.findByTestId('tool-row-mcp_testsvr_list')
    expect(pressedPolicies(list), 'S-04: exactly one choice is selected').toEqual(['ask'])
    expect(pressedPolicies(rowOf('mcp_testsvr_read'))).toEqual(['ask'])
    expect(screen.queryByText(/unset/i), 'S-04: "Unset" appears nowhere').toBeNull()

    // S-45: the Omnipus tools tab does not hold them, even with every group open.
    await openTab(/omnipus tools/i)
    await expandEveryGroup()
    expect(screen.getByTestId('tool-row-send_email'), 'control: opening the built-in groups does show built-in rows').toBeInTheDocument()
    expect(screen.queryByTestId('tool-row-mcp_testsvr_list')).toBeNull()
    expect(screen.queryByTestId('tool-row-mcp_testsvr_read')).toBeNull()

    // C-2: Deny on one row sends that tool's key alone; its sibling is untouched.
    await openTab(/external tools/i)
    await openGroup(/^testsvr/i)
    const clicked = rowOf('mcp_testsvr_list')
    await userEvent.setup().click(within(clicked).getByRole('button', { name: /^deny$/i }))
    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument(), { timeout: 3000 })
    expect(pressedPolicies(clicked)).toEqual(['deny'])
    expect(pressedPolicies(rowOf('mcp_testsvr_read'))).toEqual(['ask'])
    expectPermissionPatches(permissions(), { policies: { mcp_testsvr_list: 'deny' } })
  })

  it('S-59 / C-7: after Add service every tool of the service shows Ask, and a tool it reports later shows Ask too', async () => {
    const { unmount } = render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await addNotionThroughThePage()

    await openGroup(/^notion/i)
    for (const name of NOTION_TOOLS) {
      const row = await screen.findByTestId(`tool-row-mcp_notion_${name}`)
      expect(pressedPolicies(row), `S-59: ${name} shows Ask`).toEqual(['ask'])
    }
    expect(screen.queryByText(/unset/i), 'S-04: "Unset" appears nowhere').toBeNull()

    // The service later reports a fifth tool; the person comes back to the page.
    unmount()
    world.tools = [SEND_EMAIL, ...serviceTools('notion', [...NOTION_TOOLS, NOTION_LATER_TOOL])]
    world.servers = [service('notion', serviceTools('notion', [...NOTION_TOOLS, NOTION_LATER_TOOL]))]
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/external tools/i)
    await openGroup(/^notion/i)
    const later = await screen.findByTestId(`tool-row-mcp_notion_${NOTION_LATER_TOOL}`)
    expect(pressedPolicies(later), 'S-59: the later tool also shows Ask').toEqual(['ask'])
    for (const name of NOTION_TOOLS) expect(pressedPolicies(rowOf(`mcp_notion_${name}`))).toEqual(['ask'])

    // The page wrote nothing: the one key is the server's own (C-7), no per-tool key.
    expect(permissions().stored.policies).toEqual({ send_email: 'ask', 'mcp_notion_*': 'ask' })
    expectPermissionPatches(permissions())
  })

  it('C-18 / User Story 8 AS1: under a broader global rule mcp_* Deny the new service\'s rows show Deny and nothing is written', async () => {
    permissions().stored.policies['mcp_*'] = 'deny'
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await addNotionThroughThePage()

    await openGroup(/^notion/i)
    for (const name of NOTION_TOOLS) {
      const row = await screen.findByTestId(`tool-row-mcp_notion_${name}`)
      expect(pressedPolicies(row), `C-18: ${name} shows the broader rule's value`).toEqual(['deny'])
    }
    // Instrument: the double wrote no service key because the broader rule covers it,
    // exactly as C-18 says, and the page sent nothing of its own.
    expect(permissions().stored.policies).toEqual({ send_email: 'ask', 'mcp_*': 'deny' })
    expectPermissionPatches(permissions())
  })
})

// The group of one service on the visible tab (the catalog gives it the id mcp:<service>).
function groupOf(serverId: string): HTMLElement {
  const group = document.querySelector<HTMLElement>(`[data-group-id="mcp:${serverId}"]`)
  if (!group) throw new Error(`Instrument: no group for service ${serverId}`)
  return group
}

// #1277 (founder 2026-10-12; accepted CHECK F3): the diagnostic must be visible
// when a person opens the services, not just a bare error badge.
const MISSING_REASON = 'Secret reference QA1277_UI_MCP_TOKEN is missing from the credential store.'
const BROKEN: McpServer = {
  id: 'qa-broken-connector', name: 'Broken connector', transport: 'http', enabled: true,
  status: 'error', status_reason: MISSING_REASON, tool_count: 0,
}
const HEALTHY: McpServer = {
  id: 'qa-healthy-connector', name: 'Healthy connector', transport: 'http', enabled: true,
  status: 'connected', tool_count: 1, tools: ['qa_probe'],
}

describe('SkillsToolsPage — External tools tab, service groups', () => {
  it('S-61 / #1277: the exact missing-secret reason shows inside the failing service group with Retry and Edit, and nowhere else', async () => {
    world.servers = [BROKEN, HEALTHY]
    world.tools = [SEND_EMAIL, ...serviceTools(HEALTHY.id, ['qa_probe'])]
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/external tools/i)
    await openGroup(/^qa-broken-connector/)
    await openGroup(/^qa-healthy-connector/)

    const failing = groupOf(BROKEN.id)
    const healthy = groupOf(HEALTHY.id)
    expect(within(failing).getByText(MISSING_REASON)).toBeVisible()
    expect(within(failing).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(within(failing).getByRole('button', { name: 'Edit' })).toBeInTheDocument()
    expect(within(healthy).queryByText(MISSING_REASON)).not.toBeInTheDocument()
    expect(within(healthy).queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(screen.getAllByText(MISSING_REASON), 'the reason is on the page once').toHaveLength(1)
  })

  it('#1277 control: a healthy service stays on, lists its tool and shows no reference error', async () => {
    world.servers = [HEALTHY]
    world.tools = [SEND_EMAIL, ...serviceTools(HEALTHY.id, ['qa_probe'])]
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/external tools/i)
    await openGroup(/^qa-healthy-connector/)

    const healthy = groupOf(HEALTHY.id)
    expect(screen.getByTestId('service-switch-qa-healthy-connector')).toBeChecked()
    expect(within(healthy).getByTestId('tool-row-mcp_qa-healthy-connector_qa_probe')).toBeInTheDocument()
    expect(within(healthy).queryByText('Not connected')).toBeNull()
    expect(within(healthy).queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(screen.queryByText(MISSING_REASON)).not.toBeInTheDocument()
  })

  it('UI States (External tools, Partial): a service that is not connected says "Not connected" in its header, a connected one does not', async () => {
    world.servers = [BROKEN, HEALTHY]
    world.tools = [SEND_EMAIL, ...serviceTools(HEALTHY.id, ['qa_probe'])]
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/external tools/i)
    await screen.findByTestId('service-switch-qa-broken-connector')

    // Both groups stay collapsed, so the error notice inside the group is not drawn:
    // only a header can carry the words.
    expect(within(groupOf(BROKEN.id)).getByText('Not connected')).toBeVisible()
    expect(within(groupOf(HEALTHY.id)).queryByText('Not connected')).toBeNull()
  })

  it('User Story 8 AS2 / G7: Test on a group header tests that service and a success toast says it connected with its tool count', async () => {
    const user = userEvent.setup()
    world.servers = [{ id: 's1', name: 'my-server', transport: 'sse', status: 'connected', tool_count: 5 }]
    vi.mocked(api.testMcpServer).mockResolvedValueOnce({
      success: true,
      message: 'Connected to my-server (sse); 5 tools.',
      tool_count: 5,
      tools: ['a', 'b', 'c', 'd', 'e'],
    })
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/external tools/i)
    const test = await screen.findByRole('button', { name: 'Test my-server' })
    await waitFor(() => expect(test).toBeEnabled(), { timeout: 3000 })
    await user.click(test)

    await waitFor(() => expect(vi.mocked(api.testMcpServer)).toHaveBeenCalledExactlyOnceWith('s1'))
    await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(1))
    expect(useUiStore.getState().toasts[0]).toMatchObject({
      variant: 'success',
      message: expect.stringMatching(/Connected.*5 tool/i),
    })
  })

  it('User Story 8 AS2 / G7: a failed Test shows an error toast carrying the service\'s own message', async () => {
    const user = userEvent.setup()
    world.servers = [{ id: 's2', name: 'broken-server', transport: 'stdio', status: 'error', tool_count: 0 }]
    vi.mocked(api.testMcpServer).mockResolvedValueOnce({ success: false, message: 'Connection refused: dial tcp :3000' })
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/external tools/i)
    const test = await screen.findByRole('button', { name: 'Test broken-server' })
    await waitFor(() => expect(test).toBeEnabled(), { timeout: 3000 })
    await user.click(test)

    await waitFor(() => expect(vi.mocked(api.testMcpServer)).toHaveBeenCalledExactlyOnceWith('s2'))
    await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(1))
    // The new toast names the service first, so the whole message is no longer the
    // service's text alone; the service's own sentence must still be in it, verbatim.
    expect(useUiStore.getState().toasts[0]).toMatchObject({
      variant: 'error',
      message: expect.stringContaining('Connection refused: dial tcp :3000'),
    })
  })

  it('UI States (External tools, Empty): with no service the tab says "No external services yet" in the kit Empty, and Add service stays', async () => {
    world.servers = []
    world.tools = [SEND_EMAIL]
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openTab(/external tools/i)

    // CHECK round 4 finding F3: the empty branch must render the real kit Empty — the
    // dashed-border root, the aria-hidden icon media and the sentence in the
    // description slot — the same structure the notification panel is locked to.
    const sentence = await screen.findByText('No external services yet')
    const description = sentence.closest('[data-slot="empty-description"]')
    expect(description).not.toBeNull()
    const empty = description!.closest('[data-slot="empty"]')
    expect(empty).not.toBeNull()
    expect(empty).toHaveClass('border-dashed')
    const media = empty!.querySelector('[data-slot="empty-media"]')
    expect(media).not.toBeNull()
    expect(media).toHaveAttribute('aria-hidden', 'true')
    expect(media).toHaveAttribute('data-variant', 'icon')
    expect(media!.querySelector('svg')).not.toBeNull()
    // One or more: the page toolbar's button and, if the empty state draws its own, that one too.
    expect(screen.getAllByRole('button', { name: 'Add service' })).not.toHaveLength(0)
  })
})
