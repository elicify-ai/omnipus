// Surface src/components/skills/SkillsToolsPage.tsx#tab:external — INTERACTION check
// (the "External tools (MCP)" tab). The unit check is ...-tab-external.test.tsx; the
// browser check is tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109 and, for
// the wire shape of the switch, contracts/components/schemas/McpServerUpdate.yaml:
//   S-60, US-8 AS2   turning a service's switch off turns the service off after the click,
//                    with no further action (the update body is {enabled: false}: McpServerUpdate)
//   S-62, US-8 AS4   Remove in the header asks "Remove this service? Agents that use it
//                    lose its tools."; Cancel keeps the service; confirming removes that
//                    service and no other
//   S-61, US-8 AS3   in the notice of a service that cannot connect, Retry tests that
//                    service again and Edit opens its edit form (the spec names the two
//                    buttons, FR-046; what each does is read from their names, "Retry" and
//                    "Edit", and the form is checked only for naming the service it edits;
//                    so both tests are titled "characterization test": the spec is silent)
//   S-59, US-8 AS1   when a service later reports one more tool, that row also shows Ask
//   "External tools tab" row of UI Screens and States, Error: Retry asks for the services again
//
// Only the network edge is doubled: the list requests, the three service writes
// (update, delete, test) and the permissions endpoint.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentType } from 'react'
import { ApiError } from '@/lib/api-error'
import type { McpServer } from '@/lib/api'
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
    updateMcpServer: vi.fn(),
    deleteMcpServer: vi.fn(),
    testMcpServer: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../helpers/skill-permission-network'
import {
  groupDisclosure, headerReadings, installOverlayPolyfills, openGroup, openTab, pressedChoices, renderPage,
  requireSkillsToolsPage, resetWorld, serveWorld, service, serviceTools, tool, world,
} from './skills-tools-harness'

