// Part 44 oracles: independent CHECK Round U5 REL1/REL2/RJ/TF0 and accepted limits.
// Real shell/router/blocker/draft; only the same-document browser platform is supplied.
import { expect, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { GatewayStatus } from '@/lib/api/generated/openapi-types'
import { jsonG1, transportG1 } from './settings-gate-g1'
import { cleanupNavigationU2, hashAppU2 } from './settings-u2-navigation'
import { serialBrowserU42 } from './settings-u42-browser'

let browser: ReturnType<typeof serialBrowserU42> | undefined
export async function cleanupHistoryU44() {
  if (browser) await act(async () => { await browser!.idle() })
  cleanupNavigationU2()
  browser?.restore()
  browser = undefined
}

export async function historyU44(shape: 'typed' | 'router' | 'first' | 'typed-jump' | 'typed-target-jump' | 'late-typed', withNavigation: boolean) {
  const t = transportG1()
  const status: GatewayStatus = { online: true, agent_count: 0, channel_count: 0, daily_cost: 0 }
  t.on('GET', '/status', () => jsonG1(status))
  browser = serialBrowserU42(withNavigation)
  const edge = browser
  if (withNavigation) {
    // Own small extension; leave U42's instrument and delayed case byte-identical.
    const navigation = Object.create(edge.navigation) as typeof edge.navigation & { readonly canGoBack: boolean }
    Object.defineProperty(navigation, 'canGoBack', { get: () => edge.index > 0 })
    vi.stubGlobal('navigation', navigation)
  }
  const jumpSource = shape === 'typed-jump' || shape === 'late-typed'
  const initial = shape === 'first' ? 'gateway' : jumpSource ? 'about' : 'limits'
  const view = await hashAppU2(`/settings?tab=${initial}`)
  await screen.findByRole('heading', { name: initial === 'gateway' ? 'Gateway' : initial === 'about' ? 'About' : 'Limits', level: 2 })
  const navigate = async (tab: 'about' | 'gateway' | 'memory' | 'limits') => {
    await act(async () => { await view.router.navigate({ to: '/settings', search: { tab } }) })
    await screen.findByRole('heading', { name: tab === 'about' ? 'About' : tab === 'memory' ? 'Memory & recaps' : tab === 'limits' ? 'Limits' : 'Gateway', level: 2 })
  }
  if (shape === 'typed') {
    await act(async () => { edge.typeHash('/settings?tab=gateway') })
  } else if (shape === 'router') {
    await navigate('about')
    await navigate('gateway')
  } else if (shape === 'typed-target-jump') {
    await act(async () => { edge.typeHash('/settings?tab=about') })
    await screen.findByRole('heading', { name: 'About', level: 2 })
    await navigate('memory')
    await navigate('gateway')
  } else if (jumpSource) {
    await navigate('limits')
    await navigate('memory')
    await act(async () => { edge.typeHash('/settings?tab=gateway') })
  }
  await waitFor(() => expect(view.client.isFetching(), 'setup: generated-response network reads complete').toBe(0))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const home = snapshotU44(edge)
  const seen = new Set<Element>()
  const observer = new MutationObserver(() => screen.queryAllByRole('alertdialog').forEach((dialog) => seen.add(dialog)))
  observer.observe(document.body, { childList: true, subtree: true })
  return { ...view, t, edge, home, seen, stop: () => observer.disconnect(), navigate }
}

export function snapshotU44(edge: ReturnType<typeof serialBrowserU42>) {
  return { address: window.location.href, state: structuredClone(window.history.state), length: window.history.length,
    index: edge.index, key: edge.navigation.currentEntry.key }
}

export async function moveU44(context: Awaited<ReturnType<typeof historyU44>>, delta: number, back = false) {
  const start = context.edge.index
  await act(async () => {
    if (back) window.history.back()
    else window.history.go(delta)
    await context.edge.idle()
  })
  expect(context.edge.index, 'instrument: requested browser move actually applied').toBe(start + delta)
  return screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
}

export async function stayU44(context: Awaited<ReturnType<typeof historyU44>>, prompt: HTMLElement) {
  await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
  await act(async () => {
    await context.edge.idle()
    // Drains three documented 300ms no-API wait windows, not a performance oracle.
    await new Promise((resolve) => setTimeout(resolve, 900))
    await context.edge.idle()
  })
}

export function stayedU44(context: Awaited<ReturnType<typeof historyU44>>, questions: number, label: string) {
  console.info('R44_HISTORY ' + JSON.stringify({ label, home: context.home, after: snapshotU44(context.edge),
    questions: context.seen.size, requests: context.edge.requests, applied: context.edge.applied, reloads: context.edge.reloads }))
  expect.soft(snapshotU44(context.edge), `${label}: Stay restores the exact entry, address, state and length`).toEqual(context.home)
  expect.soft(context.edge.reloads, `${label}: no history.go(0) or history.go(-0) reload`).toEqual([])
  expect.soft(context.seen.size, `${label}: one question per intentional move, no transient extra question`).toBe(questions)
  expect.soft(screen.queryByRole('alertdialog'), `${label}: Stay leaves no leave question`).not.toBeInTheDocument()
  expect.soft(screen.getByRole('spinbutton', { name: 'Port' }), `${label}: draft is kept`).toHaveValue(8081)
  expect.soft(screen.getByText('1 unsaved change'), `${label}: draft remains unsaved`).toBeVisible()
  expect(context.t.writes(), `${label}: Stay writes nothing`).toEqual([])
}
