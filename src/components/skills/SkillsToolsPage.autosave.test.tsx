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
  policies: { send_email: 'ask', bash: 'deny', fetch_url: 'ask' },
  skill_policies: {}, visibility: { send_email: 'full', bash: 'full', fetch_url: 'full' },
  skill_visibility: {}, skill_trust: 'ask',
}))

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
  await user.click(await screen.findByRole('tab', { name: /omnipus tools/i }))
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
    expectPermissionPatches(permissions(), { policies: { send_email: 'deny' } })
    expect(permissions().stored.policies).toEqual({ send_email: 'deny', bash: 'deny', fetch_url: 'ask' })

    // Reload: the stored value comes back Deny from a fresh GET, not props.
    unmount()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await userEvent.setup().click(await screen.findByRole('tab', { name: /omnipus tools/i }))
    const trigger = screen
      .getAllByRole('button', { name: /^communication/i })
      .find((b) => b.getAttribute('aria-expanded') !== undefined)
    const user = userEvent.setup()
    if (trigger && trigger.getAttribute('aria-expanded') === 'false') await user.click(trigger)
    const reloaded = screen.getByTestId('tool-row-send_email')
    expect(pressedPolicy(reloaded)).toBe('deny')
    expect(permissions().seen.filter(({ method }) => method === 'GET').length).toBeGreaterThanOrEqual(2)
    expectPermissionPatches(permissions(), { policies: { send_email: 'deny' } })
  })

  it('S-07: a refused save shows the reason with Retry and keeps the stored value', async () => {
    permissions().refuseNext(400, 'invalid policy value')
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const row = await openGroupAndClick('bash', /^shell/i, 'allow')

    await waitFor(() => {
      expect(screen.getByText('Not saved: invalid policy value.')).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(pressedPolicy(row)).toBe('deny') // the value stored before the click
    expectPermissionPatches(permissions(), { policies: { bash: 'allow' } })
    expect(permissions().stored.policies).toEqual({ send_email: 'ask', bash: 'deny', fetch_url: 'ask' })
  })

  it('S-08: a saved-but-not-applied 503 is said plainly, with Retry, and the row shows the new value', async () => {
    permissions().refuseNext(503,
      'The settings were saved, but could not be applied to running agents. Retry applying the change, or restart Omnipus.',
      { ...permissions().stored, policies: { send_email: 'ask', bash: 'deny', fetch_url: 'deny' } },
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
    expectPermissionPatches(permissions(), { policies: { fetch_url: 'deny' } })
    expect(permissions().stored.policies).toEqual({ send_email: 'ask', bash: 'deny', fetch_url: 'deny' })
  })

  // CHECK round 4, candidate 11: the "applies directly, no confirmation" half of
  // the retired SecuritySection case 'global tool-policy save' (FR-OB-046),
  // moved to the replacement. Oracle: the spec's Qualitative Prohibitions ("must
  // not show a Save bar, Discard, a leave prompt or a Set all confirm ... because
  // T-Q3 B chose autosave with no confirm") and C-16 ("Requests use fetch with
  // keepalive ... No leave prompt"). The legacy case's second half, "no consent
  // token", has no line in the redesign spec: the PATCH contract declares no
  // re-authentication header, and a step-up would open a dialog, which the
  // absence checks below catch. It is listed in the report as a spec gap.
  it('T-Q3 B: one change saves by itself — no dialog, Save, Discard or leave prompt, before or after the save', async () => {
    const leavePromptShown = (): boolean => {
      const leaving = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(leaving)
      return leaving.defaultPrevented
    }
    // Instrument control: this probe sees a leave prompt when one is planted.
    const planted = (event: Event) => event.preventDefault()
    window.addEventListener('beforeunload', planted)
    expect(leavePromptShown(), 'instrument: a planted prompt is seen').toBe(true)
    window.removeEventListener('beforeunload', planted)
    expect(leavePromptShown(), 'instrument: and gone once removed').toBe(false)

    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const row = await openGroupAndClick('send_email', /^communication/i, 'deny')
    expect(leavePromptShown(), 'no leave prompt while the change is still pending').toBe(false)

    await waitFor(() => {
      expect(screen.getByText('Saved')).toBeInTheDocument()
    })
    expect(leavePromptShown(), 'no leave prompt after the save').toBe(false)
    for (const role of ['alertdialog', 'dialog'] as const) {
      expect(screen.queryByRole(role), `no ${role} for a single change`).toBeNull()
    }
    expect(screen.queryByRole('button', { name: /^(save|save changes|discard)$/i })).toBeNull()
    expect(screen.queryByText(/leave without saving/i)).toBeNull()
    expect(pressedPolicy(row)).toBe('deny')
    expectPermissionPatches(permissions(), { policies: { send_email: 'deny' } })
  })
})
