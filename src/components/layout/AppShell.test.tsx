// AppShell.test.tsx — app-state fetch-failure banner (Wave 1 frontend-findings-fix).
//
// AppShell (src/components/layout/AppShell.tsx) fetches `['app-state']` via
// useQuery and derives `devModeBypass` from it. Because `dev_mode_bypass` is a
// security-relevant flag, a transport failure on that fetch must NOT collapse
// to the same falsy state as a genuinely successful "bypass is off" response.
// The component renders a dedicated amber banner
// (data-testid="app-state-fetch-error-banner") whenever `isError` is true, so
// the failure is visible instead of silently looking identical to "confirmed
// off".
//
// Traces to: src/components/layout/AppShell.tsx L23-32 (appStateError derivation),
// L131-146 (rendered banner). No pre-existing BDD scenario in a wave spec for
// this component — inferred from the reviewer findings (pr-test-analyzer,
// code-simplifier, code-reviewer) that flagged the missing coverage during the
// Wave 1 7-reviewer gate; every sibling fix in the same wave (GodModeControl,
// PerformanceSection, chat.ts, session.ts) shipped a paired regression test.
// CLARIFY: no BDD Given/When/Then exists for this banner in any wave spec —
// tests below are written directly against the implemented behavior.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, NotificationList } from '@/lib/api/generated/openapi-types'

// AppShell composes a large subtree (Sidebar, NotificationPanel, ToastContainer,
// ToolApprovalModal, MediaLightbox, OmnipusRuntimeProvider) that each carry their
// own network/WS/router dependencies unrelated to the app-state fetch-error
// banner under test here. Stub them out as black boxes — consistent with how
// src/test/screens.test.tsx and Sidebar.m5.test.tsx scope heavy-provider trees
// in this repo — so this test stays focused on AppShell's own isError branch.
vi.mock('./Sidebar', () => ({ Sidebar: () => null }))
vi.mock('./NotificationPanel', () => ({ NotificationPanel: () => null }))
vi.mock('@/components/ui/toast-container', () => ({ ToastContainer: () => null }))
vi.mock('@/components/agents/ToolApprovalModal', () => ({ ToolApprovalModal: () => null }))
vi.mock('@/components/chat/MediaLightbox', () => ({ MediaLightbox: () => null }))
vi.mock('@/components/chat/OmnipusRuntimeProvider', () => ({
  OmnipusRuntimeProvider: ({ children }: { children?: React.ReactNode }) => children ?? null,
}))
vi.mock('@/hooks/useVersionCheck', () => ({ useVersionCheck: vi.fn() }))
vi.mock('@tanstack/react-router', () => ({ Outlet: () => null }))

// Mock only the fetch functions AppShell touches directly (fetchAppState,
// fetchNotifications) plus the two it prefetches on mount (fetchTasks,
// fetchAgents) so no real network call is attempted from jsdom.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
    fetchNotifications: vi.fn(),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
  }
})

import * as api from '@/lib/api'
import { AppShell } from './AppShell'

const APP_STATE_OK: AppState = { onboarding_complete: true, dev_mode_bypass: false }
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

describe('AppShell — app-state fetch-error banner', () => {
  it('renders normally without the error banner when the app-state fetch succeeds', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })
    // Give the query a tick to settle into success before asserting absence.
    await waitFor(() => {
      expect(screen.queryByTestId('app-state-fetch-error-banner')).not.toBeInTheDocument()
    })
    // Differentiation: a successful, bypass-off response must not also trip the
    // (unrelated) dev-mode-bypass banner — proves the component isn't just
    // rendering a static "everything's fine" shell regardless of the payload.
    expect(screen.queryByTestId('dev-mode-banner')).not.toBeInTheDocument()
  })

  it('renders the app-state-fetch-error-banner when the app-state fetch fails (isError)', async () => {
    vi.mocked(api.fetchAppState).mockRejectedValue(new Error('network error'))
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    await waitFor(() => {
      expect(screen.getByTestId('app-state-fetch-error-banner')).toBeInTheDocument()
    })
    const banner = screen.getByTestId('app-state-fetch-error-banner')
    expect(banner).toHaveAttribute('role', 'alert')
    expect(banner).toHaveTextContent(/could not fetch gateway state/i)
    expect(banner).toHaveTextContent(/security status/i)
    // On a fetch failure devModeBypass is derived from `undefined?.dev_mode_bypass === true`
    // → false, so the (unrelated) bypass-active banner must stay absent — the
    // fetch-error banner is the one and only signal for "state unknown".
    expect(screen.queryByTestId('dev-mode-banner')).not.toBeInTheDocument()
  })

  it('does not show the error banner while the app-state fetch is still loading', () => {
    // Never-resolving promise — the query stays in the loading state for the
    // lifetime of this test; isError must remain false throughout.
    vi.mocked(api.fetchAppState).mockReturnValue(new Promise<AppState>(() => {}))
    vi.mocked(api.fetchNotifications).mockReturnValue(new Promise<NotificationList>(() => {}))

    renderShell()

    expect(screen.queryByTestId('app-state-fetch-error-banner')).not.toBeInTheDocument()
  })
})
