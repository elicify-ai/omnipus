import React from 'react'
// AppShell.quiet-disconnect.test.tsx — the AppShell connection-error banner
// must NOT render during a quiet abnormal WebSocket disconnect, through the
// reconnect schedule, or after give-up (#823 spec items 1 and 3).
//
// Spec source: issue #823 (elicify-ai/omnipus), founder comment 2026-09-23,
// "Gap found in a real browser: the old technical banner is still on the
// error path":
//   "useConnectionStore.getState().connectionError stays null … The AppShell
//    error banner (src/components/layout/AppShell.tsx) is not rendered."
//   "No visible text during a drop contains 'gateway', 'Disconnected from
//    gateway', or a close code like '1006'."
//
// AppShell itself (src/components/layout/AppShell.tsx) renders the banner
// purely off `useConnectionStore((s) => s.connectionError)` — that half is
// already covered by the existing "AppShell — banner announcements" describe
// block in AppShell.test.tsx (which sets connectionError directly and checks
// the banner appears). What is NOT covered anywhere is the other half: that
// a real WsConnection (src/lib/ws.ts), wired to the real connection store
// exactly the way production's WsLifecycle wires it
// (src/components/chat/OmnipusRuntimeProvider.tsx), never SETS
// connectionError in the first place for a recoverable abnormal close. This
// file closes that gap by wiring the real WsConnection + real
// useConnectionStore (same pattern as src/store/connection.quiet-disconnect.test.ts)
// alongside a REAL rendered <AppShell/>, and driving an actual close event.
//
// OmnipusRuntimeProvider itself is mocked out (same as AppShell.test.tsx) —
// it is "too heavy for unit tests" (src/test/screens.test.tsx's own words)
// because it opens a real WebSocket via a large AssistantUI runtime tree
// unrelated to the banner under test. The WS wiring under test here is
// reconstructed explicitly instead, matching OmnipusRuntimeProvider's own
// WsLifecycle callback shape field-for-field.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, NotificationList } from '@/lib/api/generated/openapi-types'

vi.mock('./Sidebar', () => ({ Sidebar: () => null }))
vi.mock('./NotificationPanel', () => ({ NotificationPanel: () => null }))
vi.mock('@/components/ui/toast-container', () => ({ ToastContainer: () => null }))
vi.mock('@/components/agents/ToolApprovalModal', () => ({ ToolApprovalModal: () => null }))
vi.mock('@/components/chat/MediaLightbox', () => ({ MediaLightbox: () => null }))
vi.mock('@/components/chat/OmnipusRuntimeProvider', () => ({
  OmnipusRuntimeProvider: ({ children }: { children?: React.ReactNode }) => children ?? null,
}))
vi.mock('@/hooks/useVersionCheck', () => ({ useVersionCheck: vi.fn() }))
vi.mock('@tanstack/react-router', () => ({ Outlet: () => null, useNavigate: () => vi.fn(), useLocation: () => ({ pathname: '/' }), Link: ({ children, to, onClick, className }: { children: React.ReactNode; to: string; onClick?: () => void; className?: string }) => React.createElement('a', { href: to, onClick, className }, children) }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
    fetchNotifications: vi.fn(),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

import * as api from '@/lib/api'
import { AppShell } from './AppShell'
import { useConnectionStore } from '@/store/connection'
import { WsConnection } from '@/lib/ws'

const DEFAULT_IDENTITY: AppState['identity'] = {
  mode: 'local',
  edition: 'core',
  signed_in: false,
  blocked_reason: 'signed_out',
}
const APP_STATE_OK: AppState = { onboarding_complete: true, dev_mode_bypass: false, identity: DEFAULT_IDENTITY }
const NOTIFICATIONS_EMPTY: NotificationList = { notifications: [], unread_count: 0 }

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderShell() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AppShell />
    </QueryClientProvider>,
  )
}

// ── Mock WebSocket ────────────────────────────────────────────────────────

