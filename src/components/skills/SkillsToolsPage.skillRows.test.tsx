// SkillsToolsPage.skillRows.test.tsx — the Skills tab's rows, their More actions
// menu and the Make a copy dialog, through the imported page, with the network
// edge doubled. Written in Skills & Tools fix round 4, Part C item 3: the
// invariants of the retired SkillsScreen test files SkillsScreen.riders.test.tsx
// (S-80, S-81), SkillsScreen.delete.test.tsx and SkillsScreen.copy-dialog.test.tsx,
// moved to the page that replaces the screen (spec line 72: SkillsScreen is
// rewritten; FR-065: /skills renders the new page).
//
//   S-80 / FR-063 (#1570): one unreadable skill does not fail the list. Ten
//        skills give ten rows, and only the broken one says "Cannot load" with
//        the server's reason.
//   S-81 / FR-064 (#1576): a copy shows its own name. After Make a copy named
//        `plan-mine` the new row is labelled `plan-mine`, not "Plan".
//   S-58 / FR-044 / User Story 7 AS8: More actions on built-in `plan` offers
//        Make a copy only; on installed `weather` it offers Make a copy and
//        Remove.
//   Remove sends the skill's id and the revision the list showed, never the
//        display name: openapi.yaml `deleteSkill` (path `name` = "Skill slug
//        (matches the `id` field on Skill)", query `revision` required).
//   Make a copy dialog (skills spec r15 section 8.6 and 13, kept by User Story 7
//        AS8): the default name `<slug>-copy`, the exact note, the exact copy
//        request, the exact success text, and the live name rule — letters,
//        digits and single hyphens up to 64 characters, any letter case, refusing
//        a taken or built-in name with the exact text and recovering once the name
//        is valid. Expectations are literal text, never imported constants.
//
// Not carried over, because only the old screen had it: the "Built-in · read-only"
// badge. The redesign spec names no badge on a skill row; S-58 states the
// read-only rule through the menu, which the S-58 test below pins.
//
// The network edge is the only double: the permissions endpoint at fetch, and
// the REST calls the rows make (list, copy, delete). The page, its rows, the
// menu, the dialogs and the toast store are real.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import type { Skill } from '@/lib/api'
import { useUiStore } from '@/store/ui'

// The pack's BLOCKED seam: a missing page is a named, loud failure, and the file
// still compiles on a tree that has no page yet.
const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec S-58, S-80, S-81, FR-063, FR-064, FR-065',
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
    copySkill: vi.fn(),
    deleteSkill: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: {},
  visibility: {},
  skill_visibility: {},
  skill_trust: 'ask',
}))

function skill(id: string, overrides: Partial<Skill> = {}): Skill {
  return {
    has_local_changes: false,
    id,
    name: id,
    description: `Skill ${id}.`,
    source: 'global',
    status: 'active',
    verified: true,
    version: '1.0.0',
    revision: 'a'.repeat(64),
    category: 'tools',
    trust_approved: false,
    ...overrides,
  }
}

// What the server currently lists, as GET /skills would answer.
const world: { skills: Skill[] } = { skills: [] }

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  world.skills = []
  vi.mocked(api.fetchSkills).mockImplementation(async () => structuredClone(world.skills))
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

afterEach(() => {
  cleanup()
})

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  return render(
    <QueryClientProvider client={client}>
      <SkillsToolsPage />
    </QueryClientProvider>,
  )
}

// The Skills tab is the first tab; its groups start collapsed. The skills here
// are in category "tools", whose group is labelled "Tools".
async function openToolsGroup() {
  const trigger = await waitFor(
    () => {
      const found = screen.getAllByRole('button', { name: /^Tools/ }).find((button) => button.hasAttribute('aria-expanded'))
      expect(found, 'the Tools group disclosure is present').toBeDefined()
      return found as HTMLElement
    },
    { timeout: 3000 },
  )
  if (trigger.getAttribute('aria-expanded') === 'false') await userEvent.setup().click(trigger)
}

// The names of the items More actions offers on one skill row, in order.
async function actionsOffered(skillName: string): Promise<string[]> {
  const user = userEvent.setup()
  const button = await screen.findByRole('button', { name: `More actions for ${skillName}` })
  await waitFor(() => expect(button).toBeEnabled(), { timeout: 3000 })
  await user.click(button)
  const menu = await screen.findByRole('menu')
  const names = within(menu).getAllByRole('menuitem').map((item) => item.textContent ?? '')
  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
  return names
}

