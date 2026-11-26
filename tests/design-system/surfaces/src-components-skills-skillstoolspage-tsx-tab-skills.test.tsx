// Surface src/components/skills/SkillsToolsPage.tsx#tab:skills — UNIT check.
// Design-system catalog (scripts/design-system/catalog.mjs): a tab surface needs a
// unit, an interaction and a browser check. This file is the unit check; the
// interaction check is ...-tab-skills.interaction.test.tsx and the browser check is
// tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f: it states what
// the spec requires of the surface and describes behaviour that head already has, so it
// is GREEN on arrival. It is not a RED proof, and that it can fail (mutation, proof of
// failability) is deferred to CHECK.
//
// Oracles are the spec's own words (docs/internal/specs/skills-tools-redesign-spec.md
// at b27c64109): S-45 and User Story 6 AS1 (tab names carry counts), S-57 and US-6
// AS2 / US-7 AS7 (grouping, counts, collapsed, no empty category), S-26 and US-4 AS1
// (the Unverified skills block comes first with all three meanings visible), S-66 and
// US-10 AS1 (loading), the "Skills tab" row of "UI Screens and States" (empty, error,
// partial) and S-131 (unfinished upgrade). No expected value was read from the page.
//
// Asserted elsewhere (Part H, rulings F3 and F4): the ORDER of the tabs, Skills,
// External tools (MCP), Omnipus tools (S-45, US-6 AS1, FR-001), is pinned in
// src/components/skills/SkillsToolsPage.search.test.tsx, so the tab readings here are
// compared as a set; the curly quotes of the no-match sentence (S-49, FR-004) are
// asserted in the interaction file of this surface.
//
// Only the network edge is doubled: the list requests the page makes, and the
// permissions endpoint at fetch (tests/helpers/skill-permission-network.ts).
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
  NEVER, groupDisclosures, groupName, headerReadings, openGroup, reading, renderPage, requireSkillsToolsPage,
  resetWorld, serveWorld, service, serviceTools, skill, tool, world,
} from './skills-tools-harness'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: { weather: 'allow' },
  visibility: {},
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec S-45, S-57, S-26, S-66, S-131 and the "Skills tab" row of UI Screens and States')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
})

const CHOICES = /^(allow|ask|deny)$/i

