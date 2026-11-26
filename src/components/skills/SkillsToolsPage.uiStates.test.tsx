// SkillsToolsPage.uiStates.test.tsx — the loading, empty, error and partial
// rows of the spec's "UI Screens and States" table for the three tabs of the
// new page (FR-050: "Each tab MUST show the loading, empty, error and partial
// states in 'UI Screens and States'; no write MUST be possible while a tab is
// loading or failed"). Written in Skills & Tools fix round 4, Part C item 3, on
// team-lead's ruling of 2026-11-25: "add failing tests for every empty or error
// row ... for the new page ... The table is a requirement; untested rows are
// gaps." Before this file only "Loading skills…" (S-66) and a failing tools
// request (S-67) were pinned; the strings below appeared in no test.
//
// The table cells this file pins (the "Skills tab", "External tools tab" and
// "Omnipus tools tab" rows of "UI Screens and States" at spec commit b27c64109),
// by tab:
//   Skills tab
//     Loading  — no trust choice can be used while the skills request is open
//                (FR-050).   [control: passes on the dev head]
//     Empty    — the trust block stays; "No skills installed" and a
//                "Browse the catalog" button that opens the same catalog the
//                New skill menu opens.   [RED on the dev head, run fix4c7-01:
//                "Unable to find an element with the text: No skills
//                installed" and "Unable to find an accessible element with
//                the role "button" and name "Browse the catalog""]
//     Error    — a callout with the cause and Retry; no writes; Retry asks
//                again and the page recovers.   [control: passes on the dev
//                head, fix4c7-01]
//     Partial  — the list loads but the permissions do not: the rows are
//                read-only and the page says "Permissions could not be
//                loaded." with Retry, and Retry restores writing.
//                [RED on the dev head, run fix4c7-01: the sentence is not on
//                 the page; its callout reads "The server is unavailable.
//                 Please try again in a moment." (the two controls "rows
//                 still show, read-only" and "Retry restores" pass)]
//   External tools tab
//     Loading  — "Loading services…".   [RED on the dev head, run
//                fix4c7-01: "Unable to find an element with the text:
//                Loading services…"; the run does not show what the tab says
//                instead]
//     Error    — a callout with the cause and Retry; Retry asks again.
//                [control: passes on the dev head, fix4c7-01]
//     (Empty "No external services yet" and Partial "Not connected" are in
//      SkillsToolsPage.external.test.tsx.)
//   Omnipus tools tab
//     Loading  — "Loading tools…".   [control: passes on the dev head,
//                fix4c7-01]
//     Empty    — "No Omnipus tools reported" with Retry; Retry asks again.
//                [RED on the dev head, run fix4c7-01: "Unable to find an
//                element with the text: No Omnipus tools reported"]
//     (Error is S-67 in SkillsToolsPage.states.test.tsx. The Partial row,
//      "tools load, permissions fail", is S-132, pinned in
//      SkillsToolsPage.states.test.tsx (test 30) since spec amendment
//      b27c64109.)
//
// What this file does NOT assert, and why:
//   - "trust block skeleton" and the "(outline)" look of the empty-state button:
//     the spec names no element or style hook for either, and a test that
//     guessed a selector would pin a name the spec never gave.
//   - Skills-tab Partial "The upgrade has not finished for <n> agents", the
//     Omnipus-tools Partial and the Full-skill size and warning (FR-079): the
//     spec named no wire field for them until the amendment of 2026-11-25
//     (commit b27c64109, ruling spec-gaps-1). They are pinned where the
//     amendment puts them: S-131 and S-132 in SkillsToolsPage.states.test.tsx
//     (test 30) and S-130 in SkillsToolsPage.fullSize.test.tsx (test 66).
//
// Only the network edge is doubled: the permissions endpoint at fetch and the
// list requests the page makes. The page, its rows, tabs, callouts and the
// catalog dialog are real.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import type { RegistryTool, Skill } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'

// The pack's BLOCKED seam: a missing page is a named, loud failure, and the file
// still compiles on a tree that has no page yet.
const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-050 and the UI Screens and States table',
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

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

// The stored permissions as the server holds them: the weather skill is Allow,
// so a row that is read-only (or restored) shows it.
const permissions = useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: { weather: 'allow' },
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

function tool(name: string): RegistryTool {
  return {
    name,
    description: `${name} description`,
    scope: 'general',
    category: 'communication',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  }
}

// What the server currently lists, as the three list requests would answer.
const world: { skills: Skill[]; tools: RegistryTool[] } = { skills: [], tools: [] }

const NEVER = () => new Promise<never>(() => {})

