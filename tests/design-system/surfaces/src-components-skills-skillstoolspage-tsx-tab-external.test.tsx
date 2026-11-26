// Surface src/components/skills/SkillsToolsPage.tsx#tab:external — UNIT check (the
// "External tools (MCP)" tab). The interaction check is
// ...-tab-external.interaction.test.tsx; the browser check is
// tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109:
//   "External tools tab" row of UI Screens and States
//                       Loading "Loading services…"; Empty "No external services yet" with
//                       "Add service"; Error a callout with Retry; Partial a service that
//                       is not connected says "Not connected" in its header and shows the
//                       error notice inside, the others normal; Success one group per service
//   US-8 AS2            a service group header carries the on/off switch, Test, Edit and Remove
//   US-8 AS3, S-61      a service that cannot connect shows its reason with Retry and Edit
//                       when its group is opened (the reason in S-61 is "missing secret
//                       reference CONTEXT7_KEY")
//   US-8 AS1, S-59, C-7, C-18
//                       every tool of a service whose key mcp_<service>_* is Ask shows Ask;
//                       under a broader rule (mcp_* Deny) the rows show that rule's value
//   S-45                the tab name carries the number of external tools
//
// The accessible names of the header controls are read by the word the spec gives them
// (Test, Edit, Remove, the switch), never by the service-name suffix the page adds.
//
// Only the network edge is doubled: the list requests and the permissions endpoint.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import type { ComponentType } from 'react'
import { ApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'

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
    fetchAgents: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../helpers/skill-permission-network'
import {
  NEVER, groupDisclosure, groupDisclosures, headerReadings, openGroup, openTab, pressedChoices, reading, renderPage,
  requireSkillsToolsPage, resetWorld, serveWorld, service, serviceTools, tool, world,
} from './skills-tools-harness'

const permissions = useSkillPermissionNetwork(() => ({
  policies: { send_email: 'ask' },
  skill_policies: {},
  visibility: { send_email: 'full' },
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec S-45, S-59, S-61, US-8 and the "External tools tab" row of UI Screens and States')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
})

const NOTION = ['search', 'fetch', 'create_page', 'update_page']
const GITHUB = ['list_issues', 'get_issue', 'create_issue']
const panel = () => screen.getByRole('tabpanel')
// A service header is the section that holds the group's disclosure and its controls.
async function headerOf(serviceId: string): Promise<HTMLElement> {
  return (await groupDisclosure(new RegExp(`^${serviceId}\\b`, 'i'))).closest('section') as HTMLElement
}
function withServices(...ids: Array<[string, string[]]>) {
  const tools = ids.map(([id, names]) => serviceTools(id, names))
  world.tools = [tool('send_email'), ...tools.flat()]
  world.servers = ids.map(([id], index) => service(id, tools[index]))
}

describe('External tools tab surface unit contract', () => {
  it('UI states Success S-45: the tab opens one collapsed group per service, each named by its service with its tool count', async () => {
    withServices(['notion', NOTION], ['github', GITHUB])
    renderPage(Page)

    // S-45's count: the seven external tools, and not send_email.
    await waitFor(() => expect(reading(screen.getByRole('tab', { name: /^external tools/i }))).toBe('External tools (MCP) 7'))
    await openTab(/^external tools/i)
    await waitFor(() => expect(headerReadings().sort()).toEqual(['github 3', 'notion 4']))
    for (const header of groupDisclosures()) expect(header, `${reading(header)} starts collapsed`).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryAllByTestId(/^tool-row-/)).toHaveLength(0)
  })

  it('US-8 AS2: each service header carries the on/off switch, Test, Edit and Remove, and the switch shows the stored state', async () => {
    withServices(['notion', NOTION], ['github', GITHUB])
    world.servers = world.servers.map((server) => (server.id === 'github' ? { ...server, enabled: false } : server))
    renderPage(Page)
    await openTab(/^external tools/i)

    const notion = await headerOf('notion')
    const github = await headerOf('github')
    expect(within(notion).getByRole('switch'), 'a connected, enabled service shows its switch on').toBeChecked()
    expect(within(github).getByRole('switch'), 'a service stored as off shows its switch off').not.toBeChecked()
    for (const [name, header] of [['notion', notion], ['github', github]] as const) {
      for (const word of [/^Test\b/, /^Edit\b/, /^Remove\b/]) {
        expect(within(header).getAllByRole('button', { name: word }), `the ${name} header has one ${word} control`).toHaveLength(1)
      }
    }
  })

  it('S-59 US-8 AS1 C-7: with the key mcp_notion_* at Ask every tool of the service shows Ask', async () => {
    permissions().stored.policies['mcp_notion_*'] = 'ask'
    permissions().stored.visibility['mcp_notion_*'] = 'full'
    withServices(['notion', NOTION])
    renderPage(Page)
    await openTab(/^external tools/i)
    await openGroup(/^notion\b/i)

    const rows = await screen.findAllByTestId(/^tool-row-mcp_notion_/)
    expect(rows).toHaveLength(4)
    for (const row of rows) expect(pressedChoices(row), `${row.getAttribute('data-testid')} shows Ask`).toEqual(['Ask'])
  })

  it('C-18 US-8 AS1: under a broader rule mcp_* Deny the rows of a service with no key of its own show Deny', async () => {
    permissions().stored.policies['mcp_*'] = 'deny'
    permissions().stored.visibility['mcp_*'] = 'full'
    withServices(['notion', NOTION])
    renderPage(Page)
    await openTab(/^external tools/i)
    await openGroup(/^notion\b/i)

    const rows = await screen.findAllByTestId(/^tool-row-mcp_notion_/)
    expect(rows).toHaveLength(4)
    for (const row of rows) expect(pressedChoices(row), `${row.getAttribute('data-testid')} shows Deny`).toEqual(['Deny'])
  })

  it('UI states Loading: while the services are unanswered the tab says Loading services… and draws no service group', async () => {
    vi.mocked(api.fetchMcpServers).mockImplementation(NEVER)
    renderPage(Page)
    await openTab(/^external tools/i)

    expect(await within(panel()).findByText('Loading services…')).toBeInTheDocument()
    expect(groupDisclosures()).toHaveLength(0)
    expect(screen.queryAllByRole('switch')).toHaveLength(0)
  })

  it('UI states Empty: with no service the tab says No external services yet and offers Add service', async () => {
    world.tools = [tool('send_email')]
    renderPage(Page)
    await openTab(/^external tools/i)

    expect(await within(panel()).findByText('No external services yet')).toBeInTheDocument()
    expect(screen.queryByText('Loading services…')).toBeNull()
    const add = await screen.findAllByRole('button', { name: 'Add service' })
    expect(add.length, 'at least one Add service button').toBeGreaterThan(0)
    for (const button of add) await waitFor(() => expect(button).toBeEnabled())
  })

  it('UI states Error: a failed services request shows its cause with Retry and draws no service group', async () => {
    const cause = 'The services list could not be read'
    vi.mocked(api.fetchMcpServers).mockRejectedValue(new ApiError(500, cause))
    renderPage(Page)
    await openTab(/^external tools/i)

    expect(await within(panel()).findByText(new RegExp(cause))).toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(groupDisclosures()).toHaveLength(0)
    expect(screen.queryByText('Loading services…')).toBeNull()
  })

  it('S-61 US-8 AS3: a service that cannot connect says Not connected in its header and its opened group shows the reason with Retry and Edit', async () => {
    withServices(['github', GITHUB])
    const reason = 'missing secret reference CONTEXT7_KEY' // S-61's Given, verbatim
    world.servers = [
      ...world.servers,
      service('context7', serviceTools('context7', ['query']), { status: 'error', status_reason: reason, tool_count: 0, tools: [] }),
    ]
    renderPage(Page)
    await openTab(/^external tools/i)

    // Partial: the header says it before the group is opened; the connected service does not.
    const failing = await headerOf('context7')
    const healthy = await headerOf('github')
    expect(within(failing).getByText('Not connected')).toBeVisible()
    expect(within(healthy).queryByText('Not connected')).toBeNull()
    expect(screen.queryByText(reason), 'the notice is inside the group, which is still closed').toBeNull()

    await openGroup(/^context7\b/i)
    await openGroup(/^github\b/i)
    const opened = await headerOf('context7') // looked up again: the opened group is the one the reader sees
    const openedHealthy = await headerOf('github')
    expect(await within(opened).findByText(reason)).toBeVisible()
    await waitFor(() => expect(within(opened).getByRole('button', { name: 'Retry' })).toBeEnabled())
    expect(within(opened).getByRole('button', { name: 'Edit' })).toBeEnabled()
    expect(within(openedHealthy).queryByRole('button', { name: 'Retry' }), 'the connected service has no notice').toBeNull()
    expect(screen.getAllByText(reason), 'the reason shows once on the page').toHaveLength(1)
  })
})
