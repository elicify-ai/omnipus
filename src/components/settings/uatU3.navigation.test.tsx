// U3-4: fix-round-u3.md, US-9 AS4 / WCAG 2.4.1 bypass blocks.
// Real AppShell, Chat, generated clients, stores, WsConnection and HASH router.
// Only HTTP/socket/browser edges come from the existing stage-1 test server.
// No Worker double: WsConnection parses inbound frames on the main thread when jsdom
// has no Worker, and a silent Worker double would swallow every frame (setup, not RED).
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createHashHistory } from '@tanstack/react-router'
import type { UserContextResponse } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { json } from '../../../tests/l1-room-ui/harness'
import {
  MAIN_PATH, Stage1Network, mountStage1App, resetStage1App, settledMainChat, stopStage1App,
} from '../../../tests/l1-room-ui/stage1-harness'

const originalAddress = window.location.href
const originalState: unknown = window.history.state
let history: ReturnType<typeof createHashHistory> | undefined
let net: Stage1Network

beforeEach(() => {
  resetStage1App()
  net = new Stage1Network()
  // The unchanged legacy chat URL mounts the real Profile first (Settings #1519
  // FR-003 / D-3 row10); supply only its HTTP edge, as AppShell.stage1.test.tsx does.
  const userContext: UserContextResponse = { content: '' }
  net.respond('GET', '/api/v1/user-context', () => json(userContext))
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  window.history.replaceState({}, '', `/#${MAIN_PATH}`)
  history = createHashHistory()
})
afterEach(async () => {
  await stopStage1App()
  history?.destroy()
  history = undefined
  window.history.replaceState(originalState, '', originalAddress)
  vi.restoreAllMocks()
})

function focusOwner() {
  const element = document.activeElement
  return element ? `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ''}` : 'none'
}

it('U3-4 — Enter on Chat Skip to content focuses main without changing the real hash-router location', async () => {
  const { router, socket } = await mountStage1App(MAIN_PATH, { history })
  await settledMainChat(socket)
  expect(await screen.findByTestId('chat-input'), 'reachability: the actual Chat is open before skip activation').toBeVisible()
  await waitFor(() => expect(queryClient.isFetching()).toBe(0))
  expect(net.unexpected, 'setup control: no unexpected HTTP request may masquerade as the skip-link RED').toEqual([])
  const main = screen.getByRole('main')
  expect(main).toHaveAttribute('id', 'main-content')
  // Native keyboard start simulates returning from browser chrome, not a
  // direct focus of the skip link or main. Chat/router handlers remain real.
  if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
  expect(document.activeElement).toBe(document.body)
  const address = window.location.href
  const before = { pathname: router.state.location.pathname, search: router.state.location.search, hash: router.state.location.hash }
  const user = userEvent.setup()
  await user.tab()
  expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveFocus()
  await user.keyboard('{Enter}')
  // Yield native anchor/hashchange delivery; this is not a timing threshold.
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
  const focused = document.activeElement
  expect.soft(focused !== null && main.contains(focused),
    `U3-4/WCAG 2.4.1: activation moves focus to the main content; focus is on ${focusOwner()}`).toBe(true)
  expect.soft(window.location.href, 'U3-4: skip activation must not replace the Chat route with #main-content').toBe(address)
  expect.soft({ pathname: router.state.location.pathname, search: router.state.location.search, hash: router.state.location.hash },
    'U3-4: the real hash router retains its complete Chat location').toEqual(before)
})
