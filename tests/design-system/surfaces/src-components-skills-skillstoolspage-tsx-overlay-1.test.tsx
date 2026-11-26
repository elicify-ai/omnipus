// Surface src/components/skills/SkillsToolsPage.tsx#overlay:1 — UNIT check (the
// "Web & Search" side panel that the Settings button of the Web & Search group opens).
// The interaction check is ...-overlay-1.interaction.test.tsx; the browser check is
// tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109:
//   US-9 AS1, S-64, FR-048   the Web & Search group has a Settings button; pressing it opens
//                            a side panel with the Default search and Fallback cards; before
//                            that no panel is open
//   Design-System Components "Dialogs and panels": the panel is a `sheet` (a dialog role)
//   "Web & Search panel" row of UI Screens and States
//                            every state is "today's": the states Settings → Integrations
//                            showed before this package moved web search here. The record of
//                            today's states is the Settings test IntegrationsSection.adr096
//                            (no cards while the list loads, a configured but unusable service
//                            reads "key not reaching search" and never "Ready") and the Settings
//                            read-error callout (names what failed, quotes the cause, Retry)
//
// The panel's title is the spec's own name for it ("Web & Search panel"). Tests marked
// "characterization test" pin text the spec does not give; they do not verify correctness.
//
// Only the network edge is doubled: the list requests, the search-service requests and the
// permissions endpoint.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    fetchIntegrationProviders: vi.fn(),
    configureIntegrationProvider: vi.fn(),
    fetchAppState: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../helpers/skill-permission-network'
import {
  NEVER, groupDisclosures, installOverlayPolyfills, openGroup, openTab, reading, renderPage,
  requireSkillsToolsPage, resetWorld, serveWorld, tool, world,
} from './skills-tools-harness'
import { PLATFORM_APP_STATE, searchProviders, searchService, searchServices } from './skills-tools-search-fixture'

