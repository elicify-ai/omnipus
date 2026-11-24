// SkillsToolsPage.setAll.test.tsx — order 23 (S-02, S-05, S-06; FR-003,
// FR-010; Dataset D4). The rewritten Skills & Tools page's Set all
// behaviour, driven through the imported page with the network edge doubled
// (CHECK round 1 finding 2 / H1: the earlier file held only a file-existence
// guard that an empty placeholder passed):
//   S-02: Set all → Ask on the Files group header sets all its rows to Ask,
//         sends exactly one save request, and no confirm dialog appears;
//   S-05: Set all → Allow on the Plans group (containing execute_plan at
//         Ask) shows "Confirm autonomous plan execution" BEFORE anything is
//         saved; Cancel leaves every row unchanged;
//   S-06: Set all → Deny on a group containing Tool search denies the other
//         rows; Tool search keeps its fixed sentence and no badges.
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
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-003, FR-010, S-02, S-05, S-06 and Dataset D4',
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
  policies: Object.fromEntries([
    ...FILE_TOOLS.map(({ name }) => [name, 'deny'] as const),
    ['execute_plan', 'ask'], ['fetch_url', 'ask'], ['ToolSearch', 'allow'],
  ]),
  skill_policies: {},
  visibility: Object.fromEntries([...FILE_TOOLS, ...PLANS_TOOLS, ...GENERAL_TOOLS].map(({ name }) => [name, 'full'])),
  skill_visibility: {}, skill_trust: 'ask',
}))

function makeTool(name: string, category: string, overrides: Partial<RegistryTool> = {}): RegistryTool {
  return {
    name,
    description: `${name} description`,
    scope: 'general',
    category,
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
    ...overrides,
  }
}

const FILE_TOOLS = Array.from({ length: 12 }, (_, i) => makeTool(`file_tool_${i + 1}`, 'file'))
const PLANS_TOOLS = [makeTool('execute_plan', 'plans')]
const GENERAL_TOOLS = [
  makeTool('fetch_url', 'core'),
  makeTool('ToolSearch', 'core', {
    permission_fixed: true,
    visibility_locked: true,
    visibility: 'full',
  }),
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

async function mountPage() {
  render(<SkillsToolsPage />, { wrapper: pageWrapper })
  await userEvent.setup().click(await screen.findByRole('tab', { name: /omnipus tools/i }))
}

async function openGroup(label: RegExp) {
  const user = userEvent.setup()
  const trigger = screen
    .getAllByRole('button', { name: label })
    .find((b) => b.getAttribute('aria-expanded') !== undefined)
  if (trigger && trigger.getAttribute('aria-expanded') === 'false') await user.click(trigger)
}

async function useSetAll(label: RegExp, choice: 'Ask' | 'Allow' | 'Deny') {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: label }))
  await user.click(screen.getByRole('menuitem', { name: choice }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchSkills).mockResolvedValue([])
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([...FILE_TOOLS, ...PLANS_TOOLS, ...GENERAL_TOOLS])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

describe('SkillsToolsPage — Set all (order 23)', () => {
  it('S-02: Set all Ask on the Files group sets all 12 rows, saves once, no confirm', async () => {
    await mountPage()
    await openGroup(/^files/i)
    await useSetAll(/^set all/i, 'Ask')

    for (const tool of FILE_TOOLS) {
      const row = screen.getByTestId(`tool-row-${tool.name}`)
      expect(within(row).getByRole('button', { name: /^ask$/i })).toHaveAttribute('aria-pressed', 'true')
    }
    await waitFor(() => expectPermissionPatches(permissions(), {
      policies: Object.fromEntries(FILE_TOOLS.map(({ name }) => [name, 'ask'])),
    })) // exactly one per-key request; no unrelated group or fixed key
    expect(screen.queryByText(/confirm/i)).not.toBeInTheDocument() // no confirm dialog
  })

  it('S-05: Set all Allow on Plans asks "Confirm autonomous plan execution" first; Cancel changes nothing', async () => {
    const user = userEvent.setup()
    await mountPage()
    await openGroup(/^plans/i)
    await useSetAll(/^set all/i, 'Allow')

    // The confirm appears BEFORE anything is saved.
    expect(screen.getByText('Confirm autonomous plan execution')).toBeInTheDocument()
    expectPermissionPatches(permissions())

    await user.click(screen.getByRole('button', { name: /^cancel$/i }))
    const row = screen.getByTestId('tool-row-execute_plan')
    expect(within(row).getByRole('button', { name: /^ask$/i })).toHaveAttribute('aria-pressed', 'true')
    expectPermissionPatches(permissions())
  })

  it('S-06: Set all Deny on the group containing Tool search denies the others; Tool search keeps its fixed text', async () => {
    await mountPage()
    await openGroup(/^general/i)
    await useSetAll(/^set all/i, 'Deny')

    const row = screen.getByTestId('tool-row-fetch_url')
    expect(within(row).getByRole('button', { name: /^deny$/i })).toHaveAttribute('aria-pressed', 'true')

    await waitFor(() => expectPermissionPatches(permissions(), { policies: { fetch_url: 'deny' } }))
    const searchRow = screen.getByTestId('tool-row-ToolSearch')
    expect(
      within(searchRow).getByText(
        'Always available for discovery. The tools it finds still follow their own permissions.',
      ),
    ).toBeInTheDocument()
    for (const policy of ['allow', 'ask', 'deny']) {
      expect(within(searchRow).queryByRole('button', { name: new RegExp(`^${policy}$`, 'i') })).not.toBeInTheDocument()
    }
  })
})
