// Surface src/components/skills/SkillsToolsPage.tsx#tab:tools — UNIT check (the
// "Omnipus tools" tab). The interaction check is ...-tab-tools.interaction.test.tsx;
// the browser check is tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109:
//   S-45, US-6 AS1                  the tab name carries the number of Omnipus tools
//   US-6 AS2, S-46, S-64, S-65      groups start collapsed; the groups are named Browser,
//                                   Shell and Web & Search
//   US-9 AS2, AS3, AS4, S-65        the intro "Open Settings to change Auto-approve and God
//                                   Mode." links to Settings → Protection; the Shell group
//                                   links "Shell safety rules (Settings)" to Settings →
//                                   Sandbox & network; no link goes to Settings for tool
//                                   permissions
//   US-5 AS1, AS5, S-37             "What the model sees" offers Hidden, Name only, Name and
//                                   short description, Full; a locked tool says "Always
//                                   loaded: agents need this to work, so its level cannot be
//                                   changed."
//   "Omnipus tools tab" row of UI Screens and States, S-67, S-132
//                                   loading, empty, error and partial states
//
// Only the network edge is doubled: the list requests and the permissions endpoint.
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
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../helpers/skill-permission-network'
import {
  NEVER, groupDisclosures, headerReadings, openGroup, openTab, reading, renderPage,
  requireSkillsToolsPage, resetWorld, serveWorld, service, serviceTools, tool, world,
} from './skills-tools-harness'

