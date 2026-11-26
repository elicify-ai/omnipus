// Surface src/components/skills/SkillsToolsPage.tsx#tab:tools — INTERACTION check
// (the "Omnipus tools" tab). The unit check is ...-tab-tools.test.tsx; the browser
// check is tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109:
//   S-46, S-47, US-6 AS3     typing "browser" opens only the Browser group and shows only
//                            the rows that match; Escape empties the field and the groups
//                            show collapsed
//   S-49, US-6 AS5           "No tools match “zzzz”" with "Clear search and filters" (the
//                            spec prints curly quotes, the page straight ones: either is
//                            accepted and the difference is reported)
//   US-6 AS4, C-20           under a search the group's Set all says "Set 3 of 3 matching"
//                            and changes exactly the rows on screen
//   US-1 AS1, C-2            a click on Deny shows Deny and saves only that tool
//   S-69, US-10 AS4          Tab then Enter on the badges of `bash` moves focus to Ask and
//                            selects it, and "Saved" shows (that a polite status region
//                            announces it is S-69's last clause: probed apart, see below)
//   US-5 AS1, C-2            the level menu changes only that tool's level
//   S-51, US-6 AS7           a "What the model sees" filter shows the note "Denied items are
//                            not loaded, so this filter leaves them out. Use the Permission
//                            filter to find them."
//
// Only the network edge is doubled: the list requests and the permissions endpoint.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentType } from 'react'
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
import { expectPermissionPatches, useSkillPermissionNetwork } from '../../helpers/skill-permission-network'
import {
  groupDisclosures, openGroup, openTab, pressedChoices, renderPage, requireSkillsToolsPage, resetWorld, serveWorld, tool, world,
} from './skills-tools-harness'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {
    browser_click: 'ask', browser_navigate: 'ask', browser_type: 'ask', fetch_url: 'allow', bash: 'allow', send_email: 'ask',
  },
  skill_policies: {},
  visibility: {
    browser_click: 'full', browser_navigate: 'full', browser_type: 'full', fetch_url: 'name_only', bash: 'full', send_email: 'full',
  },
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec US-6, US-1 AS1, US-5 AS1, S-46 to S-51 and S-69')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  world.tools = [
    tool('browser_click', { category: 'browser' }),
    tool('browser_navigate', { category: 'browser' }),
    tool('browser_type', { category: 'browser' }),
    tool('fetch_url', { category: 'web', visibility: 'name_only' }),
    tool('bash', { category: 'shell' }),
    tool('send_email', { category: 'communication' }),
  ]
})

const search = () => screen.findByRole('searchbox', { name: /search/i })
const shownRows = () => screen.queryAllByTestId(/^tool-row-/).map((row) => row.getAttribute('data-testid')).sort()
const BROWSER_ROWS = ['tool-row-browser_click', 'tool-row-browser_navigate', 'tool-row-browser_type']
// "Saved" shown after a change (UI Screens and States, Success). That a polite status
// REGION announces it (S-69's Then, Accessibility "Save signal") is a separate claim,
// probed in skills-tools-a11y-spec-gaps.test.tsx and not asserted here.
const savedShown = () => screen.queryAllByText('Saved').length > 0

