// Real shell/router support for U2. Only external browser capabilities are supplied.
import { act, cleanup, render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { vi } from 'vitest'
import { routeTree } from '@/routeTree.gen'
import { useSidebarStore } from '@/store/sidebar'
import { clientG1 } from './settings-gate-g1'

const originalAddress = window.location.href
const originalHistoryState: unknown = window.history.state
const originalSidebar = useSidebarStore.getState()
const originalSidebarStorage = localStorage.getItem('omnipus-sidebar')
const histories: ReturnType<typeof createHashHistory>[] = []
const clients: ReturnType<typeof clientG1>[] = []

export function browserEdgesU2() {
  vi.stubGlobal('WebSocket', class {
    static OPEN = 1; static CLOSED = 3
    readyState = 0
    onopen = null; onmessage = null; onerror = null; onclose = null
    send() {}
    close() { this.readyState = 3 }
  })
  vi.stubGlobal('Worker', class { onmessage = null; onerror = null; postMessage() {} terminate() {} })
}

export function wideViewportU2() {
  vi.stubGlobal('matchMedia', (media: string) => ({
    // The actual Sidebar pin breakpoint is 1024; this supplies a desktop environment.
    matches: media === '(min-width: 1024px)', media, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
    dispatchEvent: () => false,
  }))
}

export async function hashAppU2(path: string) {
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

export function cleanupNavigationU2() {
  cleanup()
  histories.splice(0).forEach((history) => history.destroy())
  clients.splice(0).forEach((client) => client.clear())
  useSidebarStore.setState({ isOpen: originalSidebar.isOpen, isPinned: originalSidebar.isPinned })
  if (originalSidebarStorage === null) localStorage.removeItem('omnipus-sidebar')
  else localStorage.setItem('omnipus-sidebar', originalSidebarStorage)
  window.history.replaceState(originalHistoryState, '', originalAddress)
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
}