const permissions = useSkillPermissionNetwork(() => ({
  policies: { bash: 'ask', fetch_url: 'allow', browser_navigate: 'ask', send_email: 'ask' },
  skill_policies: {},
  visibility: { bash: 'full', fetch_url: 'name_only', browser_navigate: 'full', send_email: 'full' },
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec S-45, S-65, S-67, S-132, US-5 and US-9')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  // One tool in each of the groups the spec names (Shell, Web & Search, Browser), and one more.
  world.tools = [
    tool('bash', { category: 'shell' }),
    tool('fetch_url', { category: 'web', visibility: 'name_only' }),
    tool('browser_navigate', { category: 'browser' }),
    tool('send_email', { category: 'communication' }),
  ]
})

const CHOICES = /^(allow|ask|deny)$/i
const panel = () => screen.getByRole('tabpanel')

describe('Omnipus tools tab surface unit contract', () => {
  it('S-45 US-6 AS1: the tab name counts the Omnipus tools and leaves the external ones out', async () => {
    const external = serviceTools('notion', ['search', 'fetch'])
    world.tools = [...world.tools, ...external]
    world.servers = [service('notion', external)]
    renderPage(Page)

    await waitFor(() => expect(screen.getByRole('tab', { name: /^omnipus tools/i })).toHaveTextContent(/4$/))
    expect(reading(screen.getByRole('tab', { name: /^omnipus tools/i }))).toBe('Omnipus tools 4')
  })

  it('US-6 AS2 S-65: groups start collapsed and the spec-named groups are Browser, Shell and Web & Search', async () => {
    renderPage(Page)
    await openTab(/^omnipus tools/i)

    await waitFor(() => expect(groupDisclosures().length).toBeGreaterThanOrEqual(3))
    const readings = headerReadings()
    for (const named of [/^browser\b/, /^shell\b/, /^web & search\b/]) {
      expect(readings.some((text) => named.test(text)), `a group header reads ${named}`).toBe(true)
    }
    for (const header of groupDisclosures()) expect(header, `${reading(header)} starts collapsed`).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryAllByTestId(/^tool-row-/)).toHaveLength(0)
  })

  it('US-9 AS3 S-65: the intro reads Open Settings to change Auto-approve and God Mode. with a link to Settings Protection', async () => {
    renderPage(Page)
    await openTab(/^omnipus tools/i)

    const link = await within(panel()).findByRole('link', { name: 'Open Settings' })
    expect(link.getAttribute('href')).toMatch(/\/settings\?tab=protection$/)
    expect(link.closest('p')?.textContent).toBe('Open Settings to change Auto-approve and God Mode.')
  })

  it('US-9 AS2 S-65: the Shell group links Shell safety rules (Settings) to Settings Sandbox and network', async () => {
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)

    const link = await within(panel()).findByRole('link', { name: 'Shell safety rules (Settings)' })
    expect(link.getAttribute('href')).toMatch(/\/settings\?tab=sandbox$/)
  })

  it('US-9 AS4 S-65: no link on the tab sends the person to Settings for tool permissions', async () => {
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)
    await openGroup(/^web & search/i)

    const hrefs = within(panel()).getAllByRole('link').map((link) => link.getAttribute('href') ?? '')
    // Control: the two Settings links are in the list, so the list can see a Settings link.
    expect(hrefs.filter((href) => /\/settings\?tab=/.test(href)).sort().map((href) => href.replace(/^.*\/settings/, '/settings')))
      .toEqual(['/settings?tab=protection', '/settings?tab=sandbox'])
    expect(hrefs.filter((href) => /tab=security/.test(href)), 'no link goes to Settings → Security').toEqual([])
  })

  it('US-5 AS1: the What the model sees control of a row offers Hidden, Name only, Name and short description and Full', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^web & search/i)

    const row = await screen.findByTestId('tool-row-fetch_url')
    // The row shows the stored level (name_only) in its control name.
    await user.click(within(row).getByRole('button', { name: /^What the model sees\b.*: Name only$/ }))
    const menu = await screen.findByRole('menu')
    const items = within(menu).getAllByRole('menuitem').map((item) => (item.textContent ?? '').split(':')[0].trim())
    expect(items).toEqual(['Hidden', 'Name only', 'Name and short description', 'Full'])
    // Each has a one-line explanation after its name.
    for (const item of within(menu).getAllByRole('menuitem')) expect((item.textContent ?? '').split(':')[1]?.trim().length, `${item.textContent} has an explanation`).toBeGreaterThan(0)
  })

  it('US-5 AS5 S-37: a locked tool says why its level cannot be changed', async () => {
    world.tools = [...world.tools, tool('ask_user', { category: 'core', visibility_locked: true, visibility: 'full' })]
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^general/i)

    const row = await screen.findByTestId('tool-row-ask_user')
    expect(within(row).getByText('Always loaded: agents need this to work, so its level cannot be changed.')).toBeVisible()
  })

  it('UI states Loading: while the tools are unanswered the tab says Loading tools… and offers no row and no enabled badge', async () => {
    vi.mocked(api.fetchRegistryTools).mockImplementation(NEVER)
    renderPage(Page)
    await openTab(/^omnipus tools/i)

    expect(await within(panel()).findByText('Loading tools…')).toBeInTheDocument()
    expect(screen.queryAllByTestId(/^tool-row-/)).toHaveLength(0)
    for (const badge of screen.queryAllByRole('button', { name: CHOICES })) expect(badge).toBeDisabled()
  })

  it('UI states Empty: a registry that reports no tools shows No Omnipus tools reported with Retry', async () => {
    world.tools = []
    renderPage(Page)
    await openTab(/^omnipus tools/i)

    expect(await within(panel()).findByText('No Omnipus tools reported')).toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: /^retry$/i })).toBeEnabled()
  })

  it('S-67 UI states Error: a failed tools request shows its cause with Retry and no badge is enabled', async () => {
    const cause = 'Omnipus did not respond'
    vi.mocked(api.fetchRegistryTools).mockRejectedValue(new ApiError(503, cause))
    renderPage(Page)
    await openTab(/^omnipus tools/i)

    // The page draws the error callout above its tabs, so it is looked for on the whole page.
    expect(await screen.findByText(cause)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(screen.queryAllByTestId(/^tool-row-/), 'no row is drawn from a failed request').toHaveLength(0)
    for (const badge of screen.queryAllByRole('button', { name: CHOICES })) expect(badge, 'no badge is enabled after a failed load').toBeDisabled()
    expect(permissions().patches(), 'nothing was written').toEqual([])
  })

  it('S-132 UI states Partial: when the tools load but the permissions do not, rows and their levels show read-only with Permissions could not be loaded and Retry', async () => {
    permissions().failNextRead(500, 'Omnipus did not respond')
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)

    // The notice and its Retry sit above the tabs, so both are looked for on the whole page.
    expect(await screen.findByText('Permissions could not be loaded.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    const row = await screen.findByTestId('tool-row-bash')
    // The level shown is the one the tools request gave the row (C-6), read-only.
    expect(within(row).getByRole('button', { name: /^What the model sees\b.*: Full$/ })).toBeDisabled()
    for (const badge of within(row).getAllByRole('button', { name: CHOICES })) expect(badge, 'a badge cannot write without the permissions').toBeDisabled()
    expect(permissions().patches(), 'nothing was written').toEqual([])
  })
})
