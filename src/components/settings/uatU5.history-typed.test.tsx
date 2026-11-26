// T5-1 / FR-014 / US-2 AS6: two typed native entries, neither router-owned.
// Real shell, Chat, Settings blocker/draft and hash history. Only HTTP/socket
// and browser observation edges are supplied. One page lifetime in this file.
import { afterEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import type { GatewayStatus } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { json, TestSocket } from '../../../tests/l1-room-ui/harness'
import { MAIN_PATH, Stage1Network, resetStage1App, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { transportG1 } from '../../../tests/helpers/settings-gate-g1'

const originalAddress = window.location.href
const originalState: unknown = window.history.state
let history: ReturnType<typeof createHashHistory> | undefined
const beforeUnload = vi.fn()
afterEach(async () => {
  window.removeEventListener('beforeunload', beforeUnload)
  await stopStage1App()
  history?.destroy()
  window.history.replaceState(originalState, '', originalAddress)
  vi.restoreAllMocks()
})

it('T5-1 — typed Chat then typed Gateway, Back and Stay never reload, ask again or lose the exact Gateway address and draft', async () => {
  const defaults = transportG1().defaults
  resetStage1App()
  const net = new Stage1Network()
  for (const path of ['/config', '/config/pending-restart']) net.respond('GET', `/api/v1${path}`, () => json(defaults[path]))
  // Generated GatewayStatus requires these four fields; the old G1 status
  // stub is not a valid response and must not add an error toast to this flow.
  const status: GatewayStatus = { online: true, agent_count: net.roster.length, channel_count: 1, daily_cost: 0 }
  net.respond('GET', '/api/v1/status', () => json(status))
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  window.addEventListener('beforeunload', beforeUnload)
  // Test the listener BEFORE the product flow; this is not a browser reload claim.
  window.dispatchEvent(new Event('beforeunload', { cancelable: true }))
  expect(beforeUnload).toHaveBeenCalledTimes(1)
  beforeUnload.mockClear()
  window.history.replaceState({}, '', '/#/settings?tab=about')
  history = createHashHistory()
  // Mount the real app directly: the Chat-specific harness requires a shell
  // skip link that Settings intentionally replaces with its own skip target.
  const router = createRouter({ routeTree, history })
  await act(async () => { await router.load() })
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await waitFor(() => expect(TestSocket.instances).toHaveLength(1))
  await act(async () => { TestSocket.instances[0].open() })
  const initialLength = window.history.length
  // Native hash assignment is address-bar/hash-only input, NOT pushState({}).
  await act(async () => { window.location.hash = MAIN_PATH })
  expect(await screen.findByTestId('chat-input'), 'reachability: the actual Chat renders').toBeVisible()
  await waitFor(() => expect(router.state.location.pathname).toBe(MAIN_PATH))
  await waitFor(() => expect(queryClient.isFetching()).toBe(0))
  const chatAddress = window.location.href
  expect(window.history.state ?? null, 'variant C premise: typed Chat has no router state').toBeNull()
  await act(async () => { window.location.hash = '/settings?tab=gateway' })
  await screen.findByRole('heading', { name: 'Gateway', level: 2 })
  await waitFor(() => expect(queryClient.getQueryState(['gateway-status'])?.status,
    'instrument: the real generated status decoder accepted the fixture before the history action').toBe('success'))
  expect(net.unexpected, 'instrument: no missing HTTP dependency before the history action').toEqual([])
  expect(window.history.state ?? null, 'variant C premise: typed Gateway also has no router state').toBeNull()
  expect(window.history.length, 'instrument: both typed addresses really added native entries').toBe(initialLength + 2)
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const before = { address: window.location.href, state: structuredClone(window.history.state), length: window.history.length }
  const go = vi.spyOn(window.history, 'go') // pass-through native browser edge only
  await act(async () => { window.history.go(-1) })
  const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
  expect(go, 'instrument: the pass-through observer sees the actual Back argument').toHaveBeenCalledWith(-1)
  expect(window.location.href, 'instrument: Back really reaches the older typed Chat entry').toBe(chatAddress)
  expect(window.history.state ?? null).toBeNull()
  // At this real Back position Gateway is ahead. Supply that missing browser
  // Navigation API capability, never the blocker or its recovery implementation.
  vi.stubGlobal('navigation', { get canGoForward() { return window.location.href === chatAddress } })
  await userEvent.click(within(prompt).getByRole('button', { name: 'Stay' }))
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  // Same native-event drain as U4 controls, not an elapsed-time/performance oracle.
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 200)) })
  // === catches BOTH 0 and -0 (Object.is/toHaveBeenCalledWith(0) alone misses -0).
  const reloads = go.mock.calls.filter(([delta]) => delta === 0)
  console.info(`T5-1 native history.go calls: ${JSON.stringify(go.mock.calls)}; beforeunload=${beforeUnload.mock.calls.length}`)
  expect.soft(reloads, 'FR-014/T5-1: Stay must never request a zero-delta reload, including negative zero').toEqual([])
  expect.soft(beforeUnload, 'FR-014: Stay must not raise a native leave question; jsdom event absence alone is not browser proof').not.toHaveBeenCalled()
  expect.soft(window.location.href, 'FR-014: Stay retains the exact Gateway address, not the older Chat address').toBe(before.address)
  expect.soft(window.history.state, 'FR-014: Stay retains the exact active entry state').toEqual(before.state)
  expect.soft(window.history.length, 'FR-014: Stay adds no rollback entry').toBe(before.length)
  expect.soft(screen.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
  expect.soft(screen.getByRole('spinbutton', { name: 'Port' })).toHaveValue(8081)
  expect.soft(screen.getByText('1 unsaved change')).toBeVisible()
  expect.soft(screen.queryByRole('alertdialog'), 'FR-014: no second app question after native tasks settle').not.toBeInTheDocument()
  expect(net.requests.filter((request) => request.method !== 'GET'), 'Stay never saves the draft').toEqual([])
  expect(net.unexpected, 'HTTP setup errors cannot masquerade as history RED').toEqual([])
})
