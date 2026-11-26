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
//
// Part I (team-lead Q3; architect ruling 2026-11-26, arch/ruling-trust-arrows.md, option A with the
// no-wrap safeguard): the three choices are a radio group (spec "Accessibility and Keyboard", "Trust
// block: a radio group of three badges"), so they are found by role radio and read through aria-checked,
// not as buttons with aria-pressed. The keyboard describes below carry that ruling's 14-row oracle table,
// each test titled with its row number. The oracle is the ruling and the spec's C-16 / C-2 / FR-028 /
// FR-050 text; no expected value was read from the component.
//   Not observable in jsdom, so not claimed here: the audit-log entries of rows 10 and 11 (nearest
//   coverage: the Go case "same-value retry saves nothing and writes no audit entry", D6#15, in
//   pkg/gateway/security_permissions_red_test.go) and the callout being fully visible at 390 px in row 4
//   (nearest coverage: the S-68 row of the e2e suite at 390x844).
import { afterEach, describe, it, expect, vi, beforeEach } from 'vitest'
import { ZodError } from 'zod'
import { act, fireEvent, render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import type { PermissionSettingsPatch, Skill, ToolPolicy } from '@/lib/api/generated/openapi-types'
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
    const choices = within(block).getAllByRole('radio', { name: /^(allow|ask|deny)$/i })
    expect(choices.map((choice) => choice.textContent?.toLowerCase())).toEqual(['allow', 'ask', 'deny'])
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

    await user.click(within(block).getByRole('radio', { name: /^allow$/i }))

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
    expect(within(block).getByRole('radio', { name: /^ask$/i })).toHaveAttribute(
      'aria-checked',
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

    await user.click(within(block).getByRole('radio', { name: /^deny$/i }))
    await waitFor(() => expectPermissionPatches(permissions(), { skill_trust: 'deny' }))
    expect(within(block).queryByText(warning), 'after Deny').toBeNull()

    await user.click(within(block).getByRole('radio', { name: /^allow$/i }))
    await waitFor(() => expectPermissionPatches(permissions(), { skill_trust: 'deny' }, { skill_trust: 'allow' }))
    expect(within(block).getByText(warning), 'after Allow').toBeInTheDocument()

    await user.click(within(block).getByRole('radio', { name: /^ask$/i }))
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

    await user.click(within(block).getByRole('radio', { name: /^deny$/i }))

    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument())
    expectPermissionPatches(permissions(), { skill_trust: 'deny' })
    expect(permissions().stored.skill_trust).toBe('deny')
    expect(within(block).getByRole('radio', { name: /^deny$/i })).toHaveAttribute('aria-checked', 'true')
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

    await user.click(within(block).getByRole('radio', { name: /^deny$/i }))

    await waitFor(() => expect(screen.getByText('Not saved: invalid trust value.')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(within(block).getByRole('radio', { name: /^ask$/i })).toHaveAttribute('aria-checked', 'true')
    expectPermissionPatches(permissions(), { skill_trust: 'deny' })
    expect(permissions().stored.skill_trust).toBe('ask')
  })
})

// ---------------------------------------------------------------------------------------------------------------
// Part I: the trust block as a radio group — keyboard and mouse (architect ruling 2026-11-26, rows 1 to 14).
// ---------------------------------------------------------------------------------------------------------------

const WORD: Record<ToolPolicy, string> = { allow: 'Allow', ask: 'Ask', deny: 'Deny' }
const CHOICES: readonly ToolPolicy[] = ['allow', 'ask', 'deny']
// S-26's three meaning lines and S-32's warning, word for word.
const MEANING: Record<ToolPolicy, string> = {
  allow: 'Unverified skills install and load without a check.',
  ask: 'Installing or loading an unverified skill asks you first.',
  deny: 'Unverified skills cannot be installed or loaded.',
}
const WARNING = 'Allow turns off the check for unverified skills. Choose Ask to turn it back on.'
type Key = 'ArrowRight' | 'ArrowDown' | 'ArrowLeft' | 'ArrowUp' | 'Home' | 'End'
const EVERY_KEY: readonly Key[] = ['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp', 'Home', 'End']

