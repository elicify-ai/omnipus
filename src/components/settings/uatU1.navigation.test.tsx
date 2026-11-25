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
import { clientG1, heldG1, jsonG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

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

it.each([false, true])('U1-C6 — deferred restart banner respects the focus after Later (person moved focus: %s)', async (moved) => {
  const t = transportG1()
  const held = heldG1()
  const entry = { key: 'gateway.port', applied_value: 5000, persisted_value: 8081, reason: 'restart_required' }
  let saved = false
  t.on('PUT', '/config', ({ body }) => {
    expect(body).toEqual({ gateway: { port: 8081 } })
    saved = true
    return jsonG1({ saved: true, requires_restart: true })
  })
  t.on('GET', '/config/pending-restart', () => saved ? held.promise : jsonG1([]))
  try {
    await hashApp('/settings?tab=gateway')
    fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    const offer = await screen.findByRole('dialog', { name: 'Restart now?' })
    await waitFor(() => expect(t.calls.filter((call) => call.path === '/config/pending-restart').length).toBeGreaterThanOrEqual(2))
    expect(screen.queryByText('1 saved change waits for a gateway restart')).not.toBeInTheDocument()
    await userEvent.click(within(offer).getByRole('button', { name: 'Later' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Restart now?' })).not.toBeInTheDocument())
    const heading = screen.getByRole('heading', { name: 'Gateway', level: 2 })
    await waitFor(() => expect(heading, 'FR-035: heading holds focus until the banner exists').toHaveFocus())
    const field = screen.getByRole('spinbutton', { name: 'Port' })
    if (moved) {
      await userEvent.click(field)
      expect(field).toHaveFocus()
    }
    held.resolve(jsonG1([entry]))
    expect(await screen.findByText('1 saved change waits for a gateway restart')).toBeVisible()
    // Let the rendered banner's effects run before a negative focus assertion;
    // an immediate pre-effect match would falsely accept a later focus steal.
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
    await waitFor(() => expect(moved ? field : screen.getByRole('button', { name: 'Restart now' }),
      moved ? 'U1-C6: late banner must not steal focus the person moved' : 'U1-C6/FR-035: late banner must take focus after Later').toHaveFocus())
    expect(t.writes('/gateway/restart')).toEqual([])
  } finally {
    held.resolve(jsonG1([entry]))
  }
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

it.each(['/#/settings?tab=sandbox', '//example.com/x', 'https://example.com/x'])(
  'U1-C9 — remediation anchor preserves an already routed or external address: %s', async (href) => {
    const t = transportG1()
    t.on('GET', '/doctor', () => jsonG1({ score: 50, checked_at: '2026-11-24T00:00:00Z', issues: [
      { id: 'u1-guard', severity: 'high', title: 'Review this setting', description: 'Synthetic diagnostic.', recommendation: 'Follow the action.', action_label: 'Fix', action_link: href },
    ] }))
    await hashApp('/settings?tab=protection')
    await userEvent.click(await screen.findByRole('button', { name: /Review this setting/i }))
    const link = await screen.findByRole('link', { name: 'Fix' })
    // D-9 id35 + CHECK C9: leave already-hashed, // and external URLs intact.
    expect(link.getAttribute('href')).toBe(href)
    expect(link).toHaveTextContent(/^Fix$/)
    expect(t.writes()).toEqual([])
  },
)

it('U1-8 — same provider/model prefill reopens a filled override after Discard without a reload', async () => {
  const t = transportG1()
  const view = await hashApp('/settings?tab=models&provider=openrouter&model=acme%2Fm1')
  expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeVisible()
  expect(await screen.findByRole('textbox', { name: 'Override 1 provider' })).toHaveValue('openrouter')
  expect(screen.getByRole('textbox', { name: 'Override 1 model' })).toHaveValue('acme/m1')
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  expect(await screen.findByTestId('context-overrides-empty')).toHaveTextContent(/^No overrides\./)
  const group = screen.getByRole('button', { name: /Model overrides/ })
  expect(group).toHaveAttribute('aria-expanded', 'true')
  await userEvent.click(group)
  expect(group, 'U1-C7 premise: the repeated link must actually reopen a closed group').toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByRole('textbox', { name: 'Override 1 provider' })).not.toBeInTheDocument()
  // The address-bar/hash edge changes in-app, retaining the mounted form and
  // QueryClient. No render, router replacement, document load or query reset.
  await act(async () => { view.history.push('/settings?provider=openrouter&model=acme%2Fm1') })
  await waitFor(() => expect(view.router.state.location.search).toMatchObject({ provider: 'openrouter', model: 'acme/m1' }))
  expect(screen.getByRole('button', { name: /Model overrides/ })).toHaveAttribute('aria-expanded', 'true')
  await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Override 1 provider' }),
    'U1-8/D-3: repeating the same prefill after Discard must create the unsaved row again').not.toBeNull())
  expect(screen.getByRole('textbox', { name: 'Override 1 provider' })).toHaveValue('openrouter')
  expect(screen.getByRole('textbox', { name: 'Override 1 provider' })).toBeVisible()
  expect(screen.getByRole('textbox', { name: 'Override 1 model' })).toHaveValue('acme/m1')
  expect(screen.getByRole('textbox', { name: 'Override 1 model' })).toBeVisible()
  expect(screen.getByText('1 unsaved change')).toBeVisible()
  expect(t.writes()).toEqual([])
})

it('U1-C7 — a phone prefill-only navigation opens the Limits section, not the section list', async () => {
  const t = transportG1()
  vi.stubGlobal('innerWidth', 390) // US-9 phone fixture; matchMedia remains below the desktop breakpoint.
  const view = await hashApp('/settings')
  await screen.findByTestId('provider-row-openrouter')
  const content = document.getElementById('settings-content')!
  expect(content, 'phone list-first premise').toHaveClass('hidden')
  // A fresh prefilled mount alone would miss the section-opening effect:
  // retain the mounted list and follow a real prefill-only router navigation.
  await act(async () => { view.history.push('/settings?provider=openrouter&model=acme%2Fm1') })
  expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeVisible()
  await waitFor(() => expect(content, 'FR-004: phone section visibility contract').not.toHaveClass('hidden'))
  expect(content).toHaveClass('block')
  expect(screen.getByTestId('settings-nav-limits').closest('nav')?.parentElement).toHaveClass('hidden')
  expect(screen.getByRole('button', { name: /Model overrides/ })).toHaveAttribute('aria-expanded', 'true')
  expect(await screen.findByRole('textbox', { name: 'Override 1 provider' })).toHaveValue('openrouter')
  expect(screen.getByRole('textbox', { name: 'Override 1 provider' })).toBeVisible()
  expect(screen.getByRole('textbox', { name: 'Override 1 model' })).toHaveValue('acme/m1')
  expect(screen.getByRole('textbox', { name: 'Override 1 model' })).toBeVisible()
  expect(screen.getByText('1 unsaved change')).toBeVisible()
  expect(t.writes()).toEqual([])
  // jsdom proves the phone DOM/CSS visibility contract, not rendered pixels.
})

describe('U1-9 — cancelled real hash-history navigation retains location, draft and history', () => {
  it.each(['Back', 'typed hash', 'Back to native state', 'Back with reported growth'] as const)('%s then Stay keeps Gateway in the address and on screen', async (kind) => {
    transportG1()
    // U1-C5/H2: the extra deferred back must have somewhere observable to go.
    window.history.replaceState({}, '', '/#/profile')
    window.history.pushState({}, '', '/#/settings?tab=about')
    const view = await hashApp('/settings?tab=about')
    await screen.findByRole('heading', { name: 'About', level: 2 })
    if (kind === 'Back to native state') window.history.replaceState(null, '', window.location.href)
    await userEvent.click(screen.getByTestId('settings-nav-gateway'))
    const port = await screen.findByRole('spinbutton', { name: 'Port' })
    fireEvent.change(port, { target: { value: '8081' } })
    await screen.findByText('1 unsaved change')
    const address = window.location.href
    const activeState: unknown = JSON.parse(JSON.stringify(window.history.state))
    const historyLength = window.history.length
    if (kind === 'Back with reported growth') {
      // Browser-input fixture only: keep native entries/state/pop events real.
      // Numeric equality under this getter is NOT an entry-count proof.
      vi.spyOn(window.history, 'length', 'get').mockReturnValue(historyLength + 1)
    }
    let firstPop: { state: unknown; length: number } | undefined
    const observe = (event: PopStateEvent) => { firstPop ??= { state: event.state, length: window.history.length } }
    window.addEventListener('popstate', observe)
    try {
      await act(async () => {
        if (kind === 'typed hash') window.location.hash = '/settings?tab=limits'
        else window.history.back()
      })
      const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
      await waitFor(() => expect(firstPop, 'instrument: a real native pop arrived').not.toBeUndefined())
      const input = firstPop!
      const routerState = !!input.state && typeof input.state === 'object' && '__TSR_index' in input.state
      expect(routerState, 'instrument: independent native-state input class').toBe(kind === 'Back' || kind === 'Back with reported growth')
      expect(input.length, 'instrument: independent reported-length input class').toBe(historyLength + (kind === 'typed hash' || kind === 'Back with reported growth' ? 1 : 0))
      const promptLength = window.history.length
      await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
      await waitFor(() => expect(screen.queryByRole('alertdialog', { name: 'Leave without saving 1 change?' })).not.toBeInTheDocument())
      expect(screen.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
      expect(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
      await waitFor(() => expect(window.location.href, 'U1-9/US-2 AS6: Stay cannot leave another section in the address').toBe(address))
      // H2's settle is a deferred-task observation, not an elapsed-time oracle.
      await act(async () => { await new Promise((resolve) => setTimeout(resolve, 200)) })
      expect(window.location.href, 'U1-C5: Gateway address survives the deferred native step').toBe(address)
      expect(screen.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
      expect(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
      expect(view.router.state.location.search).toMatchObject({ tab: 'gateway' })
      expect(window.history.state, 'FR-014: retain the exact active entry state').toEqual(activeState)
      expect(window.history.length, 'Stay preserves existing browser history').toBeGreaterThanOrEqual(historyLength)
      expect(window.history.length, 'Stay adds no rollback entry after the browser move').toBe(promptLength)
      // Once the draft is discarded, Back must still reach the preceding About.
      await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
      await act(async () => { view.history.back() })
      expect(await screen.findByRole('heading', { name: 'About', level: 2 })).toBeVisible()
    } finally {
      window.removeEventListener('popstate', observe)
    }
  })
})