describe('SkillsToolsPage — Skills tab rows', () => {
  it('S-80 / FR-063: ten skills give ten rows, and only the unreadable one says "Cannot load" with its reason', async () => {
    const reason = 'helper script not readable: permission denied'
    const readable = Array.from({ length: 10 }, (_, i) => skill(`skill-${i}`))
    world.skills = [...readable.slice(0, 3), skill('skill-3-broken', { load_error: reason }), ...readable.slice(4)]
    renderPage()
    await openToolsGroup()

    await waitFor(() => expect(screen.getAllByTestId(/^skill-row-/)).toHaveLength(10))
    for (const expected of world.skills) {
      const row = screen.getByTestId(`skill-row-${expected.id}`)
      expect(within(row).getByText(expected.name), `the row for ${expected.id} is labelled by its name`).toBeInTheDocument()
    }
    const broken = screen.getByTestId('skill-row-skill-3-broken')
    expect(within(broken).getByText('Cannot load')).toBeInTheDocument()
    expect(broken, 'the row carries the server\'s reason').toHaveTextContent(reason)
    // The other nine rows are readable: exactly one "Cannot load" on the page.
    expect(screen.getAllByText('Cannot load')).toHaveLength(1)
  })

  it('S-58 / FR-044: built-in plan offers Make a copy only; installed weather offers Make a copy and Remove', async () => {
    world.skills = [
      skill('plan', { name: 'Plan', source: 'builtin', version: '0.0.0' }),
      skill('weather', { name: 'Weather' }),
    ]
    renderPage()
    await openToolsGroup()

    expect(await actionsOffered('Plan')).toEqual(['Make a copy'])
    expect(await actionsOffered('Weather')).toEqual(['Make a copy', 'Remove'])
  })

  it('S-81 / FR-064: a copy named plan-mine is labelled plan-mine, and its own menu offers Remove', async () => {
    const user = userEvent.setup()
    world.skills = [skill('plan', { name: 'Plan', source: 'builtin', version: '0.0.0' })]
    vi.mocked(api.copySkill).mockImplementation(async (_from, to) => {
      const copy = skill(to, { description: 'own copy', has_local_changes: true })
      world.skills = [...world.skills, copy]
      return copy
    })
    renderPage()
    await openToolsGroup()

    const more = await screen.findByRole('button', { name: 'More actions for Plan' })
    await waitFor(() => expect(more).toBeEnabled(), { timeout: 3000 })
    await user.click(more)
    await user.click(await screen.findByRole('menuitem', { name: 'Make a copy' }))
    const input = await screen.findByLabelText('Name of your copy')
    await user.clear(input)
    await user.type(input, 'plan-mine')
    await user.click(screen.getByRole('button', { name: 'Create copy' }))

    const copyRow = await screen.findByTestId('skill-row-plan-mine', undefined, { timeout: 3000 })
    expect(within(copyRow).getByText('plan-mine'), 'the copy row is labelled by its own name').toBeInTheDocument()
    expect(within(copyRow).queryByText('Plan'), 'S-81: not by the source\'s display name').toBeNull()
    // The built-in keeps its own label beside it: one row each.
    expect(within(screen.getByTestId('skill-row-plan')).getByText('Plan')).toBeInTheDocument()
    expect(vi.mocked(api.copySkill)).toHaveBeenCalledExactlyOnceWith('plan', 'plan-mine')
    // The copy is user-owned: its menu offers Make a copy and Remove (S-58).
    expect(await actionsOffered('plan-mine')).toEqual(['Make a copy', 'Remove'])
  })

  it('Remove sends the skill id and its revision, never the display name', async () => {
    const user = userEvent.setup()
    const revision = '3'.repeat(64)
    world.skills = [skill('web-research', { name: 'Web Research', revision, verified: false, description: 'Search the web' })]
    vi.mocked(api.deleteSkill).mockResolvedValue({
      revision,
      persistence_status: 'complete',
      activation_status: 'active',
      changed_fields: ['skills'],
    })
    renderPage()
    await openToolsGroup()

    const more = await screen.findByRole('button', { name: 'More actions for Web Research' })
    await waitFor(() => expect(more).toBeEnabled(), { timeout: 3000 })
    await user.click(more)
    await user.click(await screen.findByRole('menuitem', { name: 'Remove' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(within(confirm).getByText('Remove Web Research?')).toBeInTheDocument()
    await user.click(within(confirm).getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(api.deleteSkill).toHaveBeenCalledTimes(1))
    expect(api.deleteSkill).toHaveBeenCalledWith('web-research', revision)
    expect(api.deleteSkill).not.toHaveBeenCalledWith('Web Research', expect.anything())
  })
})

// The toasts the real store holds now (the page's own addToast writes here).
function toastsNow() {
  return useUiStore.getState().toasts
}

// Opens More actions on the named skill and chooses Make a copy; returns the
// name field of the dialog it opens.
async function openCopyDialog(skillName: string) {
  const user = userEvent.setup()
  const more = await screen.findByRole('button', { name: `More actions for ${skillName}` })
  await waitFor(() => expect(more).toBeEnabled(), { timeout: 3000 })
  await user.click(more)
  await user.click(await screen.findByRole('menuitem', { name: 'Make a copy' }))
  return screen.findByLabelText('Name of your copy')
}

describe('SkillsToolsPage — Make a copy dialog', () => {
  const PLAN = skill('plan', { name: 'plan', description: 'Plan before acting.', source: 'builtin', version: '0.0.0' })

  it('the dialog starts at plan-copy with the exact note, and Create copy sends one exact request, lists the copy and says what the copy lacks', async () => {
    const user = userEvent.setup()
    world.skills = [PLAN]
    renderPage()
    await openToolsGroup()

    const input = await openCopyDialog('plan')
    expect(input).toHaveValue('plan-copy')
    expect(screen.getByText('Your copy will not update with Omnipus. Grant it to agents in their settings.')).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'my-plan')
    // r15 section 9.2: a copy is authored (kind=create), not an unchanged installation.
    const ownCopy = skill('my-plan', { description: 'own copy', has_local_changes: true })
    vi.mocked(api.copySkill).mockImplementation(async () => {
      world.skills = [PLAN, ownCopy]
      return ownCopy
    })
    await user.click(screen.getByRole('button', { name: 'Create copy' }))

    await waitFor(() => expect(vi.mocked(api.copySkill)).toHaveBeenCalledExactlyOnceWith('plan', 'my-plan'))
    const copyRow = await screen.findByTestId('skill-row-my-plan', undefined, { timeout: 3000 })
    expect(within(copyRow).getByText('my-plan')).toBeInTheDocument()
    expect(await actionsOffered('my-plan'), 'the copy is the person\'s own: it can be removed').toEqual(['Make a copy', 'Remove'])
    // r15 section 13 fixes the complete success text, including the no-grants reminder.
    await waitFor(() => expect(toastsNow()).toHaveLength(1))
    expect(toastsNow()[0]).toMatchObject({
      message: "Created 'my-plan'. No agent is granted it yet; grant it to others in their settings.",
      variant: 'success',
    })
  })

  // r15 section 5.13 and 13: validation is live, blocks submission and recovers
  // when the name becomes valid. A constant message or null cannot satisfy both
  // states.
  const INVALID = 'Use letters, digits and single hyphens, up to 64 characters'
  const TAKEN = 'This name is already used'
  const BUILTIN = "This is a built-in skill's name"
  const NAME_CASES = [
    ['bad_name', INVALID],
    ['BAD_NAME', INVALID],
    ['Bad_Name', INVALID],
    ['two--hyphens', INVALID],
    ['-leading', INVALID],
    ['trailing-', INVALID],
    ['a'.repeat(65), INVALID], // section 5.13: maximum + 1.
    ['my-plan', TAKEN],
    ['MY-PLAN', TAKEN],
    ['mY-pLaN', TAKEN],
    ['plan', BUILTIN],
    ['PLAN', BUILTIN],
    ['PlAn', BUILTIN],
  ] as const

  it.each(NAME_CASES)('copy name %s shows the exact refusal, blocks Create copy and recovers', async (name, message) => {
    const user = userEvent.setup()
    // An installed skill whose id differs from the typed name only by letter case.
    world.skills = [PLAN, skill('My-Plan', { name: 'My-Plan', verified: false })]
    renderPage()
    await openToolsGroup()

    const input = await openCopyDialog('plan')
    const submit = screen.getByRole('button', { name: 'Create copy' })
    expect(submit, 'the default name is valid').toBeEnabled()
    await user.clear(input)
    await user.type(input, name)
    expect(input).toHaveValue(name)
    expect(screen.getByText(message, { exact: true })).toBeInTheDocument()
    expect(submit).toBeDisabled()
    await user.click(submit)
    expect(vi.mocked(api.copySkill)).not.toHaveBeenCalled()

    await user.clear(input)
    await user.type(input, 'NEW-Copy9')
    expect(input).toHaveValue('NEW-Copy9')
    for (const refusal of [INVALID, TAKEN, BUILTIN]) {
      expect(screen.queryByText(refusal, { exact: true }), `recovered: "${refusal}" is gone`).not.toBeInTheDocument()
    }
    expect(submit).toBeEnabled()
    expect(vi.mocked(api.copySkill)).not.toHaveBeenCalled()
  })

  it.each([1, 2, 63, 64])('accepts a valid %i-character name with no refusal', async (length) => {
    const user = userEvent.setup()
    world.skills = [PLAN]
    renderPage()
    await openToolsGroup()

    const input = await openCopyDialog('plan')
    await user.clear(input)
    await user.type(input, 'A'.repeat(length)) // section 5.13: min, min+1, max-1, max.
    expect(input).toHaveValue('A'.repeat(length))
    expect(screen.getByRole('button', { name: 'Create copy' })).toBeEnabled()
    for (const refusal of [INVALID, TAKEN, BUILTIN]) {
      expect(screen.queryByText(refusal, { exact: true })).not.toBeInTheDocument()
    }
  })
})
