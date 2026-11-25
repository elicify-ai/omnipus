// U2-2 / U2-4. Oracles: US-9 AS4, Accessibility and Keyboard, FR-014 / US-2 AS6.
// The actual AppShell, pinned Sidebar store, routeTree, blocker, drafts and history.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useSidebarStore } from '@/store/sidebar'
import { transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { browserEdgesU2, cleanupNavigationU2, hashAppU2, wideViewportU2 } from '../../../tests/helpers/settings-u2-navigation'

beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)

it('U2-2 — pinned sidebar: the first Tab from a fresh Settings address reaches Skip to settings content', async () => {
  transportG1()
  wideViewportU2()
  useSidebarStore.getState().pin()
  await hashAppU2('/settings?tab=gateway')
  await screen.findByRole('spinbutton', { name: 'Port' })
  expect(screen.getByRole('button', { name: 'Unpin sidebar' }), 'instrument: the real pinned Sidebar is mounted').toBeVisible()
  expect(useSidebarStore.getState().isPinned).toBe(true)
  expect(document.activeElement, 'fresh address premise: the fixture did not focus a control').toBe(document.body)
  const address = window.location.href
  const user = userEvent.setup()
  await user.tab()
  expect(screen.getByRole('link', { name: 'Skip to settings content' }),
    'U2-2/US-9 AS4: pinned navigation must not precede the Settings skip link').toHaveFocus()
  await user.keyboard('{Enter}')
  expect(document.getElementById('settings-content')).toHaveFocus()
  expect(window.location.href).toBe(address)
})

it.each(['uncapped', 'at the observed 50-entry cap'] as const)(
  'U2-4 — typed hash then Stay restores Gateway address and exact active history state without a rollback entry (%s)', async (capacity) => {
  const t = transportG1()
  if (capacity === 'at the observed 50-entry cap') {
    // validator-report-u1.md::G-06: reported length was already capped at 50.
    // Model only that native browser input, not the router/blocker or entries.
    vi.spyOn(window.history, 'length', 'get').mockReturnValue(50)
    expect(window.history.length, 'observed capped-history premise').toBe(50)
  }
  const view = await hashAppU2('/settings?tab=about')
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const address = window.location.href
  const state: unknown = JSON.parse(JSON.stringify(window.history.state))
  expect(state, 'instrument: capture a real router-owned active entry, not null').not.toBeNull()
  await act(async () => { window.location.hash = '/settings?tab=limits' })
  const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
  expect(window.location.hash, 'instrument: the native hash really changed before Stay').toBe('#/settings?tab=limits')
  // Native hash assignment already adds an entry. Stay must not add ANOTHER one.
  // history.length cannot prove that a browser-created entry was deleted.
  const promptHistoryLength = window.history.length
  await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
  await waitFor(() => expect(screen.queryByRole('alertdialog', { name: 'Leave without saving 1 change?' })).not.toBeInTheDocument())
  expect(screen.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
  expect(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
  expect(view.router.state.location.search).toMatchObject({ tab: 'gateway' })
  expect.soft(window.location.href, 'U2-4/FR-014: Stay restores the Gateway address, not Limits').toBe(address)
  expect.soft(window.history.state, 'U2-4/FR-014: Stay restores the exact active history state, not null').toEqual(state)
  expect.soft(window.history.length, 'U2-4/FR-014: Stay creates no extra rollback entry').toBe(promptHistoryLength)
  expect(t.writes(), 'Stay preserves an unsaved draft without persisting it').toEqual([])
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  await act(async () => { view.history.back() })
  expect(await screen.findByRole('heading', { name: 'About', level: 2 }),
    'FR-014: the preceding About entry remains reachable after Discard').toBeVisible()
})
