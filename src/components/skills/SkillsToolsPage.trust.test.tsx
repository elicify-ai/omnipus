// SkillsToolsPage.trust.test.tsx — order 25 (S-26, S-32; FR-027, C-3). The
// trust block on the Skills tab, driven through the imported page with the
// network edge doubled (CHECK round 1 finding 2: the earlier file held only
// a file-existence guard):
//   S-26: "Unverified skills" is the first block, Allow / Ask / Deny in that
//         order, with all three meaning lines visible;
//   S-32: choosing Allow shows the warning callout "Allow turns off the
//         check for unverified skills. Choose Ask to turn it back on.";
//   migration control: a warn_unverified stored value renders Ask (the
//         legacy values are carried over, S-33's screen half).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
// The pack's BLOCKED seam (support.tsx::requireModule convention): the
// missing module is a named, loud failure — the acceptance oracles are the
// interaction tests below.
import type { ComponentType } from 'react'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-027, C-3, S-26, S-32',
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
    updateSkillTrust: vi.fn(),
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
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({ policies: {} })
  vi.mocked(api.updateGlobalToolPolicies).mockResolvedValue({ policies: {} })
  vi.mocked(api.fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' } as never)
  vi.mocked(api.updateSkillTrust).mockResolvedValue({ level: 'allow_all' } as never)
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

function trustBlock(): HTMLElement {
  // FR-027: the trust block is FIRST on the Skills tab.
  const block = screen.getByTestId('trust-block')
  expect(block.parentElement?.firstElementChild).toBe(block)
  return block
}

describe('SkillsToolsPage — trust (order 25)', () => {
  it('S-26: the first block is "Unverified skills" with Allow/Ask/Deny in order and all three meanings', () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = trustBlock()

    expect(within(block).getByText('Unverified skills')).toBeInTheDocument()
    const buttons = within(block).getAllByRole('button', { name: /^(allow|ask|deny)$/i })
    expect(buttons.map((b) => b.textContent?.toLowerCase())).toEqual(['allow', 'ask', 'deny'])
    expect(
      within(block).getByText('Unverified skills install and load without a check.'),
    ).toBeInTheDocument()
    expect(
      within(block).getByText('Installing or loading an unverified skill asks you first.'),
    ).toBeInTheDocument()
    expect(within(block).getByText('Unverified skills cannot be installed or loaded.')).toBeInTheDocument()
  })

  it('S-32: choosing Allow in the trust block shows the warning callout', async () => {
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = trustBlock()

    await user.click(within(block).getByRole('button', { name: /^allow$/i }))

    expect(
      within(block).getByText(
        'Allow turns off the check for unverified skills. Choose Ask to turn it back on.',
      ),
    ).toBeInTheDocument()
    expect(api.updateSkillTrust).toHaveBeenCalledWith('allow_all')
  })

  it('migration control: a warn_unverified stored value renders Ask', () => {
    vi.mocked(api.fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' } as never)
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = trustBlock()
    expect(within(block).getByRole('button', { name: /^ask$/i })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
  })
})
