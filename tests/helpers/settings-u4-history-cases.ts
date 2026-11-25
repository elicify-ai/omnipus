// FR-014 / US-2 AS6, T4-1/2/3. Shared assertions, NOT a shared page lifetime.
// Each test invokes one flow in its own file/process; no application state is shared.
// Do not reset the module graph mid-file: TanStack's route reload state is not
// reset with it. Fresh Vitest files isolate the real Settings module's shown set.
import { expect, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { hashAppU2 } from './settings-u2-navigation'
import { transportG1 } from './settings-gate-g1'

// Same 200ms native-event drain as CHECK H6/H7b7, not a performance oracle.
async function stayAndSettleU4() {
  const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
  await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 200)) })
}
function snapshotU4() {
  return { address: window.location.href, state: structuredClone(window.history.state), length: window.history.length }
}
function expectStayedU4(before: ReturnType<typeof snapshotU4>) {
  expect.soft(screen.queryByRole('alertdialog'), 'FR-014: Stay must not cause a second leave question').not.toBeInTheDocument()
  expect.soft(window.location.href, 'FR-014: original Gateway address survives the settled Back/Stay').toBe(before.address)
  expect.soft(window.history.state, 'FR-014: original active entry state is preserved').toEqual(before.state)
  expect.soft(window.history.length, 'FR-014: Stay adds no rollback entry').toBe(before.length)
  expect.soft(screen.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
  expect.soft(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
  expect.soft(screen.getByText('1 unsaved change')).toBeVisible()
}

export async function typedStayU4(capacity: 'uncapped' | 'at the observed 50-entry cap') {
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
  // T4-3: create recording pressure in THIS page lifetime, not in a prior test.
  // A blocked address was never shown. Retyping it must still restore Gateway.
  for (const attempt of [2, 3]) {
    await act(async () => { window.location.hash = '/settings?tab=limits' })
    await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
    expect(window.location.hash, `repeat ${attempt}: native input premise`).toBe('#/settings?tab=limits')
    const length = window.history.length
    await stayAndSettleU4()
    expectStayedU4({ address, state, length })
    expect(view.router.state.location.search).toMatchObject({ tab: 'gateway' })
    expect(t.writes(), `repeat ${attempt}: Stay remains draft-only`).toEqual([])
  }
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  await act(async () => { view.history.back() })
  expect(await screen.findByRole('heading', { name: 'About', level: 2 }),
    'FR-014: the preceding About entry remains reachable after Discard').toBeVisible()
}

export async function routerOwnedStayU4() {
  const t = transportG1()
  await hashAppU2('/settings?tab=about')
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const before = snapshotU4()
  await act(async () => { window.history.back() })
  await stayAndSettleU4()
  expectStayedU4(before)
  expect(t.writes()).toEqual([])
}

export async function olderTypedBackStayU4() {
  const t = transportG1()
  const view = await hashAppU2('/settings?tab=about')
  await screen.findByRole('heading', { name: 'About', level: 2 })
  // A typed entry is state-less, not pushState({}) (CHECK withdrew that bad fixture).
  await act(async () => { window.location.hash = '/elsewhere0' })
  await screen.findByText('This page drifted into the deep.')
  await act(async () => { window.location.hash = '/elsewhere' })
  await waitFor(() => expect(view.router.state.location.pathname).toBe('/elsewhere'))
  expect(window.history.state ?? null, 'H7b7 input: the second native entry has no router state').toBeNull()
  await act(async () => { await view.router.navigate({ to: '/settings', search: { tab: 'gateway' } }) })
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const before = snapshotU4()
  await act(async () => { window.history.back() })
  await stayAndSettleU4()
  expectStayedU4(before)
  expect(t.writes()).toEqual([])
}

export async function shownAddressRetypeStayU4() {
  const t = transportG1()
  await hashAppU2('/settings?tab=about')
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await act(async () => { window.location.hash = '/settings?tab=limits' })
  await screen.findByRole('heading', { name: 'Limits', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const before = snapshotU4()
  await act(async () => { window.location.hash = '/settings?tab=limits' })
  expect(window.history.length, 'H6 premise: uncapped typed address grows native history').toBeGreaterThan(before.length)
  const atPrompt = { ...before, length: window.history.length }
  await stayAndSettleU4()
  expectStayedU4(atPrompt)
  expect(t.writes()).toEqual([])
}

// U4-1's brief requires deciding after router restore. Neither oracle depends
// on retaining today's flags/set. CHECK must rederive M9h/M9m if they are removed.
export async function afterStayRecordingU4() {
  const t = transportG1()
  await hashAppU2('/settings?tab=about')
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const first = snapshotU4()
  await act(async () => { window.location.hash = '/settings?tab=memory' })
  await screen.findByRole('alertdialog')
  const firstLength = window.history.length
  await stayAndSettleU4()
  expectStayedU4({ ...first, length: firstLength })
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  await act(async () => { window.location.hash = '/settings?tab=limits' })
  await screen.findByRole('heading', { name: 'Limits', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const second = snapshotU4()
  await act(async () => { window.history.back() })
  await stayAndSettleU4()
  expectStayedU4(second)
  expect(t.writes()).toEqual([])
}

export async function distinctShownKeyU4() {
  const t = transportG1()
  vi.spyOn(window.history, 'length', 'get').mockReturnValue(50)
  await hashAppU2('/settings?tab=about')
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await act(async () => { window.location.hash = '/settings?tab=limits' })
  await screen.findByRole('heading', { name: 'Limits', level: 2 })
  const shown = new URL(window.location.href)
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const before = snapshotU4()
  await act(async () => { window.location.hash = '/settings?tab=data' })
  await screen.findByRole('alertdialog')
  const typed = new URL(window.location.href)
  expect(typed.pathname).toBe(shown.pathname)
  expect(typed.hash).toBe('#/settings?tab=data')
  expect(shown.hash).toBe('#/settings?tab=limits')
  expect(window.history.length).toBe(50)
  await stayAndSettleU4()
  expectStayedU4(before)
  expect(t.writes()).toEqual([])
}
