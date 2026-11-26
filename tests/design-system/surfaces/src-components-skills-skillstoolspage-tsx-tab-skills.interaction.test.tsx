// Surface src/components/skills/SkillsToolsPage.tsx#tab:skills — INTERACTION check.
// The unit check is ...-tab-skills.test.tsx; the browser check is
// tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109:
//   US-6 AS2 and AS3, S-46, S-47   groups start collapsed; typing opens the groups
//                                   with a match and shows only matching rows; Escape
//                                   in the field empties it and the groups show collapsed
//   US-6 AS5, S-49                  no match: "No skills match “zzzz”" and "Clear search
//                                   and filters" (the spec prints curly quotes around
//                                   zzzz, the page prints straight ones; either is
//                                   accepted here and the difference is reported)
//   US-6 AS4, S-48, C-20            Set all under a filter names "Set <k> of <n> matching"
//                                   and changes only the rows on screen
//   US-2 AS1, C-2                   a click on Deny changes that one skill and shows Saved
//   US-4 AS5                        trust Allow shows "Allow turns off the check for
//                                   unverified skills. Choose Ask to turn it back on."
//   Accessibility and Keyboard      tabs: arrow keys, Home, End (the order of the tabs is
//                                   read from the page, not assumed: the spec lists the
//                                   tabs in one order but never says they sit in it)
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
  groupDisclosures, openGroup, pressedChoices, renderPage, requireSkillsToolsPage, resetWorld, serveWorld, skill, world,
} from './skills-tools-harness'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: { weather: 'allow', 'notes-helper': 'deny' },
  visibility: {},
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec US-6, US-2 AS1, US-4 AS5, S-46 to S-49 and C-2')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  world.skills = [
    skill('weather', { category: 'tools' }),
    skill('notes-helper', { category: 'tools' }),
    skill('trip-notes', { category: 'lifestyle' }),
    skill('deep-research', { category: 'research' }),
  ]
})

const search = () => screen.findByRole('searchbox', { name: /search/i })
const shownRows = () => screen.queryAllByTestId(/^skill-row-/).map((row) => row.getAttribute('data-testid'))

