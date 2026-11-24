// SkillsToolsPage.trust.test.tsx — order 25 (S-26, S-32; FR-027, C-3). The
// trust block on the Skills tab, driven through the imported page with the
// network edge doubled (CHECK round 1 finding 2: the earlier file held only
// a file-existence guard):
//   S-26: "Unverified skills" is the first block, Allow / Ask / Deny in that
//         order, with all three meaning lines visible;
//   S-32: choosing Allow shows the warning callout "Allow turns off the
//         check for unverified skills. Choose Ask to turn it back on.";
//   read-model control: canonical Ask from GET /security/permissions is
//         selected. C-3's legacy config migration is tested at config load,
//         never by making the screen consume the retired trust endpoint.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
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
import { request } from '@/lib/api/http'
import { PermissionSettings as SettingsSchema } from '@/lib/api/generated/schemas'
import { expectPermissionPatches, useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {}, skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}))

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
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

async function trustBlock(): Promise<HTMLElement> {
  // FR-027: the trust block is FIRST on the Skills tab.
  const block = await screen.findByTestId('trust-block')
  expect(block.parentElement?.firstElementChild).toBe(block)
  return block
}

describe('SkillsToolsPage — trust (order 25)', () => {
  beforeEach(async () => { SkillsToolsPage = await requireSkillsToolsPage() })

  it('S-26: the first block is "Unverified skills" with Allow/Ask/Deny in order and all three meanings', async () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = await trustBlock()

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
    const block = await trustBlock()

    await user.click(within(block).getByRole('button', { name: /^allow$/i }))

    expect(
      within(block).getByText(
        'Allow turns off the check for unverified skills. Choose Ask to turn it back on.',
      ),
    ).toBeInTheDocument()
    await waitFor(() => expectPermissionPatches(permissions(), { skill_trust: 'allow' }))
  })

  it('C-3 read-model control: the canonical stored Ask value renders Ask without writing', async () => {
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = await trustBlock()
    expect(within(block).getByRole('button', { name: /^ask$/i })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expectPermissionPatches(permissions())
  })
})

it('instrument: real HTTP helper reads the generated full model and per-key PATCH preserves unrelated keys', async () => {
  permissions().stored.policies = { bash: 'deny' }
  const given = { policies: { bash: 'deny' }, skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask' }
  expect(await request('/security/permissions', undefined, SettingsSchema)).toEqual(given)
  expect(await request('/security/permissions', {
    method: 'PATCH', body: JSON.stringify({ skill_trust: 'allow' }),
  }, SettingsSchema)).toEqual({ ...given, skill_trust: 'allow' })
  expectPermissionPatches(permissions(), { skill_trust: 'allow' })
  await expect(permissions().fetch('/api/v1/security/unplanned')).rejects.toThrow('Unexpected permissions fixture request: GET /api/v1/security/unplanned')
})
