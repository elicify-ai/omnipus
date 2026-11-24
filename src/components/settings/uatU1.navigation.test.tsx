// U1-3/6/7/8/9. Oracles: settings-redesign-spec.md::US-9 AS4,
// US-4 AS2 (Later), D-9 id35, D-3 rows13/14, US-2 AS6; docs/settings.md.
// Actual AppShell, routeTree, hash history, drafts, Save and dialogs. Only
// HTTP/browser capabilities are replaced. No component/hook/router spies.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import { clientG1, jsonG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

const originalLocation = window.location.href
const histories: ReturnType<typeof createHashHistory>[] = []
const clients: ReturnType<typeof clientG1>[] = []
// The shell owns a live-chat socket even on Settings. Hold that unrelated
// external transport; do not open network sockets or mock its app provider.
beforeEach(() => {
  vi.stubGlobal('WebSocket', class {
    static OPEN = 1; static CLOSED = 3
    readyState = 0
    onopen = null; onmessage = null; onerror = null; onclose = null
    send() {}
    close() { this.readyState = 3 }
  })
  vi.stubGlobal('Worker', class { onmessage = null; onerror = null; postMessage() {} terminate() {} })
})
afterEach(() => {
  cleanup()
  histories.splice(0).forEach((h) => h.destroy())
  clients.splice(0).forEach((c) => c.clear())
  window.history.replaceState({}, '', originalLocation)
  vi.unstubAllGlobals(); vi.restoreAllMocks()
})

async function hashApp(path: string) {
  window.history.replaceState({}, '', `/#${path}`)
  const history = createHashHistory()
  histories.push(history)
  const client = clientG1()
  clients.push(client)
  const router = createRouter({ routeTree, history })
  await act(async () => { await router.load() })
  const view = render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  await screen.findByRole('heading', { name: 'Settings' })
  return { ...view, router, history, client }
}

it('U1-3 — fresh Settings address: first Tab focuses Skip to settings content and Enter leaves the address unchanged', async () => {
  transportG1()
  await hashApp('/settings?tab=gateway')
  await screen.findByRole('spinbutton', { name: 'Port' })
  expect(document.activeElement, 'fresh-address premise: no control was focused by the fixture').toBe(document.body)
  const address = window.location.href
  const user = userEvent.setup()
  await user.tab()
  expect(screen.getByRole('link', { name: 'Skip to settings content' }), 'U1-3/US-9 AS4: the first Tab belongs to Settings, not the global shell').toHaveFocus()
  await user.keyboard('{Enter}')
  expect(document.getElementById('settings-content')).toHaveFocus()
  expect(window.location.href).toBe(address)
})

