// SkillsToolsPage.focus.test.tsx — order 45 (S-100). Focus management after
// the focused row leaves a filtered list, driven through the imported page
// with the network edge doubled (CHECK round 1 finding 2: the earlier file
// held only a file-existence guard with no focus/status assertion).
//
// Oracle: skills-tools-redesign-spec.md S-100 — "Given Permission filter =
// Ask showing 3 rows, focus on the Deny badge of the last row; When the
// person presses Enter and the save is acknowledged; Then focus moves to
// the Deny badge of the previous row; And the status region announces
// '<name> set to Deny. It no longer matches the filter. 2 shown.'"
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
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec S-100',
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
  policies: { alpha_tool: 'ask', beta_tool: 'ask', gamma_tool: 'ask' },
  skill_policies: {}, visibility: { alpha_tool: 'full', beta_tool: 'full', gamma_tool: 'full' },
  skill_visibility: {}, skill_trust: 'ask',
}))

function askTool(name: string): RegistryTool {
  return {
    name,
    description: `${name} description`,
    scope: 'general',
    category: 'web',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  }
}

// S-100's Given: the Ask filter shows exactly three rows.
const TOOLS = [askTool('alpha_tool'), askTool('beta_tool'), askTool('gamma_tool')]

function pageWrapper({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      {children}
    </QueryClientProvider>
  )
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

describe('SkillsToolsPage — focus after the focused row leaves a filtered list (order 45)', () => {
  it('S-100: Enter on the last row moves focus to the previous row and announces the result', async () => {
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await user.click(screen.getByRole('tab', { name: /omnipus tools/i }))

    // Permission filter = Ask showing the three rows.
    await user.click(screen.getByRole('button', { name: /permission filter/i }))
    await user.click(screen.getByRole('menuitem', { name: /^ask$/i }))
    for (const name of TOOLS.map((t) => t.name)) {
      expect(screen.getByTestId(`tool-row-${name}`)).toBeInTheDocument()
    }

    // Focus the Deny badge of the LAST row, then press Enter (the person's
    // activation key on a focused button).
    const lastRow = screen.getByTestId('tool-row-gamma_tool')
    const deny = within(lastRow).getByRole('button', { name: /^deny$/i })
    deny.focus()
    await user.keyboard('{Enter}')

    // The save is acknowledged, the row leaves the list, and focus moves to
    // the Deny badge of the previous row.
    await waitFor(() => {
      expect(screen.queryByTestId('tool-row-gamma_tool')).not.toBeInTheDocument()
    })
    const previousRow = screen.getByTestId('tool-row-beta_tool')
    await waitFor(() => {
      expect(within(previousRow).getByRole('button', { name: /^deny$/i })).toHaveFocus()
    })

    expectPermissionPatches(permissions(), { policies: { gamma_tool: 'deny' } })

    // The status region announces the exact sentence (S-100's Then).
    // Every catalogued Button adds its own empty role=status region
    // (button.tsx::ActionAnnouncement), so a conforming page holds several
    // status regions and a singular unscoped query cannot be the oracle
    // (CHECK round 4, candidate 14). The announcement is identified by its
    // content, and exactly one region may carry it.
    const sentence = 'gamma_tool set to Deny. It no longer matches the filter. 2 shown.'
    await waitFor(() => {
      const carrying = screen
        .getAllByRole('status')
        .filter((region) => region.textContent?.trim() === sentence)
      expect(carrying, `status regions carrying the S-100 sentence: ${carrying.length}`).toHaveLength(1)
    })
    const status = screen
      .getAllByRole('status')
      .find((region) => region.textContent?.trim() === sentence)
    // Spec accessibility section: "Save signal: polite status region". role=status
    // is the implicit polite live region; an explicit aria-live may not turn it
    // assertive.
    expect(status).toBeDefined()
    expect(status).not.toHaveAttribute('aria-live', 'assertive')
  })
})