// C-16: "Saving…" by 100 ms and the request within 1,000 ms of the key when nothing earlier is pending. A wait for
// "nothing was sent" lasts the whole 1,000 ms and 100 ms more.
const SAVING_BY_MS = 100
const SENT_BY_MS = 1000
const QUIET_MS = SENT_BY_MS + 100

// The choice of the block that carries the word.
function radio(block: HTMLElement, choice: ToolPolicy): HTMLElement {
  return within(block).getByRole('radio', { name: new RegExp(`^${WORD[choice]}$`, 'i') })
}

// What the block shows a person: which choices are checked and which one holds the keyboard focus.
function shown(block: HTMLElement): { checked: ToolPolicy[]; focus: ToolPolicy | 'elsewhere' } {
  return {
    checked: CHOICES.filter((choice) => radio(block, choice).getAttribute('aria-checked') === 'true'),
    focus: CHOICES.find((choice) => radio(block, choice) === document.activeElement) ?? 'elsewhere',
  }
}

// The block's own polite status region (one SaveStatusLine per block or row): '', 'Saving…' or 'Saved'.
const statusOf = (block: HTMLElement) => within(block).getByRole('status').textContent

// A key goes where the keyboard focus is, as it does for a person.
function press(key: Key) {
  fireEvent.keyDown(document.activeElement ?? document.body, { key })
}

// The fake-clock describes move the clock themselves (RTL's waitFor and userEvent wait on real timers); the
// precedent is SaveQueue.states.test.ts. `until` is waitFor for that clock.
const tick = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
async function until(check: () => void) {
  for (let step = 0; ; step++) {
    try { return check() } catch (error) {
      if (step >= 50) throw error
      await tick(10)
    }
  }
}

function useFakeClock() {
  beforeEach(async () => {
    SkillsToolsPage = await requireSkillsToolsPage()
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.clearAllTimers()
    vi.useRealTimers()
  })
}

// Loads the page with `stored` as the stored trust, waits until the choices are enabled and puts the keyboard
// focus on the checked one (row 1 pins that Tab lands there).
async function openTrust(stored: ToolPolicy, skills: Skill[] = []): Promise<HTMLElement> {
  permissions().stored.skill_trust = stored
  vi.mocked(api.fetchSkills).mockResolvedValue(skills)
  render(<SkillsToolsPage />, { wrapper: pageWrapper })
  await until(() => {
    const checked = radio(screen.getByTestId('trust-block'), stored)
    expect(checked).toHaveAttribute('aria-checked', 'true')
    expect(checked).toBeEnabled()
  })
  const block = screen.getByTestId('trust-block')
  act(() => { radio(block, stored).focus() })
  expect(shown(block), 'the focus starts on the checked choice').toEqual({ checked: [stored], focus: stored })
  return block
}

