// -browser-live.crossAccount.test.tsx — side-panel-shell-spec.md §12 #17
// (MIN-009: the owner's full-page /browser-live link reaching ANOTHER
// account — a copied or leaked URL, not a share: SP-37 rules the Browser
// panel is not shareable, so the panel offers no share or copy-link
// affordance and the full-page expand is the owner's own navigation.
// Re-scoped to the full-page route per SP-28).
//
// INTEGRATION pack, mounted through the REAL routeTree + a real
// memory-history router (same technique as the chat deep-link pack), with
// ONLY the process edge faked: the `WebSocket` global stands in for the
// gateway so BrowserLiveWsConnection stays REAL — its close-code mapping is
// the denial surface under test.
//
// The REAL denial shape, read from the code (not invented): the gateway
// authorizes the live-browser WS handshake from the `omnipus-session` cookie
// (ADR-044) and rejects a non-owner with WS close code 1008 (policy
// violation); src/lib/browserLiveWs.ts::onclose maps 1008 →
// onError('Authentication failed for the live browser view. Reload and try
// again.') and stops (no reconnect — the cookie has not changed). There is
// no REST 403/404 on this path: the session cookie rides the WS handshake,
// so the denial is observable only as the WS close code.
//
// Oracle: §8.2's MIN-009 disposition + §12 #17 — "a second account following
// the owner's /browser-live link sees the panel's visible denial surface,
// never the other user's stream."
//
// STATUS: characterisation — the 1008 mapping already exists on this
// pre-GREEN tree, so the pack is EXPECTED GREEN here; failability is proven
// by one production mutation (remove the close-1008 branch), reverted.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, screen } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'

vi.mock('framer-motion', () => ({
  motion: new Proxy(
    {},
    {
      get:
        (_: object, prop: string) =>
        React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: React.Ref<unknown>) =>
          React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)),
    },
  ),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
  }
})

import { routeTree } from '@/routeTree.gen'

// The gateway stand-in at the process edge. It implements only what
// BrowserLiveWsConnection uses: constructor, send (records frames), close,
// and the four event handlers. `serverOpen()` fires the handler chain the
// real gateway would (onopen → the connection sends browser_attach);
// `serverDenyWith1008()` fires onclose with the gateway's per-user
// authorization rejection code — read from pkg/gateway + browserLiveWs.ts,
// not invented.
class FakeDeniedWebSocket {
  static last: FakeDeniedWebSocket | null = null
  // The WHATWG readyState constants — browserLiveWs's _rawSend guards
  // `this.ws?.readyState === WebSocket.OPEN`, and after vi.stubGlobal the
  // identifier `WebSocket` IS this class, so the constants must exist here.
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  onopen: (() => void) | null = null
  onclose: ((ev: { code: number }) => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  sent: string[] = []
  readyState = 1
  constructor(public url: string) {
    FakeDeniedWebSocket.last = this
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {
    this.readyState = 3
  }
  serverOpen() {
    this.onopen?.()
  }
  serverDenyWith1008() {
    this.readyState = 3
    const ev = new Event('close') as Event & { code: number }
    Object.defineProperty(ev, 'code', { value: 1008 })
    this.onclose?.(ev)
  }
}

const originalMatchMedia = window.matchMedia
beforeEach(() => {
  localStorage.setItem('omnipus_auth_username', 'second-account')
  FakeDeniedWebSocket.last = null
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
  vi.stubGlobal('WebSocket', FakeDeniedWebSocket)
})

afterEach(() => {
  localStorage.removeItem('omnipus_auth_username')
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: originalMatchMedia,
  })
  vi.unstubAllGlobals()
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

const waitFor5s = { timeout: 5000 } as const

/** Let any post-denial macrotask settle before a "did NOT happen" claim. */
async function flushViaTimers() {
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
}

describe('§12 #17 — cross-account denial on the full-page /browser-live route (MIN-009, SP-28 re-scope)', () => {
  function mountOwnerLink() {
    const client = makeClient()
    // The owner's full-page link — reaching a second account as a copied or
    // leaked URL (SP-37: the Browser panel is NOT shareable; there is no
    // share/copy-link affordance to hand it over) (this test's
    // session cookie belongs to whoever the gateway authenticates it as —
    // the fake gateway denies the attach).
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({
        initialEntries: ['/browser-live?session=own-1&agent=agent-1'],
      }),
    })
    vi.stubGlobal('WebSocket', FakeDeniedWebSocket)
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    return { client, router }
  }

  it('a second account opening the owner\'s link sees the visible denial surface, never the stream', async () => {
    const { client, router } = mountOwnerLink()
    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })

    // The route mounted past its own "Missing session or agent" refusal and
    // the view connected at the WS edge — the handshake the gateway would
    // authorize from the omnipus-session cookie.
    await waitFor(() => expect(FakeDeniedWebSocket.last).not.toBeNull(), waitFor5s)
    const ws = FakeDeniedWebSocket.last!
    // The gateway accepts the TCP/handshake (onopen), and the connection then
    // sends its browser_attach frame — the handshake the gateway's per-user
    // authorization evaluates before granting the stream.
    act(() => { ws.serverOpen() })
    const attach = ws.sent.find((f) => f.includes('"type":"browser_attach"'))
    expect(attach).toBeDefined()
    expect(attach).toContain('own-1')

    // The gateway denies: WS close 1008 (policy violation). The REAL
    // BrowserLiveWsConnection must map this to the denial surface and stop.
    act(() => { ws.serverDenyWith1008() })

    // 1. The visible denial surface: the REAL 1008 mapping's exact copy.
    await waitFor(() => screen.getByText('Authentication failed for the live browser view. Reload and try again.'), waitFor5s)
    // 2. No stream/video element ever mounts — browser-live-video only mounts
    //    once a stream attaches.
    expect(screen.queryByTestId('browser-live-video')).toBeNull()
    expect(screen.queryByTestId('browser-live-frame')).toBeNull()
    // 3. The denial is terminal — the view offers its recovery affordance,
    //    but no reconnect/attach frame is ever re-sent after the 1008.
    expect(screen.getByTestId('browser-live-retry')).toBeInTheDocument()
    await flushViaTimers()
    expect(ws.sent.length).toBe(1)  // the original attach only — never re-sent
    client.clear()
  })
})