describe('Skills tab surface unit contract', () => {
  it('S-45 US-6 AS1: the page opens on Skills and every tab name carries its count', async () => {
    // S-45's Given: 53 skills, 3 services with 86 tools, 114 Omnipus tools.
    world.skills = Array.from({ length: 53 }, (_, i) => skill(`skill-${i}`))
    const builtin = Array.from({ length: 114 }, (_, i) => tool(`tool_${i}`))
    const external = [
      serviceTools('alpha', Array.from({ length: 29 }, (_, i) => `a${i}`)),
      serviceTools('beta', Array.from({ length: 29 }, (_, i) => `b${i}`)),
      serviceTools('gamma', Array.from({ length: 28 }, (_, i) => `c${i}`)),
    ]
    world.tools = [...builtin, ...external.flat()]
    world.servers = [service('alpha', external[0]), service('beta', external[1]), service('gamma', external[2])]
    renderPage(Page)

    // S-45's Then: the three readings, with Skills selected.
    const expected = ['External tools (MCP) 86', 'Omnipus tools 114', 'Skills 53']
    await waitFor(() => expect(screen.getAllByRole('tab').map(reading).sort()).toEqual(expected))
    expect(screen.getAllByRole('tab', { selected: true }).map(reading)).toEqual(['Skills 53'])
  })

  it('S-57 US-6 AS2 US-7 AS7: skills are grouped by category with counts, and an empty category is not listed', async () => {
    // S-57's Given: skills in Research (1), Tools (12) and none in Blockchain, plus 31 without a category.
    world.skills = [
      skill('research-one', { category: 'research' }),
      ...Array.from({ length: 12 }, (_, i) => skill(`tool-${i}`, { category: 'tools' })),
      ...Array.from({ length: 31 }, (_, i) => skill(`other-${i}`, { category: 'uncategorised' })),
    ]
    renderPage(Page)

    await waitFor(() => expect(headerReadings()).toHaveLength(3))
    expect(headerReadings().sort()).toEqual(['research 1', 'tools 12', 'uncategorised 31'])
    // S-57's But: no Blockchain group. The same query finds the Research group, so it can see a name.
    expect(screen.getByRole('button', { name: /^research/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^blockchain/i })).toBeNull()
  })

  // Part H (rulings F5, F6; team-lead's wording pin). The spec's Assumptions (line 3326 at b27c64109) write
  // the categories of the built-in skills as "Tools", "Business", "Research", "Testing & Security" and
  // "Content & Media"; US-7 AS7 and S-104 write "Uncategorised". S-57 prints its group headers in capitals
  // (RESEARCH 1, TOOLS 12, UNCATEGORISED 31): that is the look, and the capitals come from CSS (ruling F5), so
  // the text of the name is compared as the spec spells it in prose, ampersand included. The ids are the
  // SkillCategory values of the contract. Only these six names are pinned. The spec also writes Lifestyle
  // (S-17, S-98, S-104) and Blockchain (S-57), for which this fixture has no skill, and writes no display name
  // at all for five of the 13 ids (development, data_ai, devops, documentation, databases); that is reported
  // as a spec gap in the pack.
  it('Spec line 3326 and S-57: the category names the spec writes are the names a group header shows, ampersand included', async () => {
    world.skills = [
      skill('one-tools', { category: 'tools' }),
      skill('one-business', { category: 'business' }),
      skill('one-research', { category: 'research' }),
      skill('one-testing', { category: 'testing_security' }),
      skill('one-content', { category: 'content_media' }),
      skill('one-none', { category: 'uncategorised' }),
    ]
    renderPage(Page)

    await waitFor(() => expect(groupDisclosures()).toHaveLength(6))
    expect(groupDisclosures().map(groupName).sort()).toEqual(['Business', 'Content & Media', 'Research', 'Testing & Security', 'Tools', 'Uncategorised'])
  })

  it('US-6 AS2: category groups start collapsed and draw no skill row until one is opened', async () => {
    world.skills = [skill('alpha', { category: 'research' }), skill('beta', { category: 'tools' })]
    renderPage(Page)

    await waitFor(() => expect(groupDisclosures()).toHaveLength(2))
    for (const header of groupDisclosures()) expect(header, `group ${reading(header)} starts collapsed`).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryAllByTestId(/^skill-row-/)).toHaveLength(0)

    // Control: the same query sees a row once its group opens.
    await openGroup(/^research/i)
    expect(await screen.findAllByTestId(/^skill-row-/)).toHaveLength(1)
  })

  it('S-26 US-4 AS1: Unverified skills is the first block with Allow, Ask, Deny in order and all three meanings visible', async () => {
    world.skills = [skill('weather')]
    renderPage(Page)

    const block = await screen.findByTestId('trust-block')
    const search = await screen.findByLabelText('Search skills')
    // First block: it comes before the search field in the page.
    expect(block.compareDocumentPosition(search) & Node.DOCUMENT_POSITION_FOLLOWING, 'the block precedes the search field').toBeTruthy()
    expect(within(block).getByText('Unverified skills')).toBeInTheDocument()
    expect(within(block).getAllByRole('button', { name: CHOICES }).map((button) => button.textContent)).toEqual(['Allow', 'Ask', 'Deny'])
    // S-26's And: the three lines, each visible at once.
    for (const line of [
      'Unverified skills install and load without a check.',
      'Installing or loading an unverified skill asks you first.',
      'Unverified skills cannot be installed or loaded.',
    ]) expect(within(block).getByText(line)).toBeVisible()
  })

  it('S-66 US-10 AS1: while skills load the tab shows Loading skills and skeleton rows, the count reads an ellipsis, and search, filters and badges are disabled', async () => {
    vi.mocked(api.fetchSkills).mockImplementation(NEVER)
    renderPage(Page)

    expect(await screen.findByText('Loading skills…')).toBeInTheDocument()
    expect(screen.getAllByTestId('skeleton-row').length, 'skeleton rows show').toBeGreaterThan(0)
    expect(screen.getByRole('tab', { name: /^skills/i }).textContent?.replace(/\s+/g, '')).toBe('Skills…')
    expect(screen.getByLabelText('Search skills')).toBeDisabled()
    for (const filter of [/^permission/i, /^what the model sees/i]) {
      for (const button of screen.getAllByRole('button', { name: filter })) expect(button, `filter ${filter} is disabled`).toBeDisabled()
    }
    for (const badge of screen.queryAllByRole('button', { name: CHOICES })) expect(badge, 'no badge is enabled while loading').toBeDisabled()
  })

  it('UI states Empty: with no skill the trust block stays and the tab says No skills installed with Browse the catalog', async () => {
    renderPage(Page)

    expect(await screen.findByText('No skills installed')).toBeInTheDocument()
    expect(within(await screen.findByTestId('trust-block')).getByText('Unverified skills')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Browse the catalog' })).toBeEnabled()
    expect(screen.queryByText('Loading skills…')).toBeNull()
  })

  it('UI states Error: a failed skills request shows its cause and Retry, offers no enabled badge and writes nothing', async () => {
    const cause = 'The skills folder could not be read'
    vi.mocked(api.fetchSkills).mockRejectedValue(new ApiError(500, cause))
    renderPage(Page)

    expect(await screen.findByText(cause)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    for (const badge of screen.queryAllByRole('button', { name: CHOICES })) expect(badge, 'no badge is enabled after a failed load').toBeDisabled()
    expect(permissions().patches(), 'nothing was written').toEqual([])
  })

  it('UI states Partial: when the list loads but permissions do not, rows stay read-only and the tab says Permissions could not be loaded with Retry', async () => {
    world.skills = [skill('weather')]
    permissions().failNextRead(500, 'The settings file could not be read')
    renderPage(Page)
    await openGroup(/^tools/i)

    expect(await screen.findByText('Permissions could not be loaded.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    const row = await screen.findByTestId('skill-row-weather')
    for (const badge of within(row).queryAllByRole('button', { name: CHOICES })) expect(badge, 'a row is read-only without permissions').toBeDisabled()
    expect(permissions().patches(), 'nothing was written').toEqual([])
  })

  it('S-131 UI states Partial: an unfinished upgrade names the agents and says their old skill limits still apply', async () => {
    world.skills = [skill('weather')]
    // C-2 PermissionSettings.upgrade_unfinished: two entries, served by the permissions endpoint.
    ;(permissions().stored as unknown as Record<string, unknown>).upgrade_unfinished = [
      { agent_id: 'mia', reason: 'write_failed' },
      { agent_id: 'jim', reason: 'unreadable' },
    ]
    vi.mocked(api.fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'custom', status: 'active' },
      { id: 'jim', name: 'Jim', type: 'custom', status: 'active' },
    ] as unknown as Awaited<ReturnType<typeof api.fetchAgents>>)
    renderPage(Page)

    // S-131, verbatim.
    expect(
      await screen.findByText(
        'The upgrade has not finished for 2 agents: Mia (its settings file could not be saved), Jim (its settings file could not be read). Their old skill limits still apply.',
        { exact: false },
      ),
    ).toBeInTheDocument()
  })

  it('S-131 control: with no unfinished upgrade the Skills tab shows no upgrade notice', async () => {
    world.skills = [skill('weather')]
    renderPage(Page)

    // Settled: the permissions were read and the tab shows its group.
    await waitFor(() => expect(permissions().seen.filter(({ method }) => method === 'GET').length).toBeGreaterThan(0))
    await waitFor(() => expect(headerReadings()).toEqual(['tools 1']))
    expect(screen.queryByText(/The upgrade has not finished/)).toBeNull()
  })
})
