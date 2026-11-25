// SkillsToolsPage.search.test.tsx — order 27 (S-45 to S-50; FR-002, FR-004,
// FR-005). Search, filters and their effect on the list, driven through the
// imported page with the network edge doubled (CHECK round 1 finding 2: the
// earlier file held only a file-existence guard):
//   S-46: groups start collapsed; typing "browser" opens only the matching
//         group and only matching rows show;
//   S-47: Escape empties the field and all groups collapse again;
//   S-49: no match shows "No tools match "zzzz"" with "Clear search and
//         filters";
//   S-50: a row whose saved value no longer matches the filter leaves the
//         list, and clearing the filter shows it again;
//   S-48: the Set all control over a filtered list is named
//         "Set 1 of 1 matching…" and changes exactly the visible row.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { RegistryTool } from '@/lib/api'
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

    expect(screen.getByText('No tools match "zzzz"')).toBeInTheDocument()
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
