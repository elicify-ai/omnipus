// Surface src/components/skills/SkillRowActions.tsx#overlay:1 — INTERACTION check (the
// "Choose a category" dialog that More actions opens on a skill row). The unit check is
// ...-overlay-1.test.tsx; the browser check is tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109:
//   S-104, US-7 AS8, C-21    choosing Lifestyle for the Uncategorised skill trip-notes saves
//                            Lifestyle: one PUT /skills/trip-notes/category with the category
//                            "lifestyle" (SkillCategory contract); the skill is then listed
//                            under Lifestyle, "Choose category" is no longer offered for it
//                            (FR-044: only while Uncategorised) and no Uncategorised group is
//                            drawn when it is empty (US-7 AS7)
//   S-104, C-21, error table a category already set is refused with 409 "The category is set at
//                            install and cannot be changed."; the person sees that sentence and
//                            the list is read again, so the skill shows under the category the
//                            server holds
//   a dialog that is cancelled or dismissed changes nothing (what a Cancel and an Escape are for)
//   US-7 AS5 / S-55 (said of the install review: "Choose a category.") — the same words are
//                            used here when nothing is chosen: characterization test, the
//                            spec does not say it of this dialog
//
// Only the network edge is doubled: the list requests, the permissions endpoint and the
// category write (skills-tools-category-edge.ts).
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
  headerReadings, installOverlayPolyfills, openGroup, renderPage, requireSkillsToolsPage, resetWorld, serveWorld, skill, world,
} from './skills-tools-harness'
import { categoryEdge } from './skills-tools-category-edge'

useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: { 'trip-notes': 'ask' },
  visibility: {},
  skill_visibility: { 'trip-notes': 'name_and_description' },
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  installOverlayPolyfills()
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec S-104, US-7 AS8 and C-21')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  // S-104's Given: the skill trip-notes is stored as Uncategorised.
  world.skills = [skill('trip-notes', { category: 'uncategorised' })]
})

const person = () => userEvent.setup({ pointerEventsCheck: 0 })
const REFUSAL = 'The category is set at install and cannot be changed.'

async function openMoreActions(user: ReturnType<typeof person>) {
  const row = await screen.findByTestId('skill-row-trip-notes')
  const trigger = within(row).getByRole('button', { name: 'More actions for trip-notes' })
  await waitFor(() => expect(trigger).toBeEnabled())
  await user.click(trigger)
  return screen.findByRole('menu')
}
async function openDialog(user: ReturnType<typeof person>) {
  renderPage(Page)
  await openGroup(/^uncategorised/i)
  await user.click(within(await openMoreActions(user)).getByRole('menuitem', { name: 'Choose category' }))
  return screen.findByRole('dialog')
}
async function pick(user: ReturnType<typeof person>, dialog: HTMLElement, name: string) {
  await user.click(within(dialog).getByRole('combobox', { name: 'Category' }))
  await user.click(await screen.findByRole('option', { name }))
}
const saveButton = (dialog: HTMLElement) => within(dialog).getByRole('button', { name: /^save/i })

describe('Choose a category dialog surface interaction contract', () => {
  it('S-104 US-7 AS8: choosing Lifestyle and saving makes one write naming trip-notes with lifestyle, and the skill is then listed under Lifestyle', async () => {
    const user = person()
    const edge = categoryEdge()
    const dialog = await openDialog(user)

    await pick(user, dialog, 'Lifestyle')
    await user.click(saveButton(dialog))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(edge.writes).toEqual([{ path: '/api/v1/skills/trip-notes/category', body: { category: 'lifestyle' } }])
    // The list was read again: the skill sits under Lifestyle and no empty Uncategorised group is drawn (US-7 AS7).
    await openGroup(/^lifestyle/i)
    expect(await screen.findByTestId('skill-row-trip-notes')).toBeInTheDocument()
    expect(headerReadings().filter((text) => /^uncategorised/.test(text)), 'no empty Uncategorised group').toEqual([])
  })

  it('S-104 FR-044: after the category is saved More actions of that skill no longer offers Choose category', async () => {
    const user = person()
    categoryEdge()
    const dialog = await openDialog(user)
    await pick(user, dialog, 'Lifestyle')
    await user.click(saveButton(dialog))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await openGroup(/^lifestyle/i)

    const menu = await openMoreActions(user)

    expect(within(menu).queryByRole('menuitem', { name: 'Choose category' })).toBeNull()
    expect(within(menu).getByRole('menuitem', { name: 'Make a copy' }), 'control: the menu itself opened').toBeInTheDocument()
  })

  it('S-104 C-21: a refused write shows the server sentence The category is set at install and cannot be changed., and the list is read again', async () => {
    const user = person()
    const edge = categoryEdge()
    // The list is stale: the server already holds Research for this skill (another tab chose it).
    edge.refuseNext(409, REFUSAL, { category: 'research' })
    let holding = false
    let releaseList = () => {}
    const listAnswer = new Promise<void>((resolve) => { releaseList = resolve })
    vi.mocked(api.fetchSkills).mockImplementation(async () => {
      if (holding) await listAnswer
      return structuredClone(world.skills)
    })
    const dialog = await openDialog(user)
    await pick(user, dialog, 'Lifestyle')
    holding = true

    await user.click(saveButton(dialog))

    // While the new list is on its way the dialog is still there and carries the sentence.
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(REFUSAL)
    expect(screen.getByRole('dialog'), 'the dialog stays open on a refusal').toBe(dialog)
    expect(edge.writes).toEqual([{ path: '/api/v1/skills/trip-notes/category', body: { category: 'lifestyle' } }])
    releaseList()
    // The list read again shows the category the server holds.
    await openGroup(/^research/i)
    expect(await screen.findByTestId('skill-row-trip-notes')).toBeInTheDocument()
    expect(headerReadings().filter((text) => /^uncategorised/.test(text)), 'the skill left Uncategorised').toEqual([])
    expect(edge.writes, 'no second write was made').toHaveLength(1)
  })

  it('characterization test: Save with no category chosen shows Choose a category. and makes no write', async () => {
    const user = person()
    const edge = categoryEdge()
    const dialog = await openDialog(user)

    await user.click(saveButton(dialog))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Choose a category.')
    expect(edge.writes).toEqual([])
    expect(screen.getByRole('dialog'), 'the dialog stays open').toBe(dialog)
  })

  it('S-104: Cancel closes the dialog without a write, and the skill is still Uncategorised with Choose category on offer', async () => {
    const user = person()
    const edge = categoryEdge()
    const dialog = await openDialog(user)
    await pick(user, dialog, 'Lifestyle')

    await user.click(within(dialog).getByRole('button', { name: /^cancel$/i }))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(edge.writes).toEqual([])
    const menu = await openMoreActions(user)
    expect(within(menu).getByRole('menuitem', { name: 'Choose category' })).toBeInTheDocument()
  })

  it('S-104: Escape closes the dialog without a write', async () => {
    const user = person()
    const edge = categoryEdge()
    const dialog = await openDialog(user)
    await pick(user, dialog, 'Lifestyle')

    await user.keyboard('{Escape}')
    // The first Escape may close the open list of categories; a second closes the dialog.
    if (screen.queryByRole('dialog')) await user.keyboard('{Escape}')

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(edge.writes).toEqual([])
  })
})