describe('SkillsToolsPage — trust block: keys that select and save (rows 2, 4, 6, 7)', () => {
  useFakeClock()

  it.each(['ArrowRight', 'ArrowDown'] as const)(
    'row 2: %s from Ask selects and focuses Deny, shows Saving… by 100 ms, sends one request deny within 1,000 ms, then Saved',
    async (key) => {
      const block = await openTrust('ask')
      permissions().holdPatches()

      press(key)

      expect(shown(block), 'the key moves the choice and the focus at once').toEqual({ checked: ['deny'], focus: 'deny' })
      await tick(SAVING_BY_MS)
      expect(statusOf(block), 'C-16: Saving… by 100 ms').toBe('Saving…')
      await tick(SENT_BY_MS - SAVING_BY_MS)
      expectPermissionPatches(permissions(), { skill_trust: 'deny' })
      expect(statusOf(block), 'not Saved before the answer').toBe('Saving…')

      permissions().releasePatch()

      await until(() => expect(statusOf(block), 'Saved, in the polite status region').toBe('Saved'))
      expectPermissionPatches(permissions(), { skill_trust: 'deny' })
      expect(permissions().stored.skill_trust).toBe('deny')
      expect(shown(block), 'the answer moves neither the choice nor the focus').toEqual({ checked: ['deny'], focus: 'deny' })
    },
  )

  it.each(['ArrowLeft', 'ArrowUp'] as const)(
    'row 4: %s from Ask selects and focuses Allow, sends one request allow and shows the Allow warning',
    async (key) => {
      const block = await openTrust('ask')

      press(key)

      expect(shown(block)).toEqual({ checked: ['allow'], focus: 'allow' })
      expect(within(block).getByText(WARNING), 'FR-028: the warning is shown once Allow is chosen').toBeVisible()
      await tick(SENT_BY_MS)
      await until(() => expect(statusOf(block)).toBe('Saved'))
      expectPermissionPatches(permissions(), { skill_trust: 'allow' })
      expect(within(block).getByText(WARNING), 'and stays while Allow is stored').toBeVisible()
    },
  )

  it('row 6: Home from Deny selects Allow and sends one request allow; Ask is skipped and never sent', async () => {
    const block = await openTrust('deny')

    press('Home')

    expect(shown(block)).toEqual({ checked: ['allow'], focus: 'allow' })
    await tick(SENT_BY_MS)
    await until(() => expect(statusOf(block)).toBe('Saved'))
    expectPermissionPatches(permissions(), { skill_trust: 'allow' })
  })

  it('row 7: End from Allow selects Deny and sends one request deny', async () => {
    const block = await openTrust('allow')

    press('End')

    expect(shown(block)).toEqual({ checked: ['deny'], focus: 'deny' })
    await tick(SENT_BY_MS)
    await until(() => expect(statusOf(block)).toBe('Saved'))
    expectPermissionPatches(permissions(), { skill_trust: 'deny' })
  })
})

describe('SkillsToolsPage — trust block: the arrows stop at Allow and Deny (rows 3, 5, 8)', () => {
  useFakeClock()

  // The safeguard of the ruling: with no wrap, the keyboard never passes through Allow on its way to another value.
  it.each(['ArrowRight', 'ArrowDown'] as const)(
    'row 3: %s on Deny does not wrap round: the focus stays on Deny, nothing is selected and nothing is sent',
    async (key) => {
      const block = await openTrust('deny')

      press(key)
      await tick(QUIET_MS)

      expect(shown(block)).toEqual({ checked: ['deny'], focus: 'deny' })
      expect(statusOf(block), 'nothing was queued').toBe('')
      expect(permissions().patches(), 'no request, and in particular no allow').toEqual([])
    },
  )

  it.each(['ArrowLeft', 'ArrowUp'] as const)(
    'row 5: %s on Allow does not wrap round: the focus stays on Allow, nothing is selected and nothing is sent',
    async (key) => {
      const block = await openTrust('allow')

      press(key)
      await tick(QUIET_MS)

      expect(shown(block)).toEqual({ checked: ['allow'], focus: 'allow' })
      expect(statusOf(block), 'nothing was queued').toBe('')
      expect(permissions().patches(), 'no request, and in particular no deny').toEqual([])
    },
  )

  it.each([['allow', 'Home'], ['deny', 'End']] as const)(
    'row 8: with %s already checked, %s sends nothing',
    async (stored, key) => {
      const block = await openTrust(stored)

      press(key)
      await tick(QUIET_MS)

      expect(shown(block)).toEqual({ checked: [stored], focus: stored })
      expect(statusOf(block), 'nothing was queued').toBe('')
      expect(permissions().patches()).toEqual([])
    },
  )

  // Rows 2 to 8 in one run from Ask: each key, the choice it leaves checked and the request it sends. The arrow at an
  // end is the only step that sends nothing, and the keyboard reaches Allow only by moving onto it.
  it('walk: Right, Right, Left, Left, Left, End, Home from Ask save deny, ask, allow, deny, allow and nothing else', async () => {
    const block = await openTrust('ask')
    const walk: ReadonlyArray<readonly [Key, ToolPolicy, ToolPolicy | null]> = [
      ['ArrowRight', 'deny', 'deny'], ['ArrowRight', 'deny', null], ['ArrowLeft', 'ask', 'ask'], ['ArrowLeft', 'allow', 'allow'],
      ['ArrowLeft', 'allow', null], ['End', 'deny', 'deny'], ['Home', 'allow', 'allow'],
    ]
    const sent: PermissionSettingsPatch[] = []

    for (const [key, after, request] of walk) {
      press(key)
      await tick(QUIET_MS)
      if (request) sent.push({ skill_trust: request })
      expect(shown(block), `after ${key}`).toEqual({ checked: [after], focus: after })
      expectPermissionPatches(permissions(), ...sent)
    }
    expect(permissions().stored.skill_trust).toBe('allow')
  })
})

