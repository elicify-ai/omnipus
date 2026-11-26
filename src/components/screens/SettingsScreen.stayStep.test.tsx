// U5-1 follow-up — Stay after a browser move whose router step count is wrong.
// Spec G-06: Stay MUST keep the section, its changes, the address and the
// history unchanged. Setup: router entries About, Limits, Memory, then a
// Gateway address typed into the bar (no router state), Port edited.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { browserEdgesU2, cleanupNavigationU2, hashAppU2 } from '../../../tests/helpers/settings-u2-navigation'
import { transportG1 } from '../../../tests/helpers/settings-gate-g1'

beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)

async function typedGatewayAfterRouterEntries() {
  const t = transportG1()
  const view = await hashAppU2('/settings?tab=about')
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await act(async () => { await view.router.navigate({ to: '/settings', search: { tab: 'limits' } }) })
  await screen.findByRole('heading', { name: 'Limits', level: 2 })
  await act(async () => { await view.router.navigate({ to: '/settings', search: { tab: 'memory' } }) })
  await screen.findByRole('heading', { name: 'Memory & recaps', level: 2 })
  await act(async () => { window.location.hash = '/settings?tab=gateway' })
  expect(window.history.state ?? null, 'premise: a typed entry has no router state').toBeNull()
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  return { t, address: window.location.href, length: window.history.length }
}

async function stayAndExpectGateway(before: { address: string, length: number }, t: ReturnType<typeof transportG1>) {
  const go = vi.spyOn(window.history, 'go')
  const unload = vi.fn()
  window.addEventListener('beforeunload', unload)
  try {
    const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
    await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    // Let the history's own restore and any late browser event arrive.
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 600)) })
  } finally {
    window.removeEventListener('beforeunload', unload)
  }
  expect.soft(go.mock.calls.filter(([delta]) => !delta), 'Stay never reloads (history.go(0))').toEqual([])
  expect.soft(unload, 'no browser leave question').not.toHaveBeenCalled()
  expect.soft(window.location.href, 'G-06: Stay keeps the Gateway address').toBe(before.address)
  expect.soft(window.history.length, 'G-06: Stay adds no history entry').toBe(before.length)
  expect.soft(screen.queryByRole('alertdialog'), 'no second question').not.toBeInTheDocument()
  expect.soft(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
  expect(t.writes(), 'Stay saves nothing').toEqual([])
}

it('one Back from a typed Gateway onto a router entry (router step count 2) — Stay returns to Gateway', async () => {
  const { t, address, length } = await typedGatewayAfterRouterEntries()
  await act(async () => { window.history.back() })
  await stayAndExpectGateway({ address, length }, t)
})

const hasNavigationApi = typeof window !== 'undefined' && 'navigation' in window
it.skipIf(!hasNavigationApi)(
  'a history-menu jump two entries back from a typed Gateway — Stay returns to Gateway '
    + '(runs only with the browser Navigation API; jsdom has none, so the hands-on re-test covers it)',
  async () => {
    const { t, address, length } = await typedGatewayAfterRouterEntries()
    await act(async () => { window.history.go(-2) })
    await stayAndExpectGateway({ address, length }, t)
  },
)
