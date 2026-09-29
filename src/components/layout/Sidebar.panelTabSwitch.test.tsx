// Sidebar.panelTabSwitch.test.tsx — regression test for the real UAT finding
// against feat/resizable-side-panels@952de5216 (commit 002784be0, "add
// shared full-screen back to chat exit"):
//
//   Open Library full screen via Expand (a real new tab). From the ORIGINAL
//   tab, click the Library entry point again (the sidebar button). OBSERVED
//   (real browser, Playwright, UAT session): nothing happens beyond a local
//   "active" toggle — no toast, no tab switch, no second tab either.
//
// EXPECTED (FR-009, side-panel-shell-spec.md SP-9/SP-18/SP-30): the entry
// point must detect the identity match and either (a) focus the existing
// tab directly via the in-memory handle registry, or (b) show the
// "already open — switch" toast (panelTabSwitch.ts::showPanelTabSwitch)
// when only the BroadcastChannel presence list (not a same-tab handle) has
// it. This test exercises scenario (b): it announces real tab-presence for
// the Library identity (the same real, unmocked mechanism
// src/lib/panelTabPresence.ts uses — a real BroadcastChannel, not a stub),
// through PanelTabPresenceBridge.tsx — the ONE place in this codebase that
// already wires `useUiStore` panel-open transitions to
// `resolveExistingPanelTab` / `showPanelTabSwitch` — then drives the sidebar
// Library button exactly as a user would.
//
// STATUS (2026-09-29, qa-lead RED dispatch): this test currently PASSES —
// scenario (b) is NOT the reproduction of the UAT finding. Reported to the
// squad lead as a finding, not silently shipped as the required RED.
// PanelTabPresenceBridge.tsx DOES correctly call showPanelTabSwitch for a
// presence-only match; the toast fires exactly as FR-009 requires.
//
// The UAT repro's exact wording ("via Expand", "the ORIGINAL tab") points at
// scenario (a) instead — the SAME-tab handle path, not the presence-only
// one: expandActivePanel (usePanelShell.ts) registers the popup's Window
// handle via registerPanelPopout -> registerPanelTabHandle
// (panelPopoutLifecycle.ts::registerPanelPopout, line ~229) under the exact
// identity key the re-click computes (panelIdentityKey({panelId:'library',
// workspaceId: undefined}) = "library:app" both times, traced by reading
// src/lib/panelTabPresence.ts::panelIdentityKey and
// PanelTabPresenceBridge.tsx::panelIdentity — same shape, same key). On the
// re-click, resolveExistingPanelTab finds that handle, calls
// switchToPanelTab -> handle.focus(), and — as long as focus() does not
// THROW — returns 'focused'; PanelTabPresenceBridge's handler for
// existing === 'focused' just closes the local panel and returns, with NO
// toast and no verification that the focus actually landed
// (PanelTabPresenceBridge.tsx lines 38-42). A real browser's cross-tab
// focus-stealing prevention can silently no-op a script's `window.focus()`
// call on another tab WITHOUT throwing — jsdom's Window.focus() always
// "succeeds" with no such policy, so this specific failure mode cannot be
// driven through jsdom (confirmed by reading node/jsdom's Window
// implementation behaviour; not independently re-verified beyond that
// reading — Inferred, not Verified by a failing run). That would exactly
// match "no toast, no tab switch" (focus() call made, silently ignored) and
// "no second tab" (a handle already existed, so no window.open ran).
// Whether the FIX should be "always show the toast/fallback after a
// same-tab focus attempt, in case the browser ignored it" is a design
// question this qa-lead task does not have the spec text in hand to settle
// as a test oracle without risking demanding behaviour the spec doesn't
// call for — flagged for the squad lead / architect rather than asserted
// here as fact.
//
// Oracle: FR-009 / side-panel-shell-spec.md SP-9, SP-18, SP-30 — an entry
// point that opens a panel identity already present in the tab-presence
// list must show the switch affordance, never a silent local no-op. The
// toast copy asserted below ("already open ... switch") is
// panelTabSwitch.ts::showPanelTabSwitch's own literal message text, read
// from that file, not observed by running this test.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import {
  announcePanelTabPresence,
  getPanelTabPresence,
  type PanelIdentity,
} from '@/lib/panelTabPresence'
import { PanelTabPresenceBridge } from '@/components/panel-shell/PanelTabPresenceBridge'

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: () => ({
    matches: true,
    media: '',
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
})

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, onClick, className, ...rest }: {
    children: React.ReactNode
    to: string
    onClick?: () => void
    className?: string
  } & Record<string, unknown>) => (
    <a href={to} onClick={onClick} {...rest} data-fixture-class={className ? 'present' : undefined}>{children}</a>
  ),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    logout: vi.fn().mockResolvedValue(undefined),
    fetchSessions: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionPage: vi.fn().mockResolvedValue({ sessions: [] }),
    fetchAppState: vi.fn().mockResolvedValue({
      onboarding_complete: true,
      dev_mode_bypass: false,
      identity: { mode: 'platform', edition: 'hosted', signed_in: true },
    }),
    fetchGodMode: vi.fn().mockResolvedValue({
      enabled: false,
      available: true,
      supported: true,
      persisted: false,
    }),
    workspacesQueryKeys: { list: (params?: unknown) => ['workspaces', params] },
  }
})