const PLAN: Skill = {
  id: 'plan', name: 'plan', description: 'Plan work.', version: '1.0.0', source: 'global', status: 'active',
  verified: true, trust_approved: false, has_local_changes: false, revision: 'e'.repeat(64), category: 'business',
}

// An earlier change of another key: the skill row `plan` with its group opened and one of its badges clicked.
async function changeSkillRow(policy: ToolPolicy) {
  const group = screen.getAllByRole('button', { name: /^business/i }).find((button) => button.hasAttribute('aria-expanded'))
  expect(group, 'the Business group of the skill plan is listed').toBeDefined()
  if (group?.getAttribute('aria-expanded') === 'false') { fireEvent.click(group); await tick() }
  fireEvent.click(within(screen.getByTestId('skill-row-plan')).getByRole('button', { name: new RegExp(`^${WORD[policy]}$`, 'i') }))
  await tick()
}

describe('SkillsToolsPage — trust block: keys and the save queue (rows 10, 11)', () => {
  useFakeClock()

  // "Quickly" in the ruling's row 10 means: after the first request has gone out. C-16 lets a later change of the same key
  // replace a queued change that has not been sent yet, and allows up to 1,000 ms before a request goes out, so two keys
  // pressed inside one instant may legitimately collapse into one request (ask). The test therefore lets the spec's bound
  // pass between the two keys: allow is then certainly in flight, and ask has to wait for its answer.
  it('row 10: ArrowLeft then ArrowRight quickly from Ask sends allow, then ask only after allow has its outcome', async () => {
    const block = await openTrust('ask')
    permissions().holdPatches()

    press('ArrowLeft')
    await tick(SENT_BY_MS)
    expectPermissionPatches(permissions(), { skill_trust: 'allow' })
    expect(permissions().patchesInFlight, 'allow is in flight and unanswered').toBe(1)
    press('ArrowRight')

    expect(shown(block), 'the block shows the last key').toEqual({ checked: ['ask'], focus: 'ask' })
    await tick(QUIET_MS)
    expectPermissionPatches(permissions(), { skill_trust: 'allow' })
    expect(permissions().patchesInFlight, 'C-16: ask waits while allow has no answer').toBe(1)

    permissions().releasePatch()

    await until(() => expectPermissionPatches(permissions(), { skill_trust: 'allow' }, { skill_trust: 'ask' }))
    expect(statusOf(block), 'C-16: Saved belongs to the change whose answer arrived, and ask is still open').toBe('Saving…')
    permissions().releasePatch()
    await until(() => expect(statusOf(block)).toBe('Saved'))
    expectPermissionPatches(permissions(), { skill_trust: 'allow' }, { skill_trust: 'ask' })
    expect(permissions().maxPatchesInFlight, 'one request at a time').toBe(1)
    expect(permissions().stored.skill_trust).toBe('ask')
    expect(shown(block)).toEqual({ checked: ['ask'], focus: 'ask' })
  })

  it('row 11: Right then Left while an earlier change of another key is in flight replaces the queued deny by ask; deny is never sent', async () => {
    permissions().stored.skill_policies = { plan: 'ask' }
    const block = await openTrust('ask', [PLAN])
    permissions().holdPatches()
    await changeSkillRow('deny')
    await tick(SENT_BY_MS)
    expectPermissionPatches(permissions(), { skill_policies: { plan: 'deny' } })
    act(() => { radio(block, 'ask').focus() })

    press('ArrowRight')
    expect(shown(block), 'deny is queued behind the earlier change').toEqual({ checked: ['deny'], focus: 'deny' })
    press('ArrowLeft')
    expect(shown(block), 'C-16: a later change of the same key replaces the queued one').toEqual({ checked: ['ask'], focus: 'ask' })
    await tick(QUIET_MS)
    expectPermissionPatches(permissions(), { skill_policies: { plan: 'deny' } })

    permissions().releasePatch()

    await until(() => expectPermissionPatches(permissions(), { skill_policies: { plan: 'deny' } }, { skill_trust: 'ask' }))
    permissions().releasePatch()
    await until(() => expect(statusOf(block)).toBe('Saved'))
    expectPermissionPatches(permissions(), { skill_policies: { plan: 'deny' } }, { skill_trust: 'ask' })
    expect(permissions().maxPatchesInFlight, 'one request at a time').toBe(1)
    expect(permissions().stored.skill_trust).toBe('ask')
  })
})