const permissions = useSkillPermissionNetwork(() => ({
  policies: { bash: 'ask', fetch_url: 'allow', browser_navigate: 'ask' },
  skill_policies: {},
  visibility: { bash: 'full', fetch_url: 'full', browser_navigate: 'full' },
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  installOverlayPolyfills()
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec US-9 AS1, S-64 and FR-048')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  // One tool in each group the spec names on this tab: Browser, Shell and Web & Search.
  world.tools = [
    tool('browser_navigate', { category: 'browser' }),
    tool('bash', { category: 'shell' }),
    tool('fetch_url', { category: 'web' }),
  ]
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(searchProviders())
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

const SETTINGS = { name: 'Settings' } as const
async function openWebGroup() {
  renderPage(Page)
  await openTab(/^omnipus tools/i)
  await openGroup(/^web & search/i)
}
async function pressSettings() {
  await userEvent.setup().click(await screen.findByRole('button', SETTINGS))
}
const panel = () => screen.findByRole('dialog', { name: 'Web & Search' })
const NO_CARDS = () => {
  expect(screen.queryByTestId('default-search-card'), 'no Default search card').toBeNull()
  expect(screen.queryByTestId('fallback-search-card'), 'no Fallback card').toBeNull()
}

describe('Web & Search side panel surface unit contract', () => {
  it('US-9 AS1: until Settings is pressed no side panel is open', async () => {
    await openWebGroup()

    expect(await screen.findByRole('button', SETTINGS), 'the Settings button is there to press').toBeEnabled()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('FR-048 US-9 AS1: the Web & Search group has a Settings button and the Browser and Shell groups have none', async () => {
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^browser/i)
    await openGroup(/^shell/i)
    await openGroup(/^web & search/i)

    const buttons = await screen.findAllByRole('button', SETTINGS)
    expect(buttons, 'one Settings button on the tab, with every group open').toHaveLength(1)
    // The one button sits in the Web & Search group: the group box also holds its disclosure header.
    const group = buttons[0].closest('section')
    expect(group, 'the button sits in a group box').not.toBeNull()
    const headers = within(group as HTMLElement)
      .getAllByRole('button')
      .filter((button) => button.hasAttribute('aria-expanded') && !button.hasAttribute('aria-haspopup'))
    expect(headers.map((header) => reading(header).toLowerCase())).toEqual([expect.stringMatching(/^web & search\b/)])
    // Control: the Shell group carries its own Settings link, which is a link and not this button.
    expect(groupDisclosures().length, 'the three groups are drawn').toBeGreaterThanOrEqual(3)
  })

  it('US-9 AS1 S-64: pressing Settings opens a side panel named Web & Search with the Default search and Fallback cards', async () => {
    await openWebGroup()

    await pressSettings()

    const dialog = await panel()
    const defaultCard = await within(dialog).findByTestId('default-search-card')
    const fallbackCard = await within(dialog).findByTestId('fallback-search-card')
    expect(within(defaultCard).getByText('Default search')).toBeInTheDocument()
    expect(within(defaultCard).getByText('Tavily'), 'the default is the service the server reports as the default').toBeInTheDocument()
    expect(within(fallbackCard).getByText('Fallback')).toBeInTheDocument()
    expect(within(fallbackCard).getByText('DuckDuckGo'), 'the fallback is the service the server reports as the fallback').toBeInTheDocument()
    expect(permissions().patches(), 'opening the panel writes nothing').toEqual([])
    expect(api.configureIntegrationProvider, 'opening the panel saves nothing').not.toHaveBeenCalled()
  })

  it('US-9 AS1: the panel lists every search service once, so a person can find the one to choose', async () => {
    await openWebGroup()

    await pressSettings()

    const dialog = await panel()
    for (const [id, name] of [['brave', 'Brave Search'], ['tavily', 'Tavily'], ['duckduckgo', 'DuckDuckGo'], ['exa', 'Exa']]) {
      const row = await within(dialog).findByTestId(`search-row-${id}`)
      expect(within(row).getAllByText(name), `${name} appears once in its own row`).toHaveLength(1)
    }
  })

  it('UI states Loading (Web & Search panel, today\'s): while the services are unanswered the panel shows no card, and so shows no None', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockImplementation(NEVER)
    await openWebGroup()

    await pressSettings()

    await panel()
    NO_CARDS()
    expect(screen.queryByText('None'), 'an unanswered list must not read as "nothing chosen"').toBeNull()
  })

  it('UI states Error (Web & Search panel, today\'s): a failed request shows its cause with Retry, no card and no write', async () => {
    const cause = 'Omnipus did not respond'
    vi.mocked(api.fetchIntegrationProviders).mockRejectedValue(new ApiError(503, cause))
    await openWebGroup()

    await pressSettings()

    const dialog = await panel()
    expect(await within(dialog).findByText(new RegExp(cause))).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /^retry$/i })).toBeEnabled()
    NO_CARDS()
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('characterization test: the Error state of the panel is titled Failed to load integrations', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockRejectedValue(new ApiError(503, 'Omnipus did not respond'))
    await openWebGroup()

    await pressSettings()

    expect(await within(await panel()).findByText('Failed to load integrations')).toBeInTheDocument()
  })

  it('characterization test: with no search service reported the panel says No search providers available.', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(searchProviders([]))
    await openWebGroup()

    await pressSettings()

    expect(await within(await panel()).findByText('No search providers available.')).toBeInTheDocument()
    NO_CARDS()
  })

  it('UI states Partial (Web & Search panel, today\'s): a default whose key does not reach search reads key not reaching search and is never Ready', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(
      searchProviders(searchServices().map((row) => (row.id === 'tavily' ? searchService({ ...row, usable: false }) : row))),
    )
    await openWebGroup()

    await pressSettings()

    const dialog = await panel()
    expect(await within(dialog).findByTestId('key-not-reaching-tavily')).toHaveTextContent(/key not reaching search/i)
    expect(within(dialog).queryByTestId('ready-tavily'), 'a service whose key does not reach search is never Ready').toBeNull()
    expect(within(await within(dialog).findByTestId('default-search-card')).getByText('Tavily')).toBeInTheDocument()
  })

  it('UI states Success (Web & Search panel, today\'s): a usable service reads Ready', async () => {
    await openWebGroup()

    await pressSettings()

    expect(await within(await panel()).findByTestId('ready-tavily')).toHaveTextContent('Ready')
  })

  it('characterization test: the page asks for the search services only once the panel is open', async () => {
    await openWebGroup()
    await screen.findByRole('button', SETTINGS)
    await waitFor(() => expect(api.fetchRegistryTools).toHaveBeenCalled())
    expect(api.fetchIntegrationProviders, 'nothing asks for search services before Settings is pressed').not.toHaveBeenCalled()

    await pressSettings()

    await waitFor(() => expect(api.fetchIntegrationProviders).toHaveBeenCalled())
  })
})
