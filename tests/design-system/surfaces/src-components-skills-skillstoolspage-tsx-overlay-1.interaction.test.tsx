// Surface src/components/skills/SkillsToolsPage.tsx#overlay:1 — INTERACTION check (the
// "Web & Search" side panel that the Settings button of the Web & Search group opens).
// The unit check is ...-overlay-1.test.tsx; the browser check is
// tests/e2e/skills-tools-surfaces.spec.ts.
//
// Fix round 4, Part G. Contract test, run on the frontend head 1a140710f, where it is
// GREEN on arrival; not a RED proof. Mutation and proof of failability: deferred to CHECK.
//
// Oracles, from docs/internal/specs/skills-tools-redesign-spec.md at b27c64109, and for
// the keyboard the design system's own contract:
//   S-64, US-9 AS1, FR-048   Settings opens the panel; choosing a new default saves it as
//                            Settings → Integrations did. That earlier behaviour is recorded
//                            in the Settings test IntegrationsSection.adr096 (the choice asks
//                            first, then one write {kind: search, active: true} for that
//                            service) and in the generated IntegrationProviderUpdateRequest
//   US-9 AS1                 "the Default search and Fallback cards and their behaviour,
//                            unchanged": a fallback choice writes {fallback: true}, None
//                            writes {fallback: false}, the stored choice again asks nothing
//                            and writes nothing (all three are in IntegrationsSection.adr096)
//   a confirmation that is cancelled changes nothing (what a confirmation is for)
//   design system, SheetKeyboard story (src/components/ui/overlays.stories.tsx): Enter on the
//                            opener opens the sheet and moves focus to a button inside it
//   WAI-ARIA Authoring Practices, Dialog (Modal) pattern: Escape closes the dialog
//
// Where focus goes after the panel closes is not asserted: the spec asks for focus to
// return only for the "What the model sees" menu (Accessibility and Keyboard).
//
// Only the network edge is doubled: the list requests, the search-service requests and the
// permissions endpoint.
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
    fetchIntegrationProviders: vi.fn(),
    configureIntegrationProvider: vi.fn(),
    fetchAppState: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { useSkillPermissionNetwork } from '../../helpers/skill-permission-network'
import {
  installOverlayPolyfills, openGroup, openTab, renderPage, requireSkillsToolsPage, resetWorld, serveWorld, tool, world,
} from './skills-tools-harness'
import { PLATFORM_APP_STATE, searchProviders } from './skills-tools-search-fixture'

useSkillPermissionNetwork(() => ({
  policies: { fetch_url: 'allow' },
  skill_policies: {},
  visibility: { fetch_url: 'full' },
  skill_visibility: {},
  skill_trust: 'ask',
}))

