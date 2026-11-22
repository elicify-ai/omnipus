// SkillsToolsPage.autosave.test.tsx — order 24 (S-01, S-07, S-08; FR-007,
// FR-008, FR-009). Per-value autosave on the rewritten Skills & Tools page,
// driven through the imported page with the network edge doubled (CHECK
// round 1 finding 2: the earlier file held only a file-existence guard):
//   S-01: clicking Deny on a row shows Deny and "Saved"; after a reload the
//         row still shows Deny;
//   S-07: a refused save shows "Not saved: <server reason>." with Retry and
//         the row keeps the value stored before the click;
//   S-08: a saved-but-not-applied 503 shows the plain sentence with Retry
//         and the row shows the new value.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import type { RegistryTool } from '@/lib/api'
// The pack's BLOCKED seam (support.tsx::requireModule convention): the
// missing module is a named, loud failure — the acceptance oracles are the
// interaction tests below.
import type { ComponentType } from 'react'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-007, FR-008, FR-009, S-01, S-07, S-08 and Dataset D12',
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

const SEND_EMAIL: RegistryTool = {
  name: 'send_email',
  description: 'Send an email.',
  scope: 'general',
  category: 'communication',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}
const BASH: RegistryTool = { ...SEND_EMAIL, name: 'bash', category: 'shell' }
const FETCH_URL: RegistryTool = { ...SEND_EMAIL, name: 'fetch_url', category: 'web' }

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
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([SEND_EMAIL, BASH, FETCH_URL])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({
    policies: { send_email: 'ask', bash: 'deny', fetch_url: 'ask' },
  })
  vi.mocked(api.updateGlobalToolPolicies).mockResolvedValue({ policies: {} })
  vi.mocked(api.fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' } as never)
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

function pressedPolicy(row: HTMLElement): string | null {
  for (const name of ['allow', 'ask', 'deny']) {
    const badge = within(row).getByRole('button', { name: new RegExp(`^${name}$`, 'i') })
    if (badge.getAttribute('aria-pressed') === 'true') return name
  }
  return null
}

async function openGroupAndClick(tool: string, group: RegExp, policy: string) {
  const user = userEvent.setup()
  const trigger = screen
    .getAllByRole('button', { name: group })
    .find((b) => b.getAttribute('aria-expanded') !== undefined)
  if (trigger && trigger.getAttribute('aria-expanded') === 'false') await user.click(trigger)
  const row = screen.getByTestId(`tool-row-${tool}`)
  await user.click(within(row).getByRole('button', { name: new RegExp(`^${policy}$`, 'i') }))
  return row
}

describe('SkillsToolsPage — autosave (order 24)', () => {
  it('S-01: Deny saves, shows "Saved", and survives a reload', async () => {
    const { unmount } = render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const row = await openGroupAndClick('send_email', /^communication/i, 'deny')

    await waitFor(() => {
      expect(screen.getByText('Saved')).toBeInTheDocument()
    })
    expect(pressedPolicy(row)).toBe('deny')

    // Reload: the stored value comes back Deny.
    unmount()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const trigger = screen
      .getAllByRole('button', { name: /^communication/i })
      .find((b) => b.getAttribute('aria-expanded') !== undefined)
    const user = userEvent.setup()
    if (trigger && trigger.getAttribute('aria-expanded') === 'false') await user.click(trigger)
    const reloaded = screen.getByTestId('tool-row-send_email')
    expect(pressedPolicy(reloaded)).toBe('deny')
  })

  it('S-07: a refused save shows the reason with Retry and keeps the stored value', async () => {
    vi.mocked(api.updateGlobalToolPolicies).mockRejectedValue(
      new ApiError(400, 'invalid policy value'),
    )
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const row = await openGroupAndClick('bash', /^shell/i, 'allow')

    await waitFor(() => {
      expect(screen.getByText('Not saved: invalid policy value.')).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(pressedPolicy(row)).toBe('deny') // the value stored before the click
  })

  it('S-08: a saved-but-not-applied 503 is said plainly, with Retry, and the row shows the new value', async () => {
    vi.mocked(api.updateGlobalToolPolicies).mockRejectedValue(
      new ApiError(
        503,
        'The settings were saved, but could not be applied to running agents. Retry applying the change, or restart Omnipus.',
      ),
    )
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const row = await openGroupAndClick('fetch_url', /^web/i, 'deny')

    await waitFor(() => {
      expect(
        screen.getByText(
          'Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus.',
        ),
      ).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(pressedPolicy(row)).toBe('deny')
  })
})
