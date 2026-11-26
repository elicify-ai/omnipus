// U6-5 / U6-8 (CHECK U5-C5) / FR-014: a second browser move in the same page
// life after a Stay. Stay must not record the blocked entry as this section's
// home while the history's undo is still on its way, or the next Stay steps to
// the wrong entry. After every Stay the one-off replacement of history.go is
// taken off again (CHECK U5-C3, fault M16). Uses the test author's serialised
// browser edge, with and without the Navigation API.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { browserEdgesU2, cleanupNavigationU2, hashAppU2 } from '../../../tests/helpers/settings-u2-navigation'
import { serialBrowserU42 } from '../../../tests/helpers/settings-u42-browser'
import { jsonG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

let edge: ReturnType<typeof serialBrowserU42> | undefined
beforeEach(browserEdgesU2)
afterEach(async () => {
  if (edge) await act(async () => { await edge!.idle() })
  cleanupNavigationU2()
  edge?.restore()
  edge = undefined
})

async function stayOn(move: () => void) {
  const edgeGo = window.history.go
  await act(async () => { move(); await edge!.idle() })
  const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
  await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
  await act(async () => { await edge!.idle(); await new Promise((r) => setTimeout(r, 400)); await edge!.idle() })
  expect.soft(window.history.go, 'the one-off history.go replacement is taken off after the Stay').toBe(edgeGo)
}

function gatewayStatus() {
  const t = transportG1()
  t.on('GET', '/status', () => jsonG1({ online: true, agent_count: 0, channel_count: 0, daily_cost: 0 }))
}

for (const withNavigation of [true, false]) {
  it(`second move after a Stay (navigation API: ${withNavigation}) — Back one, Stay; jump two, Stay`, async () => {
    gatewayStatus()
    edge = serialBrowserU42(withNavigation)
    const view = await hashAppU2('/settings?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    await userEvent.click(screen.getByTestId('settings-nav-about'))
    await screen.findByRole('heading', { name: 'About', level: 2 })
    await userEvent.click(screen.getByTestId('settings-nav-gateway'))
    await screen.findByRole('heading', { name: 'Gateway', level: 2 })
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
    await screen.findByText('1 unsaved change')
    const address = window.location.href
    await stayOn(() => window.history.back())
    expect.soft(edge.index, 'after the first Stay').toBe(2)
    await stayOn(() => window.history.go(-2))
    expect.soft(edge.reloads).toEqual([])
    expect.soft(edge.index, 'after the second Stay').toBe(2)
    expect.soft(window.location.href).toBe(address)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })
}

for (const withNavigation of [true, false]) {
  it(`second move after a Stay on a typed address (navigation API: ${withNavigation}) — Back, Stay; Back, Stay`, async () => {
    gatewayStatus()
    edge = serialBrowserU42(withNavigation)
    const view = await hashAppU2('/settings?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    // Typed into the address bar: an entry without router state.
    await act(async () => { edge!.typeHash('/settings?tab=gateway') })
    await screen.findByRole('heading', { name: 'Gateway', level: 2 })
    await waitFor(() => expect(view.client.isFetching()).toBe(0))
    fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
    await screen.findByText('1 unsaved change')
    const address = window.location.href
    await stayOn(() => window.history.back())
    expect.soft(edge.index, 'after the first Stay').toBe(1)
    expect.soft(window.location.href, 'after the first Stay').toBe(address)
    await stayOn(() => window.history.back())
    expect.soft(edge.reloads).toEqual([])
    expect.soft(edge.index, 'after the second Stay').toBe(1)
    expect.soft(window.location.href).toBe(address)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
  })
}
