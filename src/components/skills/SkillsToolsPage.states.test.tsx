// SkillsToolsPage.states.test.tsx — order 30 (S-66, S-67; C-16). The page's
// loading, failure and empty states, driven through the imported page with
// the network edge doubled (CHECK round 1 finding 2: the earlier file held
// only a file-existence guard):
//   S-66: while the skills request is unanswered, "Loading skills…" and
//         skeleton rows show, the count reads "…", and search, filters and
//         badges are disabled;
//   S-67: a failing tools request shows the cause with Retry and no badge
//         is enabled;
//   empty catalogue control: no rows and no error — the page renders its
//         empty state rather than a failure.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
// The pack's BLOCKED seam (support.tsx::requireModule convention): the
// missing module is a named, loud failure — the acceptance oracles are the
// interaction tests below.
import type { ComponentType } from 'react'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec S-66, S-67',
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
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

useSkillPermissionNetwork(() => ({
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
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

describe('SkillsToolsPage — states (order 30)', () => {
  it('S-66: while the skills request is pending, writing is disabled and the count reads "…"', () => {
    vi.mocked(api.fetchSkills).mockReturnValue(new Promise(() => {})) // never answers
    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    expect(screen.getByText('Loading skills…')).toBeInTheDocument()
    expect(screen.getByText('…')).toBeInTheDocument() // the count placeholder
    expect(screen.getByRole('textbox', { name: /search/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /permission filter/i })).toBeDisabled()
    // Skeleton rows: at least one loading placeholder is rendered.
    expect(screen.getAllByTestId(/skeleton/).length).toBeGreaterThan(0)
  })

  it('S-67: a failing tools request shows the cause with Retry and no badge is enabled', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([])
    vi.mocked(api.fetchRegistryTools).mockRejectedValue(new ApiError(0, 'Omnipus did not respond'))
    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    expect(await screen.findByText('Omnipus did not respond')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    // No badge is enabled anywhere (no writable rows without data).
    for (const badge of screen.queryAllByRole('button', { name: /^(allow|ask|deny)$/i })) {
      expect(badge).toBeDisabled()
    }
  })

  it('empty control: an empty catalogue renders no rows and no error', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([])
    render(<SkillsToolsPage />, { wrapper: pageWrapper })

    await screen.findByRole('tab', { name: /^skills/i })
    expect(screen.queryByTestId(/tool-row-/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
  })
})