let lastWsInstance: {
  onopen: (() => void) | null
  onmessage: ((ev: { data: string }) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
  readyState: number
}

const MockWebSocket = vi.fn(function () {
  const instance = {
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
    send: vi.fn(),
    close: vi.fn(),
    readyState: 1, // OPEN
  }
  lastWsInstance = instance
  return instance
}) as unknown as typeof WebSocket & {
  OPEN: number
  CLOSED: number
  mockClear: () => void
  mock: { calls: unknown[][] }
}

MockWebSocket.OPEN = 1
MockWebSocket.CLOSED = 3

/** Wires a real WsConnection to the real store exactly like WsLifecycle does. */
function wireProductionCallbacks(): WsConnection {
  return new WsConnection({
    onFrame: () => {},
    onConnected: () => {
      useConnectionStore.getState().setConnected(true)
      useConnectionStore.getState().setConnectionError(null)
    },
    onDisconnected: () => {
      useConnectionStore.getState().recordDisconnect(null)
    },
    onError: (msg) => useConnectionStore.getState().setConnectionError(msg),
    onReconnectStateChange: (phase, attempt) => useConnectionStore.getState().setReconnectState(phase, attempt),
  })
}

beforeEach(() => {
  MockWebSocket.mockClear()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  act(() => {
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
      reconnectPhase: null,
      reconnectAttempt: 0,
      disconnectedAt: null,
      reconnectedAt: null,
      lastDisconnectDurationMs: null,
      lastDisconnectWasTerminal: false,
      disconnectedAssistantMessageId: null,
    })
  })
})

describe('AppShell — no alert banner or gateway/close-code text during a quiet abnormal disconnect (#823 items 1 & 3)', () => {
  it('renders no alert banner and no gateway/close-code text through an abnormal close, reconnect schedule, and give-up', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()
    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })
    // Baseline: neither the app-state banner nor the connection banner is up.
    await waitFor(() => {
      expect(screen.queryByTestId('app-state-fetch-error-banner')).not.toBeInTheDocument()
    })
    expect(screen.queryAllByRole('alert')).toHaveLength(0)

    vi.useFakeTimers()
    vi.stubGlobal('WebSocket', MockWebSocket)

    const conn = wireProductionCallbacks()
    act(() => conn.connect())
    act(() => lastWsInstance.onopen?.())

    expect(screen.queryAllByRole('alert')).toHaveLength(0)

    // The abnormal close — the moment of the drop, before any reconnect
    // timer has fired.
    act(() => lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' }))
    expect(screen.queryAllByRole('alert')).toHaveLength(0)
    expect(document.body.textContent ?? '').not.toMatch(/gateway/i)
    expect(document.body.textContent ?? '').not.toMatch(/\b1006\b/)

    // Drive the reconnect schedule until the store reports gave_up.
    const SAFETY_CAP = 60
    let i = 0
    while (useConnectionStore.getState().reconnectPhase !== 'gave_up' && i < SAFETY_CAP) {
      act(() => lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' }))
      expect(screen.queryAllByRole('alert'), `iteration ${i}`).toHaveLength(0)
      expect(document.body.textContent ?? '', `iteration ${i}`).not.toMatch(/gateway/i)
      act(() => vi.advanceTimersByTime(65_000))
      i++
    }

    expect(useConnectionStore.getState().reconnectPhase, 'the schedule must actually reach gave_up within the safety cap').toBe('gave_up')

    // After give-up: still no alarming alert banner, still no banned text,
    // and the store's connectionError (what AppShell renders off) is null.
    expect(screen.queryAllByRole('alert')).toHaveLength(0)
    expect(document.body.textContent ?? '').not.toMatch(/gateway/i)
    expect(document.body.textContent ?? '').not.toMatch(/\b1006\b/)
    expect(useConnectionStore.getState().connectionError).toBeNull()

    conn.disconnect()
    vi.useRealTimers()
  })
})
