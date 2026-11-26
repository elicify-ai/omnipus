// Probe of three accessibility requirements of the Skills & Tools spec. Fix round 4,
// Part G. This file is NOT a registered check of the design-system catalog (it names no
// surface); it exists so that the three requirements are executed and not only reported.
//
// RED proof: on the frontend head 9b21f6940 every test below FAILS for the named reason
// (the head drew none of the three). They are expected to turn GREEN on a frontend
// head that carries them (1a140710f, "phone cards, save status region, permission
// groups, aria-controls"). GREEN on that head, mutation and proof of failability are
// deferred to CHECK.
//
// Oracles are the spec's own words (docs/internal/specs/skills-tools-redesign-spec.md at
// b27c64109, section "Accessibility and Keyboard" and S-69); nothing was read from the page:
//   spec line 961  "Allow / Ask / Deny: toggle buttons with `aria-pressed` in a group
//                   named "Permission for <name>""
//   spec line 965  "Group headers: `aria-expanded`, `aria-controls`."
//   spec line 966  "Save signal: polite status region; errors use `role="alert"`."
//   S-69 (1848)    "... Enter selects Ask, and the status region announces "Saved""
//
// One reading is added to the spec's words and is labelled where it is used: an
// aria-controls value names an element that exists in the page (WAI-ARIA idref).
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
  PERMISSION_WORDS, groupDisclosures, installOverlayPolyfills, openGroup, openTab, reading, renderPage,
  requireSkillsToolsPage, resetWorld, serveWorld, service, serviceTools, skill, tool, world,
} from './skills-tools-harness'

const permissions = useSkillPermissionNetwork(() => ({
  policies: { bash: 'allow', send_email: 'ask' },
  skill_policies: { 'trip-notes': 'ask' },
  visibility: { bash: 'full', send_email: 'full' },
  skill_visibility: { 'trip-notes': 'name_and_description' },
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  installOverlayPolyfills()
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec "Accessibility and Keyboard" and S-69')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  world.skills = [skill('trip-notes', { category: 'tools' })]
  world.tools = [tool('bash', { category: 'shell' }), tool('send_email')]
  const notion = serviceTools('notion', ['search', 'fetch'])
  world.tools = [...world.tools, ...notion]
  world.servers = [service('notion', notion)]
})

// Every header of the visible tab has aria-controls, and (reading added to the spec) the value names an element of the page.
function expectEveryHeaderControlsABody(where: string) {
  const headers = groupDisclosures()
  expect(headers.length, `${where}: the tab draws group headers`).toBeGreaterThan(0)
  for (const header of headers) {
    const controlled = header.getAttribute('aria-controls')
    expect(controlled, `${where}: the header "${reading(header)}" has aria-controls`).toBeTruthy()
    expect(document.getElementById(controlled as string), `${where}: aria-controls="${controlled}" of "${reading(header)}" names an element of the page`).not.toBeNull()
    expect(header, `${where}: the header "${reading(header)}" has aria-expanded`).toHaveAttribute('aria-expanded')
  }
}

async function expectChoicesInPermissionGroup(row: HTMLElement, name: string) {
  const group = await within(row).findByRole('group', { name: `Permission for ${name}` })
  for (const word of PERMISSION_WORDS) {
    expect(within(group).getByRole('button', { name: word }), `${word} is in the group "Permission for ${name}" and is a toggle button`).toHaveAttribute('aria-pressed')
  }
  expect(within(group).getAllByRole('button'), `the group "Permission for ${name}" holds only the three choices`).toHaveLength(PERMISSION_WORDS.length)
}

describe('Skills & Tools accessibility requirements the spec states and the page must draw', () => {
  it('Accessibility "Save signal: polite status region" and S-69 Then: after a choice is saved a polite status region reads exactly Saved', async () => {
    const user = userEvent.setup()
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)
    const row = await screen.findByTestId('tool-row-bash')

    await user.click(within(row).getByRole('button', { name: 'Deny' }))

    await waitFor(() => expect(permissions().patches()).toEqual([{ policies: { bash: 'deny' } }]))
    await waitFor(() => {
      const regions = screen.getAllByRole('status').filter((element) => (element.textContent ?? '').trim() === 'Saved')
      expect(regions.length, 'an element with role status whose text is exactly Saved').toBeGreaterThanOrEqual(1)
      expect(regions[0].getAttribute('aria-live') ?? 'polite', 'the region is polite').toBe('polite')
    })
  })

  it('Accessibility "Allow / Ask / Deny ... in a group named Permission for <name>": the three choices of a tool row sit in the group Permission for bash', async () => {
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)

    await expectChoicesInPermissionGroup(await screen.findByTestId('tool-row-bash'), 'bash')
  })

  it('Accessibility "Allow / Ask / Deny ... in a group named Permission for <name>": the three choices of a skill row sit in the group Permission for trip-notes', async () => {
    renderPage(Page)
    await openGroup(/^tools/i)

    await expectChoicesInPermissionGroup(await screen.findByTestId('skill-row-trip-notes'), 'trip-notes')
  })

  it('Accessibility "Group headers: aria-expanded, aria-controls": every group header of the Skills tab has both', async () => {
    renderPage(Page)
    await screen.findByRole('tab', { name: /^skills/i })
    await openGroup(/^tools/i) // waits until the groups are drawn
    expectEveryHeaderControlsABody('Skills tab')
  })

  it('Accessibility "Group headers: aria-expanded, aria-controls": every group header of the Omnipus tools tab has both', async () => {
    renderPage(Page)
    await openTab(/^omnipus tools/i)
    await openGroup(/^shell/i)
    expectEveryHeaderControlsABody('Omnipus tools tab')
  })

  it('Accessibility "Group headers: aria-expanded, aria-controls": every service header of the External tools tab has both', async () => {
    renderPage(Page)
    await openTab(/^external tools/i)
    await openGroup(/^notion/i)
    expectEveryHeaderControlsABody('External tools tab')
  })
})
