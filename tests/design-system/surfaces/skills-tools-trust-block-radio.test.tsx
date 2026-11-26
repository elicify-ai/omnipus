// Probe of the Trust block's accessibility requirement. Fix round 4, Part H, finding F2
// (team-lead go). This file is NOT a registered check of the design-system catalog (it
// names no surface); it exists so that the requirement is executed and not only reported.
//
// Oracle: docs/internal/specs/skills-tools-redesign-spec.md at b27c64109, section
// "Accessibility and Keyboard", the bullet
//   "Trust block: a radio group of three badges, one tab stop, arrow keys; all three
//    meanings always visible, each linked by `aria-describedby`."
// and the three meanings S-26 / User Story 4 AS1 print:
//   Allow  "Unverified skills install and load without a check."
//   Ask    "Installing or loading an unverified skill asks you first."
//   Deny   "Unverified skills cannot be installed or loaded."
// Nothing was read from the page. The page's own Trust block is drawn today as three
// aria-pressed toggle buttons in a plain container (TrustBlock.tsx), which is the gap.
//
// Readings added to the spec's words, each labelled where it is used:
//   (1) a radio group has exactly one checked radio, and it is the stored value;
//   (2) "arrow keys" means: the right arrow moves focus to the next choice in the order
//       Allow, Ask, Deny and the left arrow to the previous one. Whether the arrow also
//       CHOOSES the value it lands on (the WAI-ARIA radio-group habit) is not in the
//       spec's words and is not asserted; it is a hands-on row in the pack.
//
// How the clauses are kept apart: the first test asks for the role (a radio group of three
// radios). The other clauses (one tab stop, arrow keys, aria-describedby) find each choice
// by its word, as a radio or, until the page draws radios, as the button of the same name,
// so that each reports its own failure and not the missing role again.
//
// RED proof: run against the frontend head e0f987fa0 (receipt in the Part H pack). GREEN,
// mutation and proof of failability: deferred to CHECK.
//
// Only the network edge is doubled: the list requests and the permissions endpoint.
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
  PERMISSION_WORDS, installOverlayPolyfills, renderPage, requireSkillsToolsPage, resetWorld, serveWorld, skill, world,
} from './skills-tools-harness'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: { 'trip-notes': 'ask' },
  visibility: {},
  skill_visibility: { 'trip-notes': 'name_and_description' },
  skill_trust: 'ask',
}))

// S-26 / US-4 AS1: the meaning of each choice, word for word.
const MEANING = {
  Allow: 'Unverified skills install and load without a check.',
  Ask: 'Installing or loading an unverified skill asks you first.',
  Deny: 'Unverified skills cannot be installed or loaded.',
} as const

let Page: ComponentType
beforeEach(async () => {
  installOverlayPolyfills()
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec "Accessibility and Keyboard" (Trust block) and S-26')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  world.skills = [skill('trip-notes', { category: 'tools' })]
})

async function trustBlock(): Promise<HTMLElement> {
  return screen.findByTestId('trust-block')
}

// The control of one choice: a radio (what the spec requires) or, until the page draws radios,
// the button of the same name. Exactly one must exist.
function choice(block: HTMLElement, word: string): HTMLElement {
  const found = [...within(block).queryAllByRole('radio', { name: word }), ...within(block).queryAllByRole('button', { name: word })]
  expect(found, `the Trust block has exactly one control named ${word}`).toHaveLength(1)
  return found[0]
}

function words(element: HTMLElement | null): string {
  return (element?.textContent ?? '').replace(/\s+/g, ' ').trim()
}

