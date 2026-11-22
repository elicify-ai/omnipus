// G1 F8/F9; FR-063 / US-4 AS9. Completion needs positive fresh-process AND
// pending-key clearance proof. New-address navigation uses the app's real
// hash history, not a modal-only href expectation. Only clock/fetch are injected.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import { useUiStore } from '@/store/ui'
import { GatewayRestartModal } from './GatewayRestartModal'
import { aboutG1, clientG1, jsonG1, renderG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

const originalLocation = window.location.href
afterEach(() => {
  cleanup()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks()
  window.history.replaceState({}, '', originalLocation)
})
async function polls(n: number) {
  for (let i = 0; i < n; i++) await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
}

describe('F8 FR-063 — fresh process without confirmed pending clearance is not success', () => {
  it.each(['unreadable', 'still-pending'] as const)('%s until the deadline keeps a visible retry and never announces completion', async (failure) => {
    const t = transportG1()
    const key = { key: 'gateway.hot_reload', persisted_value: true, applied_value: false, reason: 'restart_required' }
    t.on('GET', '/config/pending-restart', () => jsonG1([key]))
    t.on('GET', '/about', () => jsonG1({ ...aboutG1, uptime_seconds: 0 }))
    const onRestarted = vi.fn()
    const onClose = vi.fn()
    const { client } = renderG1(<GatewayRestartModal open onClose={onClose} onRestarted={onRestarted} />)
    await waitFor(() => expect(client.getQueryData(['pending-restart'])).toEqual([key]))
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-11-22T12:00:00Z'))
    t.on('GET', '/config/pending-restart', () => failure === 'unreadable'
      ? jsonG1({ error: 'G1 pending verification unavailable' }, 500)
      : jsonG1([key]))
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Restart now' })) })
    // Sixty seconds exhausts verification; two more also expose any delayed
    // automatic close that an incorrect success path scheduled.
    await polls(62)
    expect(t.calls.filter((c) => c.path === '/about').length, 'F8 instrument: the real fresh-process probe must have been reached').toBeGreaterThan(0)
    expect(t.calls.filter((c) => c.path === '/config/pending-restart').length, 'F8 instrument: pending verification must actually have been attempted after the initial read').toBeGreaterThan(1)
    expect.soft(onRestarted, 'F8: exhausting the verification deadline is not affirmative clearance proof').not.toHaveBeenCalled()
    expect.soft(onClose, 'F8: an unconfirmed restart must remain recoverable').not.toHaveBeenCalled()
    expect.soft(screen.queryByText('The gateway restarted.'), 'F8: never show unproved success').not.toBeInTheDocument()
    expect.soft(screen.queryByText(/not confirmed|could not confirm|did not.*confirm|could not.*verify/i), 'F8: explain that restart completion was not confirmed').not.toBeNull()
    expect.soft(screen.queryByRole('button', { name: /restart now|retry/i }), 'F8: retain the recovery action').not.toBeNull()
  })
})

it('F9 FR-063 — following the new-address link through real hash history reaches Gateway restarted handoff', async () => {
  const t = transportG1()
  const runningPort = Number(window.location.port) || (window.location.protocol === 'https:' ? 443 : 80)
  const entry = { key: 'gateway.port', applied_value: runningPort, persisted_value: 8081, reason: 'restart_required' }
  t.on('GET', '/config/pending-restart', () => jsonG1([entry]))
  t.on('GET', '/health', () => { throw new TypeError('G1 old address stopped') })
  const modal = renderG1(<GatewayRestartModal open onClose={() => {}} />)
  await waitFor(() => expect(modal.client.getQueryData(['pending-restart'])).toEqual([entry]))
  fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))
  const link = await screen.findByRole('link', { name: 'Open the new address' })
  const target = new URL(link.getAttribute('href')!, window.location.href)
  expect(target.port).toBe('8081')
  modal.unmount()
  // jsdom cannot navigate to a different origin. Reproduce the link's exact
  // pathname/search/hash at this origin; the routing bytes are unchanged.
  window.history.replaceState({}, '', target.pathname + target.search + target.hash)
  t.on('GET', '/config/pending-restart', () => jsonG1([]))
  const client = clientG1()
  const router = createRouter({ routeTree, history: createHashHistory() })
  // Resolve the actual route before rendering it: a broken link lands at
  // root, which would otherwise start an unrelated chat WebSocket in jsdom.
  await act(async () => { await router.load() })
  expect(router.state.location.pathname, 'F9: the offered URL must route to /settings under the production hash history').toBe('/settings')
  expect(router.state.location.search).toMatchObject({ tab: 'gateway', restarted: 1 })
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
  expect(await screen.findByText(/The gateway restarted/), 'F9: the restarted=1 handoff must reach the actual Settings confirmation path').toBeVisible()
})