describe('Skills tab surface interaction contract', () => {
  it('S-46 US-6 AS3: typing in the search field opens only the groups with a match and shows only the matching rows', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    const field = await search()
    await waitFor(() => expect(groupDisclosures()).toHaveLength(3))
    expect(shownRows(), 'groups start collapsed, so no row is drawn').toEqual([])

    await user.type(field, 'trip')

    await waitFor(() => expect(shownRows()).toEqual(['skill-row-trip-notes']))
    const open = groupDisclosures().filter((header) => header.getAttribute('aria-expanded') === 'true')
    expect(open.map((header) => header.textContent?.toLowerCase()), 'only the group with the match is open').toEqual([expect.stringMatching(/^lifestyle/)])
  })

  it('S-47 US-6 AS3: Escape in the search field empties it and every group shows collapsed again', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    const field = await search()
    await user.type(field, 'weather')
    await waitFor(() => expect(shownRows()).toEqual(['skill-row-weather']))

    field.focus()
    await user.keyboard('{Escape}')

    expect(field).toHaveValue('')
    await waitFor(() => expect(shownRows()).toEqual([]))
    for (const header of groupDisclosures()) expect(header).toHaveAttribute('aria-expanded', 'false')
  })

  it('S-49 US-6 AS5: a search with no match shows No skills match with Clear search and filters, and Clear brings the groups back', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await user.type(await search(), 'zzzz')

    // Spec S-49 prints curly quotes, the page straight ones: either is accepted (reported in the pack).
    expect(await screen.findByText(/^No skills match [“"]zzzz[”"]$/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Clear search and filters' }))

    expect(await search()).toHaveValue('')
    await waitFor(() => expect(groupDisclosures()).toHaveLength(3))
    expect(screen.queryByText(/^No skills match/)).toBeNull()
  })

  it('US-6 AS2: a group header opens its rows and the next press collapses them', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openGroup(/^tools/i)
    expect(await screen.findAllByTestId(/^skill-row-/)).toHaveLength(2)

    const header = groupDisclosures().find((button) => /^tools/i.test(button.textContent ?? ''))
    expect(header).toHaveAttribute('aria-expanded', 'true')
    await user.click(header as HTMLElement)

    await waitFor(() => expect(header).toHaveAttribute('aria-expanded', 'false'))
    expect(shownRows()).toEqual([])
  })

  it('US-2 AS1 C-2: a click on Deny changes that one skill, sends a write naming only it and shows Saved', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openGroup(/^tools/i)
    const row = await screen.findByTestId('skill-row-weather')
    expect(pressedChoices(row), 'weather is stored as Allow').toEqual(['Allow'])

    await user.click(within(row).getByRole('button', { name: 'Deny' }))

    await waitFor(() => expect(pressedChoices(row)).toEqual(['Deny']))
    await waitFor(() => expect(screen.getAllByText('Saved').length).toBeGreaterThan(0))
    expectPermissionPatches(permissions(), { skill_policies: { weather: 'deny' } })
    expect(pressedChoices(screen.getByTestId('skill-row-notes-helper')), 'a sibling skill is untouched').toEqual(['Deny'])
  })

  it('US-4 AS5: choosing Allow for unverified skills saves it and shows the warning, and choosing Ask takes the warning away', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    const block = await screen.findByTestId('trust-block')
    const warning = 'Allow turns off the check for unverified skills. Choose Ask to turn it back on.'
    expect(within(block).queryByText(warning), 'at Ask there is no warning').toBeNull()

    await user.click(within(block).getByRole('button', { name: 'Allow' }))
    expect(await within(block).findByText(warning)).toBeVisible()
    await waitFor(() => expect(within(block).getByRole('button', { name: 'Allow' })).toHaveAttribute('aria-pressed', 'true'))

    await user.click(within(block).getByRole('button', { name: 'Ask' }))
    await waitFor(() => expect(within(block).queryByText(warning)).toBeNull())
    expectPermissionPatches(permissions(), { skill_trust: 'allow' }, { skill_trust: 'ask' })
  })

  it('S-48 US-6 AS4 C-20: Set all under a Permission filter names the rows on screen and changes only them', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await user.click(await screen.findByRole('button', { name: /permission filter/i }))
    await user.click(await screen.findByRole('menuitem', { name: /^deny$/i }))

    // S-48's Given: of the stored values, one skill is at Deny; the accessible name starts "Set 1 of 1 matching".
    const setAll = await screen.findByRole('button', { name: /^set 1 of 1 matching/i })
    await user.click(setAll)
    await user.click(await screen.findByRole('menuitem', { name: /^allow$/i }))

    await waitFor(() => expectPermissionPatches(permissions(), { skill_policies: { 'notes-helper': 'allow' } }))
    expect(permissions().stored.skill_policies.weather, 'a skill that was not on screen is unchanged').toBe('allow')
  })

  it('Accessibility and Keyboard: the arrow keys, Home and End move between the tabs and select them', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await screen.findAllByRole('tab')
    const tabs = screen.getAllByRole('tab')
    expect(tabs).toHaveLength(3)
    const selected = () => screen.getAllByRole('tab', { selected: true })
    expect(selected()).toEqual([tabs[0]])

    tabs[0].focus()
    await user.keyboard('{End}')
    await waitFor(() => expect(selected()).toEqual([tabs[2]]))
    expect(tabs[2]).toHaveFocus()

    await user.keyboard('{ArrowLeft}')
    await waitFor(() => expect(selected()).toEqual([tabs[1]]))

    await user.keyboard('{Home}')
    await waitFor(() => expect(selected()).toEqual([tabs[0]]))
    expect(tabs[0]).toHaveFocus()

    await user.keyboard('{ArrowRight}')
    await waitFor(() => expect(selected()).toEqual([tabs[1]]))
  })
})
