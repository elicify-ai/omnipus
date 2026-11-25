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
// Fix round 4 (CHECK round 4, candidate 22) adds the invariants carried over
// from the retired SkillTrustSection.test: the warning only for Allow (S-32
// control), autosave with no Save or dialog (T-Q3 B), and a refused save with
// its reason and Retry (S-07).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ZodError } from 'zod'
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

  // CHECK round 4, candidate 22: invariants carried over from the retired
  // SkillTrustSection.test (C-22 row 6 deletes SkillTrustSection.tsx and its
  // endpoints). Each test names the legacy case it replaces and the spec line
  // that now owns it. Legacy cases with no equivalent are listed in the commit
  // message, each with its reason.

  // Legacy 'does not show warning panel when warn_unverified is selected' — and
  // the control that makes S-32 failable: a callout shown for every value would
  // pass S-32's positive test. User Story 4 AS5 ties the warning to Allow
  // ("Given trust = Allow ... a warning callout reads ... Choose Ask to turn it
  // back on."), so it cannot show for Ask or Deny and must leave when Ask is
  // chosen again. Each click waits for its own PATCH so none is coalesced.
  it('S-32 control: the warning callout is absent for Ask and Deny, present for Allow, and gone again after Ask', async () => {
    const warning = 'Allow turns off the check for unverified skills. Choose Ask to turn it back on.'
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = await trustBlock()
    expect(within(block).queryByText(warning), 'stored Ask').toBeNull()

    await user.click(within(block).getByRole('button', { name: /^deny$/i }))
    await waitFor(() => expectPermissionPatches(permissions(), { skill_trust: 'deny' }))
    expect(within(block).queryByText(warning), 'after Deny').toBeNull()

    await user.click(within(block).getByRole('button', { name: /^allow$/i }))
    await waitFor(() => expectPermissionPatches(permissions(), { skill_trust: 'deny' }, { skill_trust: 'allow' }))
    expect(within(block).getByText(warning), 'after Allow').toBeInTheDocument()

    await user.click(within(block).getByRole('button', { name: /^ask$/i }))
    await waitFor(() => expectPermissionPatches(
      permissions(), { skill_trust: 'deny' }, { skill_trust: 'allow' }, { skill_trust: 'ask' },
    ))
    expect(within(block).queryByText(warning), 'after choosing Ask again').toBeNull()
  })

  // Legacy 'clicking block_unverified radio fires updateSkillTrust immediately
  // (no Save button)' and 'no Save button is rendered': now one PATCH naming only
  // the trust key, "Saved", and no Save, Discard or dialog — T-Q3 B and the
  // spec's Qualitative Prohibitions ("must not show a Save bar, Discard, a leave
  // prompt or a Set all confirm"; the one exception is the execute_plan warning).
  it('T-Q3 B: choosing Deny saves by itself — one PATCH of the trust key, "Saved", no Save, Discard or dialog', async () => {
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = await trustBlock()

    await user.click(within(block).getByRole('button', { name: /^deny$/i }))

    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument())
    expectPermissionPatches(permissions(), { skill_trust: 'deny' })
    expect(permissions().stored.skill_trust).toBe('deny')
    expect(within(block).getByRole('button', { name: /^deny$/i })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByRole('button', { name: /^(save|save changes|discard)$/i }), 'no Save or Discard').toBeNull()
    expect(screen.queryByRole('dialog'), 'no dialog').toBeNull()
    expect(screen.queryByRole('alertdialog'), 'no confirm').toBeNull()
  })

  // Legacy 'shows error toast when mutation fails' — replaced by C-16's refused
  // row: "Not saved: <server reason>." + Retry, and the control keeps the stored
  // value (same oracle as S-07 for a tool row, here for the trust block).
  it('S-07: a refused trust save shows the reason with Retry and keeps the stored Ask', async () => {
    permissions().refuseNext(400, 'invalid trust value')
    const user = userEvent.setup()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = await trustBlock()

    await user.click(within(block).getByRole('button', { name: /^deny$/i }))

    await waitFor(() => expect(screen.getByText('Not saved: invalid trust value.')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(within(block).getByRole('button', { name: /^ask$/i })).toHaveAttribute('aria-pressed', 'true')
    expectPermissionPatches(permissions(), { skill_trust: 'deny' })
    expect(permissions().stored.skill_trust).toBe('ask')
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

it('instrument: invalid PATCH trust is rejected before a queued server response and changes no state', async () => {
  const network = permissions()
  const before = structuredClone(network.stored)
  network.refuseNext(503, 'fixture reload failed')
  const failure = structuredClone(network.rejection)
  // C-3 and the generated PATCH contract allow only allow/ask/deny. The queued
  // response deliberately bypasses final SettingsSchema parsing: rejection must
  // come from PATCH input validation, not an invalid full response by accident.
  const result = network.fetch('/api/v1/security/permissions', {
    method: 'PATCH', body: JSON.stringify({ skill_trust: 'allow_all' }),
  })
  await expect(result).rejects.toBeInstanceOf(ZodError)
  await expect(result).rejects.toMatchObject({
    issues: [{ code: 'invalid_enum_value', path: ['skill_trust'], received: 'allow_all' }],
  })
  expect(network.stored).toEqual(before)
  expect(network.rejection).toEqual(failure)
  expect(network.patches()).toEqual([{ skill_trust: 'allow_all' }])

  const response = await network.fetch('/api/v1/security/permissions', {
    method: 'PATCH', body: JSON.stringify({ skill_trust: 'allow' }),
  })
  expect(response.status).toBe(503)
  expect(await response.json()).toEqual({ error: 'fixture reload failed' })
  expect(network.stored).toEqual(before)
  expect(network.rejection).toBeUndefined()
})
