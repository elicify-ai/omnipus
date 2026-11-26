// Surface src/components/skills/SkillRowActions.tsx#overlay:1 — UNIT check (the "Choose a
// category" dialog that More actions opens on a skill row). The interaction check is
// ...-overlay-1.interaction.test.tsx; the browser check is
// tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109 and the
// SkillCategory contract (contracts/components/schemas/SkillCategory.yaml, 13 values):
//   US-7 AS8, FR-044, S-104   More actions offers "Choose category" while the skill is
//                             Uncategorised, and does not offer it for a skill that has a
//                             category; the choice opens a dialog; nothing is saved by opening it
//   S-104, C-21 "skill category" row
//                             the person chooses one category for an Uncategorised skill: the
//                             twelve categories of the contract, not Uncategorised itself
//                             (Uncategorised is where the skill is now)
//
// The spec names no title, no description and no button label for this dialog. Tests marked
// "characterization test" pin the words of the head; they do not verify correctness.
//
// Only the network edge is doubled: the list requests and the permissions endpoint. The
// category write itself is doubled in the interaction file.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentType } from 'react'
import { useUiStore } from '@/store/ui'

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
    fetchAgents: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../helpers/skill-permission-network'
import {
  installOverlayPolyfills, openGroup, renderPage, requireSkillsToolsPage, resetWorld, serveWorld, skill, world,
} from './skills-tools-harness'
import { categoryEdge } from './skills-tools-category-edge'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: { 'trip-notes': 'ask', weather: 'allow' },
  visibility: {},
  skill_visibility: { 'trip-notes': 'name_and_description', weather: 'name_and_description' },
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  installOverlayPolyfills()
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec US-7 AS8, FR-044 and S-104')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  // trip-notes is stored as Uncategorised (S-104's Given); weather has a category.
  world.skills = [
    skill('trip-notes', { category: 'uncategorised' }),
    skill('weather', { category: 'tools' }),
  ]
})

const person = () => userEvent.setup({ pointerEventsCheck: 0 })
async function showRows() {
  renderPage(Page)
  await openGroup(/^uncategorised/i)
  await openGroup(/^tools/i)
}
async function openMoreActions(user: ReturnType<typeof person>, id: string) {
  const row = await screen.findByTestId(`skill-row-${id}`)
  const trigger = within(row).getByRole('button', { name: `More actions for ${id}` })
  await waitFor(() => expect(trigger).toBeEnabled())
  await user.click(trigger)
  return screen.findByRole('menu')
}
const itemNames = (menu: HTMLElement) => within(menu).getAllByRole('menuitem').map((item) => (item.textContent ?? '').trim()).sort()
async function openCategoryDialog(user: ReturnType<typeof person>) {
  const menu = await openMoreActions(user, 'trip-notes')
  await user.click(within(menu).getByRole('menuitem', { name: 'Choose category' }))
  return screen.findByRole('dialog')
}

describe('Choose a category dialog surface unit contract', () => {
  it('US-7 AS8 FR-044: More actions of an Uncategorised skill offers Make a copy, Choose category and Remove', async () => {
    const user = person()
    await showRows()

    const menu = await openMoreActions(user, 'trip-notes')

    expect(itemNames(menu)).toEqual(['Choose category', 'Make a copy', 'Remove'])
  })

  it('US-7 AS8 FR-044: More actions of a skill that has a category does not offer Choose category', async () => {
    const user = person()
    await showRows()

    const menu = await openMoreActions(user, 'weather')

    expect(itemNames(menu)).toEqual(['Make a copy', 'Remove'])
  })

  it('S-104: until Choose category is chosen no dialog is open, and choosing it opens one without writing anything', async () => {
    const user = person()
    const edge = categoryEdge()
    await showRows()
    expect(screen.queryByRole('dialog')).toBeNull()

    const dialog = await openCategoryDialog(user)

    expect(dialog).toBeInTheDocument()
    expect(within(dialog).getByRole('combobox', { name: 'Category' }), 'the dialog holds the Category field').toBeInTheDocument()
    expect(edge.writes, 'opening the dialog writes no category').toEqual([])
    expect(permissions().patches(), 'opening the dialog writes no permission').toEqual([])
  })

  it('S-104 C-21: the Category field offers the twelve categories of the contract and not Uncategorised', async () => {
    const user = person()
    await showRows()
    const dialog = await openCategoryDialog(user)

    await user.click(within(dialog).getByRole('combobox', { name: 'Category' }))

    const options = (await screen.findAllByRole('option')).map((option) => (option.textContent ?? '').trim())
    // SkillCategory has 13 values; Uncategorised is where the skill is now, so 12 can be chosen.
    expect(options).toHaveLength(12)
    for (const named of ['Lifestyle', 'Research', 'Tools', 'Business', 'Blockchain']) {
      expect(options, `${named} is offered (the spec names it)`).toContain(named)
    }
    expect(options.map((option) => option.toLowerCase())).not.toContain('uncategorised')
  })

  it('characterization test: the dialog is titled for the skill and says the category can be chosen once', async () => {
    const user = person()
    await showRows()

    const dialog = await openCategoryDialog(user)

    expect(within(dialog).getByText('Choose a category for trip-notes')).toBeInTheDocument()
    expect(within(dialog).getByText('You can choose it once. After that the category cannot be changed.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /^cancel$/i })).toBeEnabled()
    expect(within(dialog).getByRole('button', { name: /^save/i })).toBeEnabled()
  })
})