vi.mock('@/store/workspacesStore', () => {
  const state = { activeWorkspaceId: 'ws-1' as string | null, setActiveWorkspaceId: vi.fn() }
  return {
    useWorkspacesStore: (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state),
  }
})

vi.mock('@/store/session', () => {
  const state = { activeSessionId: null as string | null, startNewSession: vi.fn() }
  const useSessionStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useSessionStore.getState = () => state
  return { useSessionStore }
})

vi.mock('@/components/chat/useSelectSession', () => ({ useSelectSession: () => vi.fn() }))

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'dana', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

vi.mock('@/store/notifications', () => ({
  useNotificationsStore: (selector?: (s: { unreadCount: number }) => unknown) => {
    const state = { unreadCount: 0 }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/components/ui/dropdown-menu', async () => {
  const { Button } = await vi.importActual<typeof import('@/components/ui/button')>('@/components/ui/button')
  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuItem: ({ children, onSelect }: { children: React.ReactNode; onSelect?: () => void }) => (
      <Button variant="ghost" onClick={onSelect}>{children}</Button>
    ),
    DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuSeparator: () => <hr />,
  }
})

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({ NewWorkspaceSlideOver: () => null }))

vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside {...rest} data-fixture-class={className ? 'present' : undefined}>{children}</aside>
    ),
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div onClick={onClick} {...rest} data-fixture-class={className ? 'present' : undefined}>{children}</div>
    ),
  },
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { Sidebar } from './Sidebar'

function renderSidebarWithPresenceBridge() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <PanelTabPresenceBridge />
      <Sidebar />
    </QueryClientProvider>,
  )
}

let announcement: ReturnType<typeof announcePanelTabPresence> | null = null

beforeEach(() => {
  act(() => {
    useSidebarStore.setState({ isOpen: true, isPinned: true })
    useUiStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false, toasts: [] })
  })
})

afterEach(() => {
  cleanup()
  announcement?.stop()
  announcement = null
  act(() => {
    useUiStore.getState().closePanel()
    useUiStore.setState({ toasts: [] })
  })
})

describe('Sidebar LIBRARY entry point — already-open-elsewhere switch (FR-009, §12)', () => {
  it('RED — clicking Library while it is already open full-screen in another tab must show the switch toast, not a silent no-op', async () => {
    // The identity the full-screen route computes for a workspace-scoped,
    // no-workspace-context Library open — panelTabPresence.ts's own
    // panelIdentityFromContext('library', {}) shape, matching
    // PanelTabPresenceBridge.tsx's panelIdentity() for the same context.
    const identity = { panelId: 'library', workspaceId: undefined } satisfies PanelIdentity

    renderSidebarWithPresenceBridge()

    // Simulate the OTHER (full-screen) tab announcing presence for that
    // identity, through the real BroadcastChannel-backed mechanism (the same
    // one src/lib/panelTabPresence.lifecycle.test.ts exercises) — not a
    // mock or a stub.
    announcement = announcePanelTabPresence(identity)
    await waitFor(() => expect(getPanelTabPresence()).toContainEqual(identity))

    // Drive the ORIGINAL tab's entry point exactly as a user would.
    fireEvent.click(screen.getByTestId('sidebar-library-button'))

    // EXPECTED (FR-009 / SP-9/SP-18/SP-30): the entry point must recognise
    // the identity is already open elsewhere and offer the switch — not
    // just flip a local "active" flag with no visible effect.
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts
      expect(toasts.some((toast) => /already open.*switch/i.test(toast.message))).toBe(true)
    })
  })
})