describe('Trust block accessibility: a radio group of three badges, one tab stop, arrow keys, meanings linked by aria-describedby', () => {
  it('Accessibility "Trust block: a radio group of three badges": one radio group holds three radios, Allow, Ask, Deny in that order, and the stored value is the checked one', async () => {
    renderPage(Page)
    const block = await trustBlock()

    const group = await within(block).findByRole('radiogroup')
    const radios = within(group).getAllByRole('radio')
    expect(radios, 'the radio group holds exactly three radios').toHaveLength(3)
    PERMISSION_WORDS.forEach((word, index) => {
      expect(radios[index], `radio ${index + 1} of the group is ${word}`).toBe(within(group).getByRole('radio', { name: word }))
    })
    // Reading (1): the stored value is Ask (permissions() above), so Ask is the one checked radio.
    expect(within(group).getByRole('radio', { name: 'Ask' }), 'Ask is stored, so Ask is checked').toBeChecked()
    expect(within(group).getByRole('radio', { name: 'Allow' }), 'Allow is not stored').not.toBeChecked()
    expect(within(group).getByRole('radio', { name: 'Deny' }), 'Deny is not stored').not.toBeChecked()
  })

  it('Accessibility "Trust block: ... one tab stop": pressing Tab through the page stops on exactly one of the three choices', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    const block = await trustBlock()
    const controls = PERMISSION_WORDS.map((word) => choice(block, word))

    // Start on the selected tab, which sits before the block, and press Tab well past the block.
    // A set, because a walk that wraps round the document may meet the same stop twice.
    ;(await screen.findByRole('tab', { selected: true })).focus()
    const stops = new Set<string>()
    for (let step = 0; step < 30; step += 1) {
      await user.tab()
      const index = controls.indexOf(document.activeElement as HTMLElement)
      if (index >= 0) stops.add(PERMISSION_WORDS[index])
    }
    expect(
      [...stops],
      `Tab reached the choices ${[...stops].join(', ') || '(none)'}; the group has one tab stop, so exactly one`,
    ).toHaveLength(1)
  })

  it('Accessibility "Trust block: ... arrow keys": from Ask the right arrow moves focus to Deny, the left arrow back to Ask and on to Allow', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    const block = await trustBlock()

    choice(block, 'Ask').focus()
    expect(choice(block, 'Ask'), 'instrument: the test put focus on Ask').toHaveFocus()

    // Reading (2): arrows move focus along the order Allow, Ask, Deny. Waits, because a roving
    // focus moves focus after the key event, not inside it.
    await user.keyboard('{ArrowRight}')
    await waitFor(() => expect(choice(block, 'Deny'), 'the right arrow on Ask moves focus to Deny').toHaveFocus())
    await user.keyboard('{ArrowLeft}')
    await waitFor(() => expect(choice(block, 'Ask'), 'the left arrow on Deny moves focus back to Ask').toHaveFocus())
    await user.keyboard('{ArrowLeft}')
    await waitFor(() => expect(choice(block, 'Allow'), 'the left arrow on Ask moves focus to Allow').toHaveFocus())
  })

  it.each(PERMISSION_WORDS.map((word) => [word, MEANING[word]] as const))(
    'Accessibility "Trust block: ... each linked by aria-describedby": the %s choice is described by its own meaning, which is on screen',
    async (word, sentence) => {
      renderPage(Page)
      const block = await trustBlock()
      const control = choice(block, word)

      const ids = (control.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
      expect(ids.length, `the ${word} choice carries aria-describedby`).toBeGreaterThan(0)
      const described = ids.map((id) => document.getElementById(id))
      described.forEach((element, index) => expect(element, `aria-describedby "${ids[index]}" names an element of the page`).not.toBeNull())
      const text = described.map(words).join(' ')
      expect(text, `${word} is described by its own meaning`).toContain(sentence)
      for (const other of PERMISSION_WORDS.filter((candidate) => candidate !== word)) {
        expect(text, `${word} is not described by the meaning of ${other}`).not.toContain(MEANING[other])
      }
      described.forEach((element) => expect(element as HTMLElement, 'the described meaning is visible').toBeVisible())
    },
  )

  it.each(['allow', 'ask', 'deny'] as const)(
    'Accessibility "Trust block: ... all three meanings always visible": with %s stored all three meanings are on screen',
    async (stored) => {
      permissions().stored.skill_trust = stored
      renderPage(Page)
      const block = await trustBlock()

      for (const word of PERMISSION_WORDS) {
        await waitFor(() => expect(within(block).getByText(MEANING[word]), `the meaning of ${word} with ${stored} stored`).toBeVisible())
      }
    },
  )
})