it('U1-6 — real Port Save then Later focuses the restart banner action, not the section heading', async () => {
  const t = transportG1()
  const entry = { key: 'gateway.port', applied_value: 5000, persisted_value: 8081, reason: 'restart_required' }
  let saved = false
  t.on('PUT', '/config', ({ body }) => {
    expect(body, 'instrument: real section Save serializes the changed Port only').toEqual({ gateway: { port: 8081 } })
    saved = true
    return jsonG1({ saved: true, requires_restart: true })
  })
  t.on('GET', '/config/pending-restart', () => jsonG1(saved ? [entry] : []))
  await hashApp('/settings?tab=gateway')
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  const offer = await screen.findByRole('dialog', { name: 'Restart now?' })
  expect(within(offer).getByTestId('restart-labels')).toHaveTextContent('Port')
  await userEvent.click(within(offer).getByRole('button', { name: 'Later' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Restart now?' })).not.toBeInTheDocument())
  expect(await screen.findByText('1 saved change waits for a gateway restart')).toBeVisible()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Restart now' }),
    'U1-6/US-4 AS2: Later returns focus to Restart now in the persistent banner').toHaveFocus())
  expect(t.writes('/gateway/restart'), 'Later must not restart the gateway').toEqual([])
})

it('U1-7 — actual sandbox-health remediation URL resolves to Sandbox & network under production hash routing', async () => {
  const t = transportG1()
  // Exact action_link shape emitted by the gateway doctor, with synthetic
  // issue text. The expected destination comes from D-9 inventory id35.
  t.on('GET', '/doctor', () => jsonG1({ score: 50, checked_at: '2026-11-24T00:00:00Z', issues: [
    { id: 'u1-sandbox', severity: 'high', title: 'Sandbox is off', description: 'No kernel confinement.', recommendation: 'Review sandbox mode.', action_label: 'Fix', action_link: '/settings?tab=sandbox' },
  ] }))
  const view = await hashApp('/settings?tab=protection')
  await userEvent.click(await screen.findByRole('button', { name: /Sandbox is off/i }))
  const link = await screen.findByRole('link', { name: 'Fix' })
  const target = new URL(link.getAttribute('href')!, window.location.href)
  view.unmount()
  view.history.destroy()
  histories.splice(histories.indexOf(view.history), 1)
  // jsdom cannot follow an anchor via a document navigation. Transfer the
  // actual anchor's exact routing bytes, then resolve the real routeTree.
  window.history.replaceState({}, '', target.pathname + target.search + target.hash)
  const history = createHashHistory()
  histories.push(history)
  const router = createRouter({ routeTree, history })
  await act(async () => { await router.load() })
  expect(router.state.location.pathname, 'U1-7/D-9 id35: the health Fix must not resolve to chat home').toBe('/settings')
  expect(router.state.location.search).toMatchObject({ tab: 'sandbox' })
  render(<QueryClientProvider client={view.client}><RouterProvider router={router} /></QueryClientProvider>)
  expect(await screen.findByRole('heading', { name: 'Sandbox & network', level: 2 })).toBeVisible()
})

it('U1-8 — same provider/model prefill reopens a filled override after Discard without a reload', async () => {
  const t = transportG1()
  const view = await hashApp('/settings?tab=models&provider=openrouter&model=acme%2Fm1')
  expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeVisible()
  expect(await screen.findByRole('textbox', { name: 'Override 1 provider' })).toHaveValue('openrouter')
  expect(screen.getByRole('textbox', { name: 'Override 1 model' })).toHaveValue('acme/m1')
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  expect(await screen.findByTestId('context-overrides-empty')).toHaveTextContent(/^No overrides\./)
  // The address-bar/hash edge changes in-app, retaining the mounted form and
  // QueryClient. No render, router replacement, document load or query reset.
  await act(async () => { view.history.push('/settings?provider=openrouter&model=acme%2Fm1') })
  await waitFor(() => expect(view.router.state.location.search).toMatchObject({ provider: 'openrouter', model: 'acme/m1' }))
  expect(screen.getByRole('button', { name: /Model overrides/ })).toHaveAttribute('aria-expanded', 'true')
  await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Override 1 provider' }),
    'U1-8/D-3: repeating the same prefill after Discard must create the unsaved row again').not.toBeNull())
  expect(screen.getByRole('textbox', { name: 'Override 1 provider' })).toHaveValue('openrouter')
  expect(screen.getByRole('textbox', { name: 'Override 1 model' })).toHaveValue('acme/m1')
  expect(t.writes()).toEqual([])
})

describe('U1-9 — cancelled real hash-history navigation retains location, draft and history', () => {
  it.each(['Back', 'typed hash'] as const)('%s then Stay keeps Gateway in the address and on screen', async (kind) => {
    transportG1()
    const view = await hashApp('/settings?tab=about')
    await screen.findByRole('heading', { name: 'About', level: 2 })
    await userEvent.click(screen.getByTestId('settings-nav-gateway'))
    const port = await screen.findByRole('spinbutton', { name: 'Port' })
    fireEvent.change(port, { target: { value: '8081' } })
    await screen.findByText('1 unsaved change')
    const address = window.location.href
    const historyLength = window.history.length
    await act(async () => {
      if (kind === 'Back') window.history.back()
      else window.location.hash = '/settings?tab=limits'
    })
    const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
    await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog', { name: 'Leave without saving 1 change?' })).not.toBeInTheDocument())
    expect(screen.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
    expect(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
    await waitFor(() => expect(window.location.href, 'U1-9/US-2 AS6: Stay cannot leave another section in the address').toBe(address))
    expect(view.router.state.location.search).toMatchObject({ tab: 'gateway' })
    expect(window.history.length, 'Stay preserves existing browser history').toBeGreaterThanOrEqual(historyLength)
    // Once the draft is discarded, Back must still reach the preceding About.
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    await act(async () => { view.history.back() })
    expect(await screen.findByRole('heading', { name: 'About', level: 2 })).toBeVisible()
  })
})