describe('SkillsToolsPage — trust block while the tab is loading or has failed (row 12, FR-050)', () => {
  useFakeClock()

  // FR-050: no write is possible while a tab is loading or failed. Whether the block is then drawn as a skeleton or as
  // disabled choices is the UI-states tests' business; what counts here is that no key writes anything and no key
  // changes the checked choice. Focus is not asserted: a browser gives no focus to a disabled button, but jsdom treats any
  // element with a tabindex as focusable, so a focus assertion would test the test environment, not the page. The keys
  // are sent straight to each choice and to the active element, which is stronger than a browser would deliver.
  const checkedChoices = (choices: HTMLElement[]) => choices.filter((choice) => choice.getAttribute('aria-checked') === 'true').map((choice) => choice.textContent)

  async function expectNoTrustWrite() {
    const block = screen.queryByTestId('trust-block')
    const choices = block ? within(block).queryAllByRole('radio') : []
    const checkedBefore = checkedChoices(choices)
    for (const choice of choices) {
      expect(choice, 'a trust choice is disabled').toBeDisabled()
      for (const key of EVERY_KEY) fireEvent.keyDown(choice, { key })
    }
    for (const key of EVERY_KEY) press(key)
    await tick(QUIET_MS)
    expect(checkedChoices(choices), 'no key changed the checked choice').toEqual(checkedBefore)
    expect(permissions().patches(), 'no request').toEqual([])
  }

  it.each([
    ['the skills list is still loading', () => { vi.mocked(api.fetchSkills).mockImplementation(() => new Promise<Skill[]>(() => {})) }],
    ['the permissions request has not answered', () => { permissions().holdReads() }],
    ['the permissions request failed', () => { permissions().failNextRead(500, 'The settings file could not be read') }],
    ['the skills list failed', () => { vi.mocked(api.fetchSkills).mockRejectedValue(new ApiError(500, 'The skills folder could not be read')) }],
  ] as const)('row 12: while %s, no key changes the trust and nothing is sent', async (_state, arrange) => {
    arrange()
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await tick(QUIET_MS)

    await expectNoTrustWrite()
  })

  // The control that keeps the test above from passing on a page that never drew a choice: after a successful
  // Retry (US-10 AS5, "after a successful Retry the badges work") the same keys do write.
  it('row 12 control: after a successful Retry the same keys work', async () => {
    permissions().failNextRead(500, 'The settings file could not be read')
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    await until(() => expect(screen.getByText('Permissions could not be loaded.')).toBeInTheDocument())
    await expectNoTrustWrite()

    fireEvent.click(screen.getByRole('button', { name: /^retry$/i }))
    await until(() => expect(radio(screen.getByTestId('trust-block'), 'ask')).toBeEnabled())
    const block = screen.getByTestId('trust-block')
    act(() => { radio(block, 'ask').focus() })
    press('ArrowRight')
    await tick(SENT_BY_MS)

    expectPermissionPatches(permissions(), { skill_trust: 'deny' })
  })
})