describe('Omnipus tools tab surface interaction contract', () => {
  it('S-46 US-6 AS3: typing browser opens only the Browser group and shows only the rows that match', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    const field = await search()
    await waitFor(() => expect(groupDisclosures().length).toBeGreaterThanOrEqual(4))
    expect(shownRows(), 'groups start collapsed, so no row is drawn').toEqual([])

    await user.type(field, 'browser')

    await waitFor(() => expect(shownRows()).toEqual(BROWSER_ROWS))
    const open = groupDisclosures().filter((header) => header.getAttribute('aria-expanded') === 'true')
    expect(open.map((header) => header.textContent?.toLowerCase()), 'only the Browser group is open').toEqual([expect.stringMatching(/^browser/)])
  })

  it('S-47 US-6 AS3: Escape in the search field empties it and every group shows collapsed again', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    const field = await search()
    await user.type(field, 'browser')
    await waitFor(() => expect(shownRows()).toEqual(BROWSER_ROWS))

    field.focus()
    await user.keyboard('{Escape}')

    expect(field).toHaveValue('')
    await waitFor(() => expect(shownRows()).toEqual([]))
    for (const header of groupDisclosures()) expect(header).toHaveAttribute('aria-expanded', 'false')
  })

  it('S-49 US-6 AS5: a search with no match shows No tools match with Clear search and filters, and Clear brings the groups back', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await user.type(await search(), 'zzzz')

    // Spec S-49 prints curly quotes, the page straight ones: either is accepted (reported in the pack).
    expect(await screen.findByText(/^No tools match [“"]zzzz[”"]$/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Clear search and filters' }))

    expect(await search()).toHaveValue('')
    await waitFor(() => expect(groupDisclosures().length).toBeGreaterThanOrEqual(4))
    expect(screen.queryByText(/^No tools match/)).toBeNull()
  })

  it('US-6 AS4 C-20: under a search the group Set all says Set 3 of 3 matching and changes exactly the three rows on screen', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await user.type(await search(), 'browser')

    const setAll = await screen.findByRole('button', { name: /^set 3 of 3 matching/i })
    await user.click(setAll)
    await user.click(await screen.findByRole('menuitem', { name: /^allow$/i }))

    await waitFor(() => expectPermissionPatches(permissions(), {
      policies: { browser_click: 'allow', browser_navigate: 'allow', browser_type: 'allow' },
    }))
    expect(permissions().stored.policies.bash, 'a tool that was not on screen is unchanged').toBe('allow')
    expect(permissions().stored.policies.fetch_url, 'a tool that was not on screen is unchanged').toBe('allow')
  })

  it('US-1 AS1 C-2: a click on Deny shows Deny, saves only that tool and shows Saved', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)
    const row = await screen.findByTestId('tool-row-bash')
    expect(pressedChoices(row), 'exactly one choice is pressed, and it is the stored one').toEqual(['Allow'])

    await user.click(within(row).getByRole('button', { name: 'Deny' }))

    await waitFor(() => expect(pressedChoices(row)).toEqual(['Deny']))
    await waitFor(() => expect(savedShown()).toBe(true))
    expectPermissionPatches(permissions(), { policies: { bash: 'deny' } })
  })

  it('S-69 US-10 AS4: Tab then Enter on the badges of bash moves focus to Ask, selects it and shows Saved', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)
    const row = await screen.findByTestId('tool-row-bash')
    const allow = within(row).getByRole('button', { name: 'Allow' })
    await waitFor(() => expect(allow).toBeEnabled())

    allow.focus()
    await user.tab()
    expect(within(row).getByRole('button', { name: 'Ask' }), 'focus moves to Ask').toHaveFocus()
    await user.keyboard('{Enter}')

    await waitFor(() => expect(pressedChoices(row)).toEqual(['Ask']))
    await waitFor(() => expect(savedShown(), 'Saved is shown after the change').toBe(true))
    expectPermissionPatches(permissions(), { policies: { bash: 'ask' } })
  })

  it('US-5 AS1 C-2: choosing a level from the menu changes only that tool, with one write', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^web & search/i)
    const row = await screen.findByTestId('tool-row-fetch_url')

    await user.click(within(row).getByRole('button', { name: /^What the model sees\b.*: Name only$/ }))
    await user.click(await screen.findByRole('menuitem', { name: /^Full\b/ }))

    await waitFor(() => expect(within(row).getByRole('button', { name: /^What the model sees\b.*: Full$/ })).toBeInTheDocument())
    expectPermissionPatches(permissions(), { visibility: { fetch_url: 'full' } })
  })

  it('S-51 US-6 AS7: a What the model sees filter shows the note that denied items are not loaded', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    const note = 'Denied items are not loaded, so this filter leaves them out. Use the Permission filter to find them.'
    await search()
    expect(screen.queryByText(note), 'no filter, no note').toBeNull()

    await user.click(await screen.findByRole('button', { name: /^what the model sees/i }))
    await user.click(await screen.findByRole('menuitem', { name: /^hidden/i }))

    expect(await screen.findByText(note)).toBeInTheDocument()
  })
})
