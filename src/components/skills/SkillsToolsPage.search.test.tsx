// SkillsToolsPage.search.test.tsx — order 27 (S-45 to S-50; FR-002, FR-004,
// FR-005). Search, filters and their effect on the list, driven through the
// imported page with the network edge doubled (CHECK round 1 finding 2: the
// earlier file held only a file-existence guard):
//   S-46: groups start collapsed; typing "browser" opens only the matching
//         group and only matching rows show;
//   S-47: Escape empties the field and all groups collapse again;
//   S-49: no match shows `No tools match “zzzz”` (curly quotes, as the spec
//         writes them) with "Clear search and filters";
//   S-50: a row whose saved value no longer matches the filter leaves the
//         list, and clearing the filter shows it again;
//   S-48: the Set all control over a filtered list is named
//         "Set 1 of 1 matching…" and changes exactly the visible row.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { McpServer, RegistryTool, Skill } from '@/lib/api'
// The pack's BLOCKED seam (support.tsx::requireModule convention): the
// missing module is a named, loud failure — the acceptance oracles are the
// interaction tests below.
import type { ComponentType } from 'react'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-002, FR-004, FR-005, S-45 to S-50',
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
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

import * as api from '@/lib/api'
import { expectPermissionPatches, useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

const permissions = useSkillPermissionNetwork(() => ({
  policies: { browser_navigate: 'ask', browser_click: 'allow', fetch_url: 'allow' },
  skill_policies: {},
  visibility: { browser_navigate: 'full', browser_click: 'full', fetch_url: 'full' },
  skill_visibility: {}, skill_trust: 'ask',
}))

function tool(name: string, category: string): RegistryTool {
  return {
    name,
    description: `${name} description`,
    scope: 'general',
    category,
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  }
}

const TOOLS = [
  tool('browser_navigate', 'browser'),
  tool('browser_click', 'browser'),
  tool('fetch_url', 'web'),
]

function pageWrapper({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      {children}
    </QueryClientProvider>
  )
}

async function openToolsTab() {
  render(<SkillsToolsPage />, { wrapper: pageWrapper })
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: /omnipus tools/i }))
  return user
}

async function typeSearch(text: string) {
  const user = await openToolsTab()
  await user.type(screen.getByRole('searchbox', { name: /search/i }), text)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchSkills).mockResolvedValue([])
  vi.mocked(api.fetchRegistryTools).mockResolvedValue(TOOLS)
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