describe('SkillsToolsPage — trust block: Tab, Space and Enter, screen reader and mouse (rows 1, 9, 13, 14)', () => {
  beforeEach(async () => { SkillsToolsPage = await requireSkillsToolsPage() })

  async function readyBlock(stored: ToolPolicy): Promise<HTMLElement> {
    permissions().stored.skill_trust = stored
    render(<SkillsToolsPage />, { wrapper: pageWrapper })
    const block = await trustBlock()
    await waitFor(() => {
      expect(radio(block, stored)).toHaveAttribute('aria-checked', 'true')
      expect(radio(block, stored)).toBeEnabled()
    })
    return block
  }

  // Tab from the top of the page until the focus is inside the block (at most 40 stops).
  async function tabInto(user: ReturnType<typeof userEvent.setup>, block: HTMLElement): Promise<Element | null> {
    for (let stop = 0; stop < 40 && !block.contains(document.activeElement); stop++) await user.tab()
    return document.activeElement
  }

  it.each(CHOICES)('row 1: with %s stored, Tab lands on it, the other two are out of the tab order and nothing is sent', async (stored) => {
    const user = userEvent.setup()
    const block = await readyBlock(stored)

    expect(await tabInto(user, block), 'Tab lands on the checked choice').toBe(radio(block, stored))

    for (const other of CHOICES.filter((choice) => choice !== stored)) {
      expect(radio(block, other), `${WORD[other]} is not a tab stop`).toHaveAttribute('tabindex', '-1')
    }
    await user.tab()
    expect(block.contains(document.activeElement), 'one tab stop: the next Tab leaves the block').toBe(false)
    expect(permissions().patches(), 'moving the focus sends nothing').toEqual([])
  })

  it('row 9: Space and Enter on the focused choice send nothing, because it is already selected', async () => {
    const user = userEvent.setup()
    const block = await readyBlock('ask')
    await tabInto(user, block)

    await user.keyboard(' ')
    await user.keyboard('{Enter}')
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, QUIET_MS)) })

    expect(shown(block)).toEqual({ checked: ['ask'], focus: 'ask' })
    expect(statusOf(block), 'nothing was queued').toBe('')
    expect(permissions().patches()).toEqual([])
  }, 15_000)

  // The control for row 9: Space does select a choice that is not selected yet (the WAI-ARIA radio group pattern,
  // which is the ruling's basis), so the test above passes on the already-selected rule and not on keys that reach nothing.
  it('row 9 control: Space on a focused choice that is not selected selects it and sends it', async () => {
    const user = userEvent.setup()
    const block = await readyBlock('ask')
    act(() => { radio(block, 'allow').focus() })

    await user.keyboard(' ')

    await waitFor(() => expectPermissionPatches(permissions(), { skill_trust: 'allow' }))
    expect(shown(block).checked).toEqual(['allow'])
  })

  it('row 13: the group is named Unverified skills; each choice is a radio with aria-checked and reads its meaning line', async () => {
    const block = await readyBlock('ask')

    const group = within(block).getByRole('radiogroup', { name: 'Unverified skills' })
    const radios = within(group).getAllByRole('radio')
    expect(radios.map((choice) => choice.textContent)).toEqual(['Allow', 'Ask', 'Deny'])
    expect(radios.map((choice) => choice.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false'])
    radios.forEach((choice, at) => {
      expect(choice, `${WORD[CHOICES[at]]} reads its meaning line`).toHaveAccessibleDescription(MEANING[CHOICES[at]])
    })
  })

  it('row 14: a mouse click on Allow from Ask gives the same result as the arrow key: Allow checked, one request, the warning', async () => {
    const user = userEvent.setup()
    const block = await readyBlock('ask')

    await user.click(radio(block, 'allow'))

    expect(shown(block)).toEqual({ checked: ['allow'], focus: 'allow' })
    expect(within(block).getByText(WARNING)).toBeVisible()
    await waitFor(() => expect(statusOf(block)).toBe('Saved'))
    expectPermissionPatches(permissions(), { skill_trust: 'allow' })
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
