// SkillsToolsPage.levels.test.tsx — order 26 (S-37, S-39, S-44, S-51; C-6).
// Level controls on the rewritten page's tools tab, driven through the
// imported page with the network edge doubled (CHECK round 1 finding 2: the
// earlier file held only a file-existence guard):
//   S-37: opening a row's "What the model sees" offers Hidden / Name only /
//         Name and short description / Full, each with a one-line
//         explanation;
//   S-39: a locked row shows Full with the fixed cannot-change sentence and
//         no level control;
//   S-51: with "What the model sees" = Hidden the denied-items note shows;
//   S-44 control: no "Load tools on demand" note anywhere on the tabs.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
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
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec C-6, S-37, S-39, S-44, S-51, FR-006',
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
    fetchGlobalToolPolicies: vi.fn(),
    updateGlobalToolPolicies: vi.fn(),
    fetchSkillTrust: vi.fn(),
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

const SERVE_WEB: RegistryTool = {
  name: 'serve_web',
  description: 'Serve a web preview.',
  scope: 'general',
  category: 'web',
  source: 'builtin',
  visibility: 'name_and_description',
  visibility_locked: false,
  permission_fixed: false,
}
const ASK_USER: RegistryTool = {
  ...SERVE_WEB,
  name: 'ask_user_question',
  category: 'core',
  visibility: 'full',
  visibility_locked: true,
}
const HIDDEN_TOOL: RegistryTool = { ...SERVE_WEB, name: 'deep_dive', visibility: 'hidden' }

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
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([SERVE_WEB, ASK_USER, HIDDEN_TOOL])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({ policies: {} })
  vi.mocked(api.updateGlobalToolPolicies).mockResolvedValue({ policies: {} })
  vi.mocked(api.fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' } as never)
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

async function openToolsTabAndGroup(label: RegExp) {
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: /omnipus tools/i }))
  const trigger = screen
    .getAllByRole('button', { name: label })
    .find((b) => b.getAttribute('aria-expanded') !== undefined)
  if (trigger && trigger.getAttribute('aria-expanded') === 'false') await user.click(trigger)
}

describe('SkillsToolsPage — levels (order 26)', () => {
  it('S-37: the level control offers the four levels, each with a one-line explanation', async () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openToolsTabAndGroup(/^web/i)

    const row = screen.getByTestId('tool-row-serve_web')
    const user = userEvent.setup()
    await user.click(within(row).getByRole('button', { name: /what the model sees/i }))

    for (const [name, meaning] of [
      ['Hidden', /never sent/i],
      ['Name only', /the name, no description/i],
      ['Name and short description', /name and one line/i],
      ['Full', /whole definition/i],
    ] as const) {
      const option = screen.getByRole('menuitem', { name: new RegExp(name, 'i') })
      expect(option, `${name} carries its meaning`).toHaveTextContent(meaning.source)
    }
  })

  it('S-39: a locked row shows Full with the cannot-change sentence and no level control', async () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await openToolsTabAndGroup(/^general/i)

    const row = screen.getByTestId('tool-row-ask_user_question')
    expect(within(row).getByText(/Always loaded/i)).toBeInTheDocument()
    expect(
      within(row).getByText(
        'Always loaded: agents need this to work, so its level cannot be changed.',
      ),
    ).toBeInTheDocument()
    expect(
      within(row).queryByRole('button', { name: /what the model sees/i }),
    ).not.toBeInTheDocument()
  })

  it('S-51: the Hidden level filter shows the denied-items note', async () => {
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await user.click(screen.getByRole('tab', { name: /omnipus tools/i }))

    await user.click(screen.getByRole('button', { name: /what the model sees/i }))
    await user.click(screen.getByRole('menuitem', { name: /^hidden$/i }))

    expect(
      screen.getByText(
        'Denied items are not loaded, so this filter leaves them out. Use the Permission filter to find them.',
      ),
    ).toBeInTheDocument()
  })

  it('S-44 control: no "Load tools on demand" note remains on the tool tabs', async () => {
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await user.click(screen.getByRole('tab', { name: /omnipus tools/i }))

    expect(screen.queryByText(/load tools on demand/i)).not.toBeInTheDocument()
  })
})
