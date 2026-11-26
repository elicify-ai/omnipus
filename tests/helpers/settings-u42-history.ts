// FR-014 / US-2 AS6: invariant oracles from the spec, never from Stay's code.
// Real Settings/router/blocker/drafts. One page lifetime per importing test file.
import { expect, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { GatewayStatus } from '@/lib/api/generated/openapi-types'
import { cleanupNavigationU2, hashAppU2 } from './settings-u2-navigation'
import { jsonG1, transportG1 } from './settings-gate-g1'
import { serialBrowserU42 } from './settings-u42-browser'

let browser: ReturnType<typeof serialBrowserU42> | undefined
export async function cleanupHistoryU42() {
  if (browser) await act(async () => { await browser!.idle() })
  cleanupNavigationU2() // destroy the real router BEFORE restoring browser edges
  browser?.restore()
  browser = undefined
}

function networkU42() {
  const t = transportG1()
  // The inherited G1 /status stub predates generated GatewayStatus validation.
  // Supply only the real HTTP edge; setup errors must not masquerade as history RED.
  const status: GatewayStatus = { online: true, agent_count: 0, channel_count: 0, daily_cost: 0 }
  t.on('GET', '/status', () => jsonG1(status))
  return t
}
function snapshotU42() {
  return { address: window.location.href, state: structuredClone(window.history.state), length: window.history.length }
}
function questionsU42() {
  const seen = new Set<Element>()
  const collect = () => screen.queryAllByRole('alertdialog').forEach((element) => seen.add(element))
  const observer = new MutationObserver(collect)
  observer.observe(document.body, { childList: true, subtree: true, attributes: true })
  return { seen, stop: () => observer.disconnect() }
}
async function dirtyGatewayU42() {
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
}
function expectStayedU42(before: ReturnType<typeof snapshotU42>, questions: ReturnType<typeof questionsU42>) {
  expect.soft(screen.queryByRole('alertdialog'), 'FR-014: no second leave question after Stay settles').not.toBeInTheDocument()
  expect.soft(questions.seen.size, 'FR-014: only the original leave question, not a transient second question').toBe(1)
  expect.soft(window.location.href, 'FR-014: Stay keeps the exact Gateway address').toBe(before.address)
  expect.soft(window.history.state, 'FR-014: Stay keeps the active entry state').toEqual(before.state)
  expect.soft(window.history.length, 'FR-014: Stay adds no rollback entry').toBe(before.length)
  expect.soft(screen.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
  expect.soft(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
  expect.soft(screen.getByText('1 unsaved change')).toBeVisible()
}

// 600ms is the slow-platform INPUT: twice CHECK's documented old 300ms wait.
// 900ms drains pending application/native tasks (three old wait windows), not
// an elapsed-time acceptance threshold. Neither value asserts performance.
export async function serialStayU42(direction: 'back' | 'forward', withNavigation: boolean, undoDelay: number) {
  const t = networkU42()
  browser = serialBrowserU42(withNavigation)
  const edge = browser
  const view = await hashAppU2('/settings?tab=limits')
  await screen.findByRole('heading', { name: 'Limits', level: 2 })
  await userEvent.click(screen.getByTestId(direction === 'back' ? 'settings-nav-about' : 'settings-nav-gateway'))
  await screen.findByRole('heading', { name: direction === 'back' ? 'About' : 'Gateway', level: 2 })
  await userEvent.click(screen.getByTestId(direction === 'back' ? 'settings-nav-gateway' : 'settings-nav-about'))
  await screen.findByRole('heading', { name: direction === 'back' ? 'Gateway' : 'About', level: 2 })
  if (direction === 'forward') {
    await act(async () => { window.history.back(); await edge.idle() })
    await screen.findByRole('heading', { name: 'Gateway', level: 2 })
  }
  await waitFor(() => expect(view.client.isFetching()).toBe(0))
  await dirtyGatewayU42()
  const before = snapshotU42()
  expect(edge.navigation.entries().map((entry) => new URL(entry.url).hash), 'instrument: leading entry prevents a boundary no-op hiding extra traversal').toEqual(
    direction === 'back'
      ? ['#/settings?tab=limits', '#/settings?tab=about', '#/settings?tab=gateway']
      : ['#/settings?tab=limits', '#/settings?tab=gateway', '#/settings?tab=about'],
  )
  const questions = questionsU42()
  try {
    await act(async () => { window.history[direction](); await edge.idle() })
    const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
    await waitFor(() => expect(questions.seen.size).toBe(1))
    expect(window.location.hash, 'instrument: browser traversal really leaves Gateway before Stay').toBe('#/settings?tab=about')
    expect(edge.index, 'instrument: Back reaches the middle entry; Forward reaches the NEWEST').toBe(direction === 'back' ? 1 : 2)
    if (withNavigation) expect(Reflect.get(window, 'navigation').canGoForward, 'truthful entry-list answer, not a convenient constant').toBe(direction === 'back')
    edge.delayGo(undoDelay)
    await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
    await act(async () => {
      await edge.idle()
      await new Promise((resolve) => setTimeout(resolve, 900))
      await edge.idle()
    })
    console.info('R42_HISTORY ' + JSON.stringify({ direction, withNavigation, undoDelay, before, after: snapshotU42(),
      questions: questions.seen.size, index: edge.index, requests: edge.requests, applied: edge.applied, reloads: edge.reloads }))
    expect.soft(edge.reloads, 'FR-014: Stay never requests a zero-delta document reload').toEqual([])
    expectStayedU42(before, questions)
    expect.soft(edge.index, 'FR-014: the active Gateway ENTRY, not only its text, is restored').toBe(direction === 'back' ? 2 : 1)
    expect.soft(view.router.state.location.search).toMatchObject({ tab: 'gateway' })
    expect(t.writes(), 'Stay never saves the draft').toEqual([])
  } finally { questions.stop() }
}

export async function leadingControlU42() {
  const t = networkU42()
  vi.stubGlobal('navigation', undefined)
  const view = await hashAppU2('/settings?tab=limits')
  await screen.findByRole('heading', { name: 'Limits', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-about'))
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  await waitFor(() => expect(view.client.isFetching()).toBe(0))
  await dirtyGatewayU42()
  const before = snapshotU42()
  const questions = questionsU42()
  try {
    await act(async () => { window.history.back() })
    const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
    expect(window.location.hash, 'control: blocked About has Limits before it, not a history boundary').toBe('#/settings?tab=about')
    await waitFor(() => expect(questions.seen.size).toBe(1))
    await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 900)) })
    console.info('R42_LEADING_CONTROL ' + JSON.stringify({ before, after: snapshotU42(), questions: questions.seen.size }))
    expectStayedU42(before, questions)
    expect(t.writes()).toEqual([])
  } finally { questions.stop() }
}