let Page: ComponentType
beforeEach(async () => {
  installOverlayPolyfills()
  Page = await requireSkillsToolsPage('skills-tools-redesign-spec US-9 AS1, S-64 and FR-048')
  vi.resetAllMocks()
  useUiStore.setState({ toasts: [] })
  resetWorld()
  serveWorld(api)
  world.tools = [tool('fetch_url', { category: 'web' })]
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(searchProviders())
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

// Radix sets pointer-events on the page behind an open panel and jsdom has no layout, so the
// pointer-events guard of user-event is switched off; every click still goes through the real
// event path of the component.
const person = () => userEvent.setup({ pointerEventsCheck: 0 })
const SETTINGS = { name: 'Settings' } as const
const panel = () => screen.findByRole('dialog', { name: 'Web & Search' })

async function openWebGroup() {
  renderPage(Page)
  await openTab(/^omnipus tools/i)
  await openGroup(/^web & search/i)
}
async function openPanel(user: ReturnType<typeof person>) {
  await openWebGroup()
  await user.click(await screen.findByRole('button', SETTINGS))
  return panel()
}
// "Change" on a card, then the service in the list that opens (the same steps Settings had).
async function choose(user: ReturnType<typeof person>, role: 'default' | 'fallback', label: RegExp) {
  const card = await screen.findByTestId(`${role}-search-card`)
  await user.click(within(card).getByRole('button', { name: 'Change' }))
  await user.click(await screen.findByRole('combobox', { name: role === 'default' ? 'Default search' : 'Fallback' }))
  await user.click(await screen.findByRole('option', { name: label }))
}
const answerYes = async (user: ReturnType<typeof person>) => user.click(await screen.findByRole('button', { name: 'Update integration' }))

describe('Web & Search side panel surface interaction contract', () => {
  it('US-9 AS1: Enter on Settings opens the panel with focus on a button inside it, and Escape closes it', async () => {
    const user = person()
    await openWebGroup()
    const settings = await screen.findByRole('button', SETTINGS)
    settings.focus()

    await user.keyboard('{Enter}')

    const dialog = await panel()
    await waitFor(() => expect(dialog.contains(document.activeElement), 'focus is inside the panel').toBe(true))
    expect(document.activeElement?.tagName, 'focus is on a button, as the design system sheet contract expects').toBe('BUTTON')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(screen.queryByTestId('default-search-card'), 'the cards left with the panel').toBeNull()
  })

  it('US-9 AS1: the panel opens again after it was closed and shows the cards again', async () => {
    const user = person()
    await openPanel(user)
    await screen.findByTestId('default-search-card')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await user.click(await screen.findByRole('button', SETTINGS))

    expect(await within(await panel()).findByTestId('default-search-card')).toBeInTheDocument()
    expect(await screen.findByTestId('fallback-search-card')).toBeInTheDocument()
  })

  it('S-64 US-9 AS1: choosing Exa as the new default asks first, then makes one write that makes Exa the default, and the card shows Exa', async () => {
    const user = person()
    let saved = false
    const exaIsDefault = { ...searchProviders(), default_search: 'exa', active_search: 'exa' }
    vi.mocked(api.fetchIntegrationProviders).mockImplementation(async () => (saved ? exaIsDefault : searchProviders()))
    vi.mocked(api.configureIntegrationProvider).mockImplementation(async () => {
      saved = true
      return exaIsDefault
    })
    await openPanel(user)
    expect(within(await screen.findByTestId('default-search-card')).getByText('Tavily')).toBeInTheDocument()

    await choose(user, 'default', /^Exa/i)
    expect(api.configureIntegrationProvider, 'nothing is saved before the person says yes').not.toHaveBeenCalled()
    await answerYes(user)

    await waitFor(() => expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1))
    expect(api.configureIntegrationProvider).toHaveBeenCalledWith('exa', { kind: 'search', active: true }, undefined)
    await waitFor(() => expect(within(screen.getByTestId('default-search-card')).getByText('Exa')).toBeInTheDocument())
  })

  it('US-9 AS1: choosing Exa as the fallback asks first, then makes one write that makes Exa the fallback', async () => {
    const user = person()
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(searchProviders())
    await openPanel(user)

    await choose(user, 'fallback', /^Exa/i)
    expect(api.configureIntegrationProvider, 'nothing is saved before the person says yes').not.toHaveBeenCalled()
    await answerYes(user)

    await waitFor(() => expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1))
    expect(api.configureIntegrationProvider).toHaveBeenCalledWith('exa', { kind: 'search', fallback: true }, undefined)
  })

  it('US-9 AS1: choosing None as the fallback writes fallback false and names no service as the default', async () => {
    const user = person()
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(searchProviders())
    await openPanel(user)

    await choose(user, 'fallback', /^None/i)
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
    await answerYes(user)

    await waitFor(() => expect(api.configureIntegrationProvider).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.configureIntegrationProvider).mock.calls[0][1]).toStrictEqual({ kind: 'search', fallback: false })
  })

  it('US-9 AS1: choosing the stored default again asks nothing and writes nothing', async () => {
    const user = person()
    await openPanel(user)

    await choose(user, 'default', /^Tavily/i)

    expect(screen.queryByRole('alertdialog'), 'no question is asked').toBeNull()
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('S-64: saying no to the question writes nothing and the default stays what it was', async () => {
    const user = person()
    await openPanel(user)
    await choose(user, 'default', /^Exa/i)

    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
    expect(within(screen.getByTestId('default-search-card')).getByText('Tavily')).toBeInTheDocument()
  })
})