beforeEach(() => {
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  world.skills = []
  world.tools = []
  vi.mocked(api.fetchSkills).mockImplementation(async () => structuredClone(world.skills))
  vi.mocked(api.fetchRegistryTools).mockImplementation(async () => structuredClone(world.tools))
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

async function openTab(name: RegExp) {
  await userEvent.setup().click(await screen.findByRole('tab', { name }))
}

// The Skills tab's groups start collapsed; the fixtures are in category
// "tools", whose group is labelled "Tools".
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

const PERMISSION_WORDS = /^(allow|ask|deny)$/i

// Every Allow / Ask / Deny control on the page: the badges of the rows are toggle buttons and the three choices of
// the trust block are radios (spec "Accessibility and Keyboard": a radio group of three badges). A check over the
// whole page has to look at both roles; a button-only query returns nothing for the trust block and passes without it.
const everyChoice = () => [
  ...screen.queryAllByRole('button', { name: PERMISSION_WORDS }),
  ...screen.queryAllByRole('radio', { name: PERMISSION_WORDS }),
]

describe('SkillsToolsPage — Skills tab states (UI Screens and States, row "Skills tab")', () => {
  it('Loading / FR-050: while the skills request is unanswered, no trust choice can be used', async () => {
    vi.mocked(api.fetchSkills).mockImplementation(NEVER)
    renderPage()

    expect(await screen.findByText('Loading skills…')).toBeInTheDocument()
    // The table draws a trust block skeleton, so there may be no choices at
    // all; any that are drawn must be disabled (FR-050: no write while loading).
    for (const choice of everyChoice()) {
      expect(choice, 'a trust choice is disabled while loading').toBeDisabled()
    }
  })

  it('Empty: the trust block stays and the page says "No skills installed" with a "Browse the catalog" button', async () => {
    world.skills = []
    renderPage()

    expect(await screen.findByText('No skills installed')).toBeInTheDocument()
    const block = await screen.findByTestId('trust-block')
    expect(within(block).getByText('Unverified skills'), 'the trust block stays').toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Browse the catalog' })).toBeEnabled()
    // Not an error: no Retry, no loading text.
    expect(screen.queryByRole('button', { name: /^retry$/i })).toBeNull()
    expect(screen.queryByText('Loading skills…')).toBeNull()
  })

  it('Empty control: with a skill installed, "No skills installed" does not show', async () => {
    world.skills = [skill('weather')]
    renderPage()
    await openToolsGroup()

    expect(await screen.findByTestId('skill-row-weather')).toBeInTheDocument()
    expect(screen.queryByText('No skills installed')).toBeNull()
  })

  it('Empty: "Browse the catalog" opens the same catalog the New skill menu opens', async () => {
    world.skills = []
    const user = userEvent.setup()
    const titleOf = (dialog: HTMLElement) => {
      const labelledBy = dialog.getAttribute('aria-labelledby')
      return (labelledBy ? document.getElementById(labelledBy)?.textContent : dialog.getAttribute('aria-label')) ?? ''
    }
    renderPage()
    await waitFor(() => expect(screen.queryByText('Loading skills…')).toBeNull())

    // The reference: the menu's own item (User Story 7: New skill → Browse).
    await user.click(screen.getByRole('button', { name: /new skill/i }))
    await user.click(await screen.findByRole('menuitem', { name: 'Browse the catalog' }))
    const fromMenu = titleOf(await screen.findByRole('dialog'))
    expect(fromMenu, 'the catalog dialog has a title').not.toBe('')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await user.click(screen.getByRole('button', { name: 'Browse the catalog' }))
    expect(titleOf(await screen.findByRole('dialog'))).toBe(fromMenu)
  })

  it('Error: a failing skills request shows its cause with Retry, nothing can be written, and Retry recovers', async () => {
    const cause = 'The skills folder could not be read'
    world.skills = [skill('weather')]
    vi.mocked(api.fetchSkills).mockRejectedValueOnce(new ApiError(500, cause))
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByText(cause)).toBeInTheDocument()
    for (const choice of everyChoice()) {
      expect(choice, 'no choice is enabled while the list failed').toBeDisabled()
    }
    expect(permissions().patches(), 'nothing was written').toEqual([])

    const before = vi.mocked(api.fetchSkills).mock.calls.length
    await user.click(screen.getByRole('button', { name: /^retry$/i }))
    await waitFor(() => expect(vi.mocked(api.fetchSkills).mock.calls.length).toBeGreaterThan(before))
    await waitFor(() => expect(screen.queryByText(cause)).toBeNull())
    await openToolsGroup()
    expect(await screen.findByTestId('skill-row-weather'), 'the list the retry returned is shown').toBeInTheDocument()
  })

  it('Partial: when the list loads but the permissions do not, the page says "Permissions could not be loaded." with Retry', async () => {
    world.skills = [skill('weather')]
    permissions().failNextRead(500, 'The settings file could not be read')
    renderPage()

    expect(await screen.findByText('Permissions could not be loaded.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
  })

  it('Partial: when the permissions fail the skill rows still show, read-only', async () => {
    world.skills = [skill('weather')]
    permissions().failNextRead(500, 'The settings file could not be read')
    renderPage()
    await openToolsGroup()

    const row = await screen.findByTestId('skill-row-weather')
    expect(within(row).getByText('weather'), 'the list loaded').toBeInTheDocument()
    for (const choice of within(row).queryAllByRole('button', { name: PERMISSION_WORDS })) {
      expect(choice, 'a permission choice is read-only without permissions').toBeDisabled()
    }
    expect(permissions().patches(), 'nothing was written').toEqual([])
  })

  it('Partial: Retry reads the permissions again and the row shows its stored Allow and can be changed', async () => {
    world.skills = [skill('weather')]
    permissions().failNextRead(500, 'The settings file could not be read')
    const user = userEvent.setup()
    renderPage()
    await openToolsGroup()
    await screen.findByTestId('skill-row-weather')

    const readsBefore = permissions().seen.filter(({ method }) => method === 'GET').length
    await user.click(await screen.findByRole('button', { name: /^retry$/i }))

    await waitFor(() => expect(permissions().seen.filter(({ method }) => method === 'GET').length).toBeGreaterThan(readsBefore))
    const row = screen.getByTestId('skill-row-weather')
    await waitFor(() => {
      expect(within(row).getByRole('button', { name: /^allow$/i })).toBeEnabled()
    })
    expect(within(row).getByRole('button', { name: /^allow$/i }), 'the stored value is shown').toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByText('Permissions could not be loaded.')).toBeNull()
  })
})

describe('SkillsToolsPage — External tools tab states (UI Screens and States, row "External tools tab")', () => {
  it('Loading: while the services are unanswered the tab says "Loading services…"', async () => {
    vi.mocked(api.fetchRegistryTools).mockImplementation(NEVER)
    vi.mocked(api.fetchMcpServers).mockImplementation(NEVER)
    renderPage()
    await openTab(/external tools/i)

    expect(await screen.findByText('Loading services…')).toBeInTheDocument()
  })

  it('Error: a failing services request shows its cause with Retry, and Retry asks again', async () => {
    const cause = 'The services list could not be read'
    vi.mocked(api.fetchMcpServers).mockRejectedValueOnce(new ApiError(500, cause))
    const user = userEvent.setup()
    renderPage()
    await openTab(/external tools/i)

    const panel = await screen.findByRole('tabpanel')
    expect(await within(panel).findByText(new RegExp(cause))).toBeInTheDocument()
    const before = vi.mocked(api.fetchMcpServers).mock.calls.length
    await user.click(within(panel).getByRole('button', { name: /^retry$/i }))
    await waitFor(() => expect(vi.mocked(api.fetchMcpServers).mock.calls.length).toBeGreaterThan(before))
    await waitFor(() => expect(within(screen.getByRole('tabpanel')).queryByText(new RegExp(cause))).toBeNull())
  })
})

describe('SkillsToolsPage — Omnipus tools tab states (UI Screens and States, row "Omnipus tools tab")', () => {
  it('Loading: while the tools are unanswered the tab says "Loading tools…"', async () => {
    vi.mocked(api.fetchRegistryTools).mockImplementation(NEVER)
    renderPage()
    await openTab(/^omnipus tools/i)

    expect(await screen.findByText('Loading tools…')).toBeInTheDocument()
  })

  it('Empty: a registry that reports no tools shows "No Omnipus tools reported" with Retry, and Retry asks again', async () => {
    world.tools = []
    const user = userEvent.setup()
    renderPage()
    await openTab(/^omnipus tools/i)

    const panel = await screen.findByRole('tabpanel')
    expect(await within(panel).findByText('No Omnipus tools reported')).toBeInTheDocument()
    const before = vi.mocked(api.fetchRegistryTools).mock.calls.length
    await user.click(within(panel).getByRole('button', { name: /^retry$/i }))
    await waitFor(() => expect(vi.mocked(api.fetchRegistryTools).mock.calls.length).toBeGreaterThan(before))
  })

  it('Empty control: with tools reported, "No Omnipus tools reported" does not show', async () => {
    world.tools = [tool('send_email')]
    renderPage()
    await openTab(/^omnipus tools/i)

    const panel = await screen.findByRole('tabpanel')
    await waitFor(() => expect(within(panel).queryByText('Loading tools…')).toBeNull())
    expect(within(panel).queryByText('No Omnipus tools reported')).toBeNull()
  })
})