describe('SkillsToolsPage — search and filters (order 27)', () => {
  it('BLOCKED diagnostic: the page component exists', () => {
    // CHECK round 1 finding 2: existence is a diagnostic, never the
    // acceptance oracle — the interaction tests below are. Observed
    // 2026-11-22: this file's static import of the missing './SkillsToolsPage'
    // resolved silently under vitest's transform (five locator failures, no
    // resolution error), so the diagnostic keeps the named-missing RED
    // reason even then.
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-002, FR-004, FR-005, S-45 to S-50',
    ).toContain('/src/components/skills/SkillsToolsPage.tsx')
  })

  it('S-46: groups start collapsed; a search opens only the matching group and rows', async () => {
    const user = await openToolsTab()
    expect(screen.queryByTestId('tool-row-browser_navigate')).not.toBeInTheDocument() // collapsed

    await user.type(screen.getByRole('searchbox', { name: /search/i }), 'browser')

    expect(screen.getByTestId('tool-row-browser_navigate')).toBeInTheDocument()
    expect(screen.getByTestId('tool-row-browser_click')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-row-fetch_url')).not.toBeInTheDocument()
  })

  it('S-47: Escape empties the search and all groups collapse again', async () => {
    const user = await openToolsTab()
    const field = screen.getByRole('searchbox', { name: /search/i })
    await user.type(field, 'browser')
    expect(screen.getByTestId('tool-row-browser_navigate')).toBeInTheDocument()

    field.focus()
    await user.keyboard('{Escape}')

    expect(field).toHaveValue('')
    expect(screen.queryByTestId('tool-row-browser_navigate')).not.toBeInTheDocument()
  })

  it('S-49: no match shows the empty state with its clear control', async () => {
    await typeSearch('zzzz')

    // Oracle: spec S-49 / US-6 AS5 write the message with typographic (curly) quotes,
    // U+201C and U+201D around the term — `No tools match “zzzz”`. The earlier version of
    // this assertion pinned straight quotes, which is the page's former behaviour and
    // not the spec's; architect ruling F4 (arch/rulings-partG-F3-F6.md) and team-lead
    // (2026-11-26) require the curly pair. getByText matches the whole text, so a
    // straight-quote rendering fails here.
    expect(screen.getByText('No tools match “zzzz”')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Clear search and filters' })).toBeInTheDocument()
  })

  it('S-50: a row whose new value no longer matches the filter leaves the list; clearing shows it', async () => {
    permissions().stored.policies = { browser_navigate: 'ask', browser_click: 'allow', fetch_url: 'allow' }
    const user = await openToolsTab()
    await user.click(screen.getByRole('button', { name: /permission filter/i }))
    await user.click(screen.getByRole('menuitem', { name: /^ask$/i }))

    const row = screen.getByTestId('tool-row-browser_navigate')
    await user.click(within(row).getByRole('button', { name: /^deny$/i }))

    await waitFor(() => {
      expect(screen.queryByTestId('tool-row-browser_navigate')).not.toBeInTheDocument()
    })
    await user.click(screen.getByRole('button', { name: /permission filter/i }))
    // The clear option is the item named exactly "All". The old pattern /all/i
    // also matches the "Allow" item, so getByRole could not tell them apart
    // (CHECK round 4, candidate 13). The spec (S-50: "clearing the filter") does
    // not name the item's label; the anchored word keeps the intent — the one
    // option that clears the permission filter — without matching "Allow".
    await user.click(screen.getByRole('menuitem', { name: /^all$/i }))
    expect(screen.getByTestId('tool-row-browser_navigate')).toBeInTheDocument()
    expect(
      within(screen.getByTestId('tool-row-browser_navigate')).getByRole('button', { name: /^deny$/i }),
    ).toHaveAttribute('aria-pressed', 'true')
    expectPermissionPatches(permissions(), { policies: { browser_navigate: 'deny' } })
  })

  it('S-48: Set all over a filter names "<k> of <n> matching" and changes exactly the visible row', async () => {
    // Skills tab, Permission filter = Deny, 1 of 1 skill at Deny (S-48's
    // shape, with the fixture's single skill).
    vi.mocked(api.fetchSkills).mockResolvedValue([
      {
        id: 'notes-helper',
        name: 'notes-helper',
        version: '1.0.0',
        category: 'tools',
        verified: true,
        status: 'active',
        has_local_changes: false,
        trust_approved: true,
        revision: '0'.repeat(64),
      } as never,
    ])
    permissions().stored.skill_policies = { 'notes-helper': 'deny' }
    permissions().stored.skill_visibility = { 'notes-helper': 'name_and_description' }
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await user.click(screen.getByRole('tab', { name: /^skills/i }))
    await user.click(screen.getByRole('button', { name: /permission filter/i }))
    await user.click(screen.getByRole('menuitem', { name: /^deny$/i }))

    const setAll = screen.getByRole('button', { name: /^set 1 of 1 matching/i })
    await user.click(setAll)
    await user.click(screen.getByRole('menuitem', { name: /^allow$/i }))

    await waitFor(() => expectPermissionPatches(permissions(), { skill_policies: { 'notes-helper': 'allow' } }))
    expect(permissions().stored.policies).toEqual({ browser_navigate: 'ask', browser_click: 'allow', fetch_url: 'allow' })
  })
})

// S-45 / User Story 6 AS1 / FR-001: the page opens on Skills and every tab says how
// many it holds. Added in Skills & Tools fix round 4 (item 22): this file's header
// has always named S-45, but its first case was S-46, and the retired
// SkillCard.test case 'renders Skills & Tools heading and tabs' (the old screen's
// "Installed Skills", "MCP Servers" and "Built-in Tools") had no counterpart on the
// page. Two data sets: the spec's own numbers, and smaller ones, so a label with
// fixed numbers cannot pass both. The ORDER is asserted too (Part H, architect ruling
// F3): User Story 6 AS1 and FR-001 print "three tabs with counts — Skills, External
// tools (MCP), Omnipus tools", the same order S-45 prints its three readings in. The
// first version of this test compared the readings sorted, so a page that drew Omnipus
// tools before External tools passed it. Whitespace between a tab's name and its count
// is layout, not text.
describe('SkillsToolsPage — the page opens on Skills with counts (S-45)', () => {
  function skillRecord(i: number): Skill {
    return {
      has_local_changes: false, id: `skill-${i}`, name: `skill-${i}`, description: `Skill ${i}.`,
      source: 'global', status: 'active', verified: true, version: '1.0.0',
      revision: 'a'.repeat(64), category: 'tools', trust_approved: false,
    }
  }
  function squeeze(text: string): string {
    return text.replace(/\s+/g, '')
  }
  function reading(tab: HTMLElement): string {
    return squeeze(tab.textContent ?? '')
  }

  it.each([
    {
      label: "the spec's numbers (53 skills, 3 services with 86 tools, 114 Omnipus tools)",
      skillCount: 53, serviceSizes: [30, 28, 28], omnipusCount: 114,
      tabs: ['Skills 53', 'External tools (MCP) 86', 'Omnipus tools 114'],
    },
    {
      label: 'other numbers (2 skills, 1 service with 3 tools, 5 Omnipus tools)',
      skillCount: 2, serviceSizes: [3], omnipusCount: 5,
      tabs: ['Skills 2', 'External tools (MCP) 3', 'Omnipus tools 5'],
    },
  ])('S-45: with $label the tabs read $tabs, in this order, and Skills is selected', async ({ skillCount, serviceSizes, omnipusCount, tabs }) => {
    const omnipus = Array.from({ length: omnipusCount }, (_, i) => tool(`omnipus_tool_${i}`, `group_${i % 7}`))
    const servers: McpServer[] = []
    const external: RegistryTool[] = []
    serviceSizes.forEach((size, index) => {
      const id = `service-${index + 1}`
      const names = Array.from({ length: size }, (_, j) => `mcp_${id}_tool_${j}`)
      names.forEach((name) => external.push({ ...tool(name, 'search'), source: 'mcp', server_id: id }))
      servers.push({ id, name: id, transport: 'sse', status: 'connected', enabled: true, tool_count: size, tools: names })
    })
    vi.mocked(api.fetchSkills).mockResolvedValue(Array.from({ length: skillCount }, (_, i) => skillRecord(i)))
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([...omnipus, ...external])
    vi.mocked(api.fetchMcpServers).mockResolvedValue(servers)
    // The stored settings name every tool and skill the page lists, so no row is
    // short of a value for a reason other than the one under test.
    const policies: Record<string, 'ask'> = {}
    const visibility: Record<string, 'full'> = {}
    for (const entry of [...omnipus, ...external]) {
      policies[entry.name] = 'ask'
      visibility[entry.name] = 'full'
    }
    const skillPolicies: Record<string, 'ask'> = {}
    const skillVisibility: Record<string, 'name_and_description'> = {}
    for (let i = 0; i < skillCount; i += 1) {
      skillPolicies[`skill-${i}`] = 'ask'
      skillVisibility[`skill-${i}`] = 'name_and_description'
    }
    permissions().stored.policies = policies
    permissions().stored.visibility = visibility
    permissions().stored.skill_policies = skillPolicies
    permissions().stored.skill_visibility = skillVisibility

    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    // "Skills is selected" waits for no count, so it is asked first: a wrong tab
    // label below cannot stop this check from running.
    const skillsTab = await screen.findByRole('tab', { name: /^skills/i })
    const selected = screen.getAllByRole('tab').filter((tab) => tab.getAttribute('aria-selected') === 'true')
    expect(selected, 'exactly one tab is selected, and it is Skills').toEqual([skillsTab])

    await waitFor(
      () => {
        const found = screen.getAllByRole('tab').map(reading).sort()
        expect(found, `the three tabs, each with its count (expected ${tabs.join(' | ')})`).toEqual(tabs.map(squeeze).sort())
      },
      { timeout: 4000 },
    )
    // US-6 AS1 / FR-001: the tabs sit in the order the spec lists them (document order is the
    // order a person reads them and the order Tab and the arrow keys walk them).
    expect(
      screen.getAllByRole('tab').map(reading),
      `the tabs sit in the order Skills, External tools (MCP), Omnipus tools (expected ${tabs.join(' | ')})`,
    ).toEqual(tabs.map(squeeze))
  })
})