// Installs the permissions endpoint stub for every test; no test of this file reads what was sent to it.
useSkillPermissionNetwork(() => ({
  policies: { send_email: 'ask', 'mcp_github_*': 'ask', 'mcp_notion_*': 'ask' },
  skill_policies: {},
  visibility: { send_email: 'full', 'mcp_github_*': 'full', 'mcp_notion_*': 'full' },
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  installOverlayPolyfills()
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec S-59 to S-62 and US-8')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
})

const NOTION = ['search', 'fetch', 'create_page', 'update_page']
const GITHUB = ['list_issues', 'get_issue', 'create_issue']
const REASON = 'missing secret reference CONTEXT7_KEY' // S-61's Given, verbatim
const panel = () => screen.getByRole('tabpanel')
async function headerOf(serviceId: string): Promise<HTMLElement> {
  return (await groupDisclosure(new RegExp(`^${serviceId}\\b`, 'i'))).closest('section') as HTMLElement
}
function withServices(...ids: Array<[string, string[]]>) {
  const tools = ids.map(([id, names]) => serviceTools(id, names))
  world.tools = [tool('send_email'), ...tools.flat()]
  world.servers = ids.map(([id], index) => service(id, tools[index]))
}
// The server's side of a switch change: the stored flag follows the update body.
function answerUpdates() {
  vi.mocked(api.updateMcpServer).mockImplementation(async (id, body) => {
    world.servers = world.servers.map((server) => (server.id === id ? { ...server, enabled: body.enabled ?? server.enabled } : server))
    return world.servers.find((server) => server.id === id) as McpServer
  })
}
function failingService() {
  withServices(['github', GITHUB])
  world.servers = [
    ...world.servers,
    service('context7', [], { status: 'error', status_reason: REASON, tool_count: 0, tools: [] }),
  ]
}

describe('External tools tab surface interaction contract', () => {
  it('S-60 US-8 AS2: turning the switch of github off sends one update {enabled: false} naming only it, shows it off and asks nothing more', async () => {
    const user = userEvent.setup()
    withServices(['github', GITHUB], ['notion', NOTION])
    answerUpdates()
    renderPage(Page)
    await openTab(/^external tools/i)
    const control = within(await headerOf('github')).getByRole('switch')
    await waitFor(() => expect(control).toBeEnabled())
    expect(control, 'the service starts on').toBeChecked()

    await user.click(control)

    await waitFor(() => expect(api.updateMcpServer).toHaveBeenCalledExactlyOnceWith('github', { enabled: false }))
    await waitFor(async () => expect(within(await headerOf('github')).getByRole('switch')).not.toBeChecked())
    expect(within(await headerOf('notion')).getByRole('switch'), 'the other service is untouched').toBeChecked()
    expect(screen.queryByRole('alertdialog'), 'no confirmation is asked').toBeNull()
    expect(screen.queryByRole('dialog'), 'no form is opened').toBeNull()
  })

  it('S-60 control: a service stored as off turns on with one update {enabled: true}', async () => {
    const user = userEvent.setup()
    withServices(['github', GITHUB])
    world.servers = world.servers.map((server) => ({ ...server, enabled: false }))
    answerUpdates()
    renderPage(Page)
    await openTab(/^external tools/i)
    const control = within(await headerOf('github')).getByRole('switch')
    await waitFor(() => expect(control).toBeEnabled())
    expect(control).not.toBeChecked()

    await user.click(control)

    await waitFor(() => expect(api.updateMcpServer).toHaveBeenCalledExactlyOnceWith('github', { enabled: true }))
    await waitFor(async () => expect(within(await headerOf('github')).getByRole('switch')).toBeChecked())
  })

  it('S-62 US-8 AS4: Remove in a service header asks first, and Cancel keeps the service', async () => {
    const user = userEvent.setup()
    withServices(['github', GITHUB])
    renderPage(Page)
    await openTab(/^external tools/i)
    const remove = within(await headerOf('github')).getByRole('button', { name: /^Remove\b/ })
    await waitFor(() => expect(remove).toBeEnabled())

    await user.click(remove)

    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Remove this service? Agents that use it lose its tools.')).toBeVisible()
    expect(api.deleteMcpServer, 'nothing is removed by asking').not.toHaveBeenCalled()
    await user.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.deleteMcpServer, 'Cancel removes nothing').not.toHaveBeenCalled()
    expect(headerReadings(), 'the service is still listed').toEqual(['github 3'])
  })

  it('S-62 US-8 AS4: confirming the question removes that service and no other', async () => {
    const user = userEvent.setup()
    withServices(['github', GITHUB], ['notion', NOTION])
    vi.mocked(api.deleteMcpServer).mockImplementation(async (id) => {
      world.servers = world.servers.filter((server) => server.id !== id)
      world.tools = world.tools.filter((entry) => entry.server_id !== id)
    })
    renderPage(Page)
    await openTab(/^external tools/i)
    const remove = within(await headerOf('github')).getByRole('button', { name: /^Remove\b/ })
    await waitFor(() => expect(remove).toBeEnabled())

    await user.click(remove)
    const ask = await screen.findByRole('alertdialog')
    await user.click(within(ask).getByRole('button', { name: /^remove\b/i }))

    await waitFor(() => expect(api.deleteMcpServer).toHaveBeenCalledExactlyOnceWith('github'))
    await waitFor(() => expect(headerReadings()).toEqual(['notion 4']))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('S-61 US-8 AS3 characterization test: Retry in the notice of a service that cannot connect tests that service again', async () => {
    const user = userEvent.setup()
    failingService()
    vi.mocked(api.testMcpServer).mockResolvedValue({ success: false, message: REASON })
    renderPage(Page)
    await openTab(/^external tools/i)
    await openGroup(/^context7\b/i)
    const notice = await headerOf('context7')
    const retry = await within(notice).findByRole('button', { name: 'Retry' })
    await waitFor(() => expect(retry).toBeEnabled())

    await user.click(retry)

    await waitFor(() => expect(api.testMcpServer).toHaveBeenCalledExactlyOnceWith('context7'))
    expect(within(await headerOf('context7')).getByText(REASON), 'the service still cannot connect, so the reason stays').toBeVisible()
  })

  it('S-61 US-8 AS3 characterization test: Edit in the notice opens the edit form of that service', async () => {
    const user = userEvent.setup()
    failingService()
    renderPage(Page)
    await openTab(/^external tools/i)
    await openGroup(/^context7\b/i)
    const edit = await within(await headerOf('context7')).findByRole('button', { name: 'Edit' })
    await waitFor(() => expect(edit).toBeEnabled())

    await user.click(edit)

    const form = await screen.findByRole('dialog')
    // The spec says Edit is offered (FR-046), not how the form shows which service it edits, so the
    // reading is stated loosely: the name of that service is on the form, as text or as a field value.
    const shown = within(form).queryAllByText('context7').length + within(form).queryAllByDisplayValue('context7').length
    expect(shown, 'the form names the service being edited, context7').toBeGreaterThanOrEqual(1)
  })

  it('S-59 US-8 AS1: after Test the service reports a fifth tool and its row shows Ask like the other four', async () => {
    const user = userEvent.setup()
    withServices(['notion', NOTION])
    const later = 'mcp_notion_archive_page'
    vi.mocked(api.testMcpServer).mockImplementation(async () => {
      world.tools = [...world.tools, ...serviceTools('notion', ['archive_page'])]
      world.servers = world.servers.map((server) => ({ ...server, tool_count: 5, tools: [...(server.tools ?? []), later] }))
      return { success: true, message: 'Connected to notion; 5 tools.', tool_count: 5, tools: [...NOTION.map((name) => `mcp_notion_${name}`), later] }
    })
    renderPage(Page)
    await openTab(/^external tools/i)
    await openGroup(/^notion\b/i)
    expect(await screen.findAllByTestId(/^tool-row-mcp_notion_/)).toHaveLength(4)
    const test = within(await headerOf('notion')).getByRole('button', { name: /^Test\b/ })
    await waitFor(() => expect(test).toBeEnabled())

    await user.click(test)

    await waitFor(() => expect(screen.getAllByTestId(/^tool-row-mcp_notion_/)).toHaveLength(5))
    for (const row of screen.getAllByTestId(/^tool-row-mcp_notion_/)) {
      expect(pressedChoices(row), `${row.getAttribute('data-testid')} shows Ask`).toEqual(['Ask'])
    }
    expect(screen.getByTestId(`tool-row-${later}`), 'the later tool has a row').toBeInTheDocument()
  })

  it('UI states Error: Retry asks for the services again and the groups appear once they load', async () => {
    const user = userEvent.setup()
    withServices(['github', GITHUB])
    vi.mocked(api.fetchMcpServers).mockRejectedValueOnce(new ApiError(500, 'The services list could not be read'))
    renderPage(Page)
    await openTab(/^external tools/i)
    expect(await within(panel()).findByText(/The services list could not be read/)).toBeInTheDocument()

    await user.click(within(panel()).getByRole('button', { name: /^retry$/i }))

    expect(await headerOf('github')).toBeInTheDocument()
    expect(within(panel()).queryByRole('button', { name: /^retry$/i }), 'the error is gone once the services load').toBeNull()
  })
})
