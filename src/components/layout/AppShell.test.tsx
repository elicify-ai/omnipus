import React from 'react'
// AppShell.test.tsx — app-state fetch-failure banner (Wave 1 frontend-findings-fix).
//
// AppShell (src/components/layout/AppShell.tsx) fetches `['app-state']` via
// useQuery and derives `devModeBypass` from it. Because `dev_mode_bypass` is a
// security-relevant flag, a transport failure on that fetch must NOT collapse
// to the same falsy state as a genuinely successful "bypass is off" response.
// The component renders a dedicated warning banner
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

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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
// The Link double folds `search` into the href (same pattern as
// Sidebar.test.tsx) so the God Mode corner-dot test can assert the full
// /settings?focus=god-mode&tab=gateway target.
vi.mock('@tanstack/react-router', () => ({
  Outlet: () => null,
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children, to, search, onClick, className, ...rest }: {
    children: React.ReactNode
    to: string
    search?: Record<string, string>
    onClick?: () => void
    className?: string
  } & Record<string, unknown>) => React.createElement(
    'a',
    {
      href: search ? `${to}?${new URLSearchParams(search).toString()}` : to,
      onClick,
      className,
      ...rest,
    },
    children,
  ),
}))

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
    // CrossWorkspaceApprovalBanner (rendered for real below, not stubbed —
    // the coexistence test needs it to actually mount) resolves workspace
    // names via this call.
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    // The corner dot (GodModeIndicators.tsx, mounted by AppShell since the
    // banner removal) queries god-mode via useGodModeOn; the banner-removal
    // describe below also answers the endpoint with "on"/"error" to prove
    // AppShell renders none of the banner's old surfaces for it.
    fetchGodMode: vi.fn().mockResolvedValue({ enabled: false, available: false, supported: true, persisted: false }),
  }
})

import * as api from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { AppShell } from './AppShell'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSidebarStore } from '@/store/sidebar'

// ADR-0010 / login-and-onboarding-spec.md §2.2 — `identity` is a required
// field on AppState. This fixture describes an unauthenticated core build;
// these tests are about the notification/connection banners, not identity,
// so it stays fixed rather than parameterized.
const DEFAULT_IDENTITY: AppState['identity'] = {
  mode: 'local',
  edition: 'core',
  signed_in: false,
  blocked_reason: 'signed_out',
}

const APP_STATE_OK: AppState = {
  onboarding_complete: true,
  dev_mode_bypass: false,
  identity: DEFAULT_IDENTITY,
}
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

// ── Skip-to-content link (WCAG 2.4.1 Bypass Blocks) ─────────────────────────
//
// The skip link must be document-wide the FIRST Tab stop (tabIndex={1}) so
// the chat screen's own positive-tabIndex composer ring (which starts at 2,
// see the map in src/components/chat/ChatControls.tsx) can never shadow it —
// a link with no explicit tabIndex would otherwise lose the race to those
// positive-tabIndex composer controls and become functionally unreachable by
// keyboard on the chat screen. See src/components/layout/AppShell.tsx L133-142.
describe('AppShell — skip link', () => {
  it('is the shell\'s first element child, targets #main-content, and carries tabIndex=1', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    const link = await waitFor(() => screen.getByRole('link', { name: /skip to content/i }))
    expect(link).toHaveAttribute('href', '#main-content')
    expect(link.tabIndex).toBe(1)

    // First focusable element in the shell — Sidebar is mocked to null in
    // this file, so the skip link is literally the shell's first child.
    const shell = document.querySelector('[data-app-shell]')
    expect(shell?.firstElementChild).toBe(link)
  })

  it('the #main-content target exists and is itself unreachable by Tab (tabIndex=-1, programmatic-focus-only)', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })
    const main = document.getElementById('main-content')
    expect(main).not.toBeNull()
    expect(main?.tagName).toBe('MAIN')
    expect(main?.tabIndex).toBe(-1)
  })
})

// ── visualViewport ALWAYS-ON tracking (regression guard) ────────────────────
//
// This hook (AppShell.tsx, the `computeAppMetrics`-driven effect) has been
// rewritten repeatedly chasing iOS keyboard/scroll regressions — see
// docs/internal/architecture/ios-scroll-stability.md. The canonical mechanism
// as of the 2026-07-20 fix is ALWAYS-ON: `--app-top`/`--app-vh` are published
// from `visualViewport` unconditionally, with NO dependency on
// `document.activeElement`. A prior "fix" (commit dec7713b) gated publishing
// on an editable having focus — that REMOVED the vars (shell snaps to
// top:0/100dvh) the instant focus left the composer for anything
// non-editable, reproducing the exact bug this test guards against: "the
// header row jumps out of the viewable area, and tapping any non-editable
// element makes it jump to the top again." Do not reintroduce that gate; see
// also the pure-function coverage in `AppShell.viewport.test.ts`
// (`computeAppMetrics`).
describe('AppShell — visualViewport always-on tracking', () => {
  const originalMatchMedia = window.matchMedia
  const originalVisualViewport = (window as unknown as { visualViewport?: unknown }).visualViewport
  const originalInnerHeight = window.innerHeight

  function stubVisualViewport(initial: { height: number; offsetTop: number }) {
    const listeners: Record<string, Array<() => void>> = { resize: [], scroll: [] }
    const vv = {
      height: initial.height,
      offsetTop: initial.offsetTop,
      addEventListener: vi.fn((type: string, cb: () => void) => {
        listeners[type] = listeners[type] ?? []
        listeners[type].push(cb)
      }),
      removeEventListener: vi.fn((type: string, cb: () => void) => {
        listeners[type] = (listeners[type] ?? []).filter((l) => l !== cb)
      }),
      fireResize: () => listeners.resize?.forEach((cb) => cb()),
    }
    Object.defineProperty(window, 'visualViewport', {
      configurable: true,
      writable: true,
      value: vv,
    })
    return vv
  }

  function stubCoarsePointerMatchMedia() {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        // Only the '(pointer: coarse)' query AppShell actually checks needs
        // to resolve true here — everything else defaults false.
        matches: query === '(pointer: coarse)',
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }),
    })
  }

  function stubInnerHeight(height: number) {
    Object.defineProperty(window, 'innerHeight', { configurable: true, writable: true, value: height })
  }

  beforeEach(() => {
    stubInnerHeight(800)
    stubCoarsePointerMatchMedia()
    // Make requestAnimationFrame synchronous so the effect's rAF-batched
    // metric writes are observable immediately after dispatching events,
    // without depending on jsdom's own (unreliable) rAF timing.
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
      cb(0)
      return 0
    })
    vi.stubGlobal('cancelAnimationFrame', () => {})
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    Object.defineProperty(window, 'matchMedia', { configurable: true, writable: true, value: originalMatchMedia })
    Object.defineProperty(window, 'innerHeight', { configurable: true, writable: true, value: originalInnerHeight })
    if (originalVisualViewport === undefined) {
      delete (window as unknown as { visualViewport?: unknown }).visualViewport
    } else {
      Object.defineProperty(window, 'visualViewport', { configurable: true, writable: true, value: originalVisualViewport })
    }
    document.documentElement.style.removeProperty('--app-vh')
    document.documentElement.style.removeProperty('--app-top')
  })

  it('publishes --app-vh/--app-top from visualViewport at mount, with no editable ever focused', async () => {
    // Keyboard-open-shaped state (height well below innerHeight) and a
    // panned offsetTop — set with NO focus/focusin dispatched at all. Under
    // the old focus gate this would be permanently absent; always-on
    // tracking must publish it from the very first read.
    stubVisualViewport({ height: 480, offsetTop: 130 })
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()
    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })

    expect(document.documentElement.style.getPropertyValue('--app-top')).toBe('130px')
    expect(document.documentElement.style.getPropertyValue('--app-vh')).toBe('480px')
  })

  it('does NOT remove --app-top when focus leaves an editable for a non-editable element — the iPad header-jump regression', async () => {
    const vv = stubVisualViewport({ height: 480, offsetTop: 130 })
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()
    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })
    expect(document.documentElement.style.getPropertyValue('--app-top')).toBe('130px')

    // Focus the composer (keyboard opens) — offsetTop stays panned.
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    document.dispatchEvent(new Event('focusin', { bubbles: true }))
    expect(document.documentElement.style.getPropertyValue('--app-top')).toBe('130px')

    // Tap a non-editable element: blur the input, focus moves to a plain
    // div. This is the exact reproduction — a focus-gated implementation
    // removes the vars here, snapping the shell to top:0 while the visual
    // viewport is still panned.
    const nonEditable = document.createElement('div')
    document.body.appendChild(nonEditable)
    input.blur()
    nonEditable.setAttribute('tabindex', '-1')
    nonEditable.focus()
    document.dispatchEvent(new Event('focusout', { bubbles: true }))

    expect(document.documentElement.style.getPropertyValue('--app-top')).toBe('130px')
    expect(document.documentElement.style.getPropertyValue('--app-vh')).toBe('480px')

    document.body.removeChild(input)
    document.body.removeChild(nonEditable)
    void vv
  })

  it('removes --app-vh once the keyboard is deterministically closed (height ≈ innerHeight), independent of focus', async () => {
    const vv = stubVisualViewport({ height: 480, offsetTop: 130 })
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()
    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })
    expect(document.documentElement.style.getPropertyValue('--app-vh')).toBe('480px')

    // Keyboard closes: vv settles back to full height/offset. Fired as a
    // real `resize` event (no focus event involved at all).
    vv.height = 800
    vv.offsetTop = 0
    vv.fireResize()

    expect(document.documentElement.style.getPropertyValue('--app-vh')).toBe('')
    expect(document.documentElement.style.getPropertyValue('--app-top')).toBe('0px')
  })

  it('schedules a trailing re-read on focusout to catch a dropped final resize event', async () => {
    const vv = stubVisualViewport({ height: 480, offsetTop: 130 })
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()
    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })
    expect(document.documentElement.style.getPropertyValue('--app-vh')).toBe('480px')

    // Switch to fake timers only now, so the component's ~250ms trailing
    // setTimeout can be advanced deterministically without also faking out
    // the render's own async plumbing above.
    vi.useFakeTimers()

    // Keyboard closes but iOS drops the final `resize` event — simulate by
    // mutating vv WITHOUT firing resize, then blurring (focusout is the
    // trailing-read trigger). Also force the synchronous re-read inside
    // handleFocusOut to be a no-op by pre-advancing state only after it —
    // i.e. mutate AFTER the synchronous read so only the trailing read (not
    // the immediate one) can observe the settled values.
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    input.blur()
    document.dispatchEvent(new Event('focusout', { bubbles: true }))
    // The synchronous re-read inside handleFocusOut has now run with the
    // STILL-OPEN vv state (480/130) — vars remain reflecting keyboard-open.
    expect(document.documentElement.style.getPropertyValue('--app-vh')).toBe('480px')

    // Now mutate vv to the settled/closed state — this is what the dropped
    // resize event would have applied, and only the trailing timer path
    // will pick it up.
    vv.height = 800
    vv.offsetTop = 0
    vi.advanceTimersByTime(300)

    expect(document.documentElement.style.getPropertyValue('--app-vh')).toBe('')
    expect(document.documentElement.style.getPropertyValue('--app-top')).toBe('0px')

    document.body.removeChild(input)
    vi.useRealTimers()
  })
})

// ── Banner announcements (FW-3, item 2) ──────────────────────────────────────
//
// The connectionError and devModeBypass banners were visible but silent to
// screen readers (no role="alert") — a sighted user sees the red bar
// immediately; a screen reader user got nothing unless they happened to be
// focused inside it. Both are security/connectivity-relevant and must
// announce. Traces to: src/components/layout/AppShell.tsx L177-201.
describe('AppShell — banner announcements', () => {
  afterEach(() => {
    useConnectionStore.setState({ connectionError: null })
  })

  it('connectionError banner carries role="alert"', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    useConnectionStore.setState({ connectionError: 'Lost connection to gateway' })

    renderShell()

    const banner = await waitFor(() => screen.getByText('Lost connection to gateway'))
    expect(banner.closest('[role="alert"]')).not.toBeNull()
  })

  it('devModeBypass banner carries role="alert"', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue({
      onboarding_complete: true,
      dev_mode_bypass: true,
      identity: DEFAULT_IDENTITY,
    })
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    const banner = await waitFor(() => screen.getByTestId('dev-mode-banner'))
    expect(banner).toHaveAttribute('role', 'alert')
  })
})

// ── #823: no global banner while a transport drop is in progress ─────────────
//
// The old defect (issue #823 follow-up comment): a WS drop set
// connectionError immediately, so AppShell rendered "Disconnected from
// gateway — code 1006 connection lost. Reconnecting…" the instant the
// network cut, before the 15s quiet window even started. After the fix,
// src/lib/ws.ts never calls callbacks.onError for an abnormal close / retry /
// give-up — so connectionError stays null through the entire drop, and
// AppShell renders no connection banner at all. The only surface for a drop
// is the phase-1 quiet UI (ConnectionStatus.tsx), owned elsewhere, not
// AppShell.
describe('AppShell — no connection banner while a transport drop is in progress (#823)', () => {
  afterEach(() => {
    useConnectionStore.setState({
      connectionError: null,
      isConnected: true,
      reconnectPhase: null,
      reconnectAttempt: 0,
    })
  })

  it('renders no role="alert" connection banner while disconnected and reconnecting', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    // Post-fix ws.ts state during a live drop: isConnected false, actively
    // retrying, but connectionError was never set.
    useConnectionStore.setState({
      connectionError: null,
      isConnected: false,
      reconnectPhase: 'reconnecting',
      reconnectAttempt: 1,
    })

    renderShell()

    await waitFor(() => expect(api.fetchAppState).toHaveBeenCalled())
    expect(screen.queryByText(/disconnected from gateway/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/code 1006/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/reconnecting/i)).not.toBeInTheDocument()
  })

  it('renders no role="alert" connection banner after the reconnect give-up phase', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    useConnectionStore.setState({
      connectionError: null,
      isConnected: false,
      reconnectPhase: 'gave_up',
      reconnectAttempt: 20,
    })

    renderShell()

    await waitFor(() => expect(api.fetchAppState).toHaveBeenCalled())
    expect(screen.queryByText(/connection lost after/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/click "reconnect now"/i)).not.toBeInTheDocument()
  })
})

// ── <sm docked-browser takeover inerts the collapsed chat region (FW-3, item 6) ──
//
// When BrowserLivePanel is docked open on a phone viewport (<640px), the flex
// row gives it the width and the chat region collapses to zero — but its
// controls stayed in the DOM (and thus the Tab order), so a keyboard user
// could Tab into invisible stops. `inert` on the main-content wrapper closes
// that gap; it's gated on BOTH conditions (panel open AND phone viewport) so
// desktop's side-by-side split (panel open, chat still visible) stays fully
// interactive. Traces to: src/components/layout/AppShell.tsx L160-173.
describe('AppShell — <sm docked-browser takeover inerts collapsed chat controls', () => {
  const originalMatchMedia = window.matchMedia

  function stubMatchMedia(matchesPhoneQuery: boolean) {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        matches: query === '(max-width: 639px)' ? matchesPhoneQuery : false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }),
    })
  }

  afterEach(() => {
    useUiStore.setState({ browserPanel: null })
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: originalMatchMedia,
    })
  })

  it('is inert when the browser panel is open AND the viewport is a phone (<640px)', async () => {
    stubMatchMedia(true)
    useUiStore.setState({ browserPanel: { sessionId: 's1', agentId: 'a1' } })
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    const main = await waitFor(() => screen.getByTestId('app-main-content'))
    // jsdom's `.inert` IDL property is unreliable across versions — assert on
    // the actual DOM attribute React writes, which is what a real browser's
    // focus/Tab-order machinery keys off regardless.
    expect(main.hasAttribute('inert')).toBe(true)
  })

  it('is NOT inert when the browser panel is open but the viewport is desktop-width', async () => {
    stubMatchMedia(false)
    useUiStore.setState({ browserPanel: { sessionId: 's1', agentId: 'a1' } })
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    const main = await waitFor(() => screen.getByTestId('app-main-content'))
    expect(main.hasAttribute('inert')).toBe(false)
  })

  it('is NOT inert on a phone viewport when the browser panel is closed — differentiation', async () => {
    stubMatchMedia(true)
    useUiStore.setState({ browserPanel: null })
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    const main = await waitFor(() => screen.getByTestId('app-main-content'))
    expect(main.hasAttribute('inert')).toBe(false)
  })
})

// ── Cross-workspace approval banner coexists with the connection-error
// banner (founder decision 2026-09-14) ─────────────────────────────────────
//
// CrossWorkspaceApprovalBanner (src/components/layout/CrossWorkspaceApprovalBanner.tsx)
// is mounted in the same stacked-banner column as connectionError/
// devModeBypass/appStateError, right above <main>. Both are plain block-level
// children of a flex column (never `fixed`/`absolute`), so when more than one
// is visible at once they simply stack — neither can visually cover or
// replace the other, and <main> (flex-1) still gets whatever height remains.
// This proves that stacking holds for real, not just by code inspection.
describe('AppShell — cross-workspace approval banner coexists with other banners', () => {
  afterEach(() => {
    useConnectionStore.setState({ connectionError: null })
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })

  it('renders both the connection-error banner and the cross-workspace approval banner at once, neither hiding the other', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    vi.mocked(api.fetchWorkspaces).mockResolvedValue([
      {
        revision: '0'.repeat(64),
        id: 'ws-other',
        name: 'UAT-T2',
        status: 'active',
        pinned: false,
        pin_order: 0,
        task_count: 0,
        created_at: '2026-09-14T00:00:00Z',
        updated_at: '2026-09-14T00:00:00Z',
      },
    ])
    useConnectionStore.setState({ connectionError: 'Lost connection to gateway' })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-active' })
    useToolApprovalStore.getState().enqueue({
      type: 'tool_approval_required',
      approval_id: 'appr-cross',
      tool_call_id: 'call-cross',
      tool_name: 'write_file',
      args: {},
      agent_id: 'agent-x',
      session_id: 'sess-x',
      turn_id: 'turn-x',
      expires_in_ms: 300_000,
      workspace_id: 'ws-other',
    })

    renderShell()

    const connectionBanner = await waitFor(() => screen.getByText('Lost connection to gateway'))
    const approvalBanner = await waitFor(() =>
      screen.getByText('1 approval waiting in UAT-T2'),
    )
    expect(connectionBanner).toBeInTheDocument()
    expect(approvalBanner).toBeInTheDocument()

    // Stacked siblings, not overlapping: the approval banner's ancestor
    // button must come AFTER the connection-error alert in document order
    // (matching source order — connectionError renders first), and the two
    // must not be the same element or contained one-in-the-other.
    const approvalButton = screen.getByTestId('cross-workspace-approval-banner')
    const alertBanner = connectionBanner.closest('[role="alert"]')
    expect(alertBanner).not.toBeNull()
    expect(approvalButton).not.toBe(alertBanner)
    expect(alertBanner?.contains(approvalButton)).toBe(false)
    expect(approvalButton.contains(alertBanner)).toBe(false)
    // DOCUMENT_POSITION_FOLLOWING (4): approvalButton comes after alertBanner.
    expect(
      alertBanner
        ? (alertBanner.compareDocumentPosition(approvalButton) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0
        : false,
    ).toBe(true)

    // <main> is still present and not pushed out of the DOM/collapsed to
    // nothing — both banners take their natural height in the flex column,
    // <main> (flex-1) absorbs the rest.
    const main = screen.getByTestId('app-main-content')
    expect(main).toBeInTheDocument()
  })
})

// ── God-mode banner is gone (founder decision 2026-09-25) ────────────────────
//
// The app-wide GodModeActiveBanner that ADR-092 FR-034 originally relocated
// into AppShell is deleted: its replacement is the sidebar God Mode pill plus
// ONE app-shell corner dot (src/components/layout/GodModeIndicators.tsx; the
// pill is covered in Sidebar.test.tsx, the dot in the describe below — the
// per-hamburger dots of the first revision are gone, and ScreenHeader.test.tsx
// pins their absence). These tests pin the deletion itself — even with the
// endpoint answering "on" (or failing), AppShell must render none of the
// banner's old surfaces. Sidebar is mocked to null in this file, so the pill
// cannot mask the assertion.
describe('AppShell — god-mode banner removal (2026-09-25)', () => {
  const PLATFORM_APP_STATE: AppState = {
    onboarding_complete: true,
    dev_mode_bypass: false,
    identity: { mode: 'platform', edition: 'hosted', signed_in: true },
  }

  it('renders none of the old banner testids even when god-mode is on', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    vi.mocked(api.fetchGodMode).mockResolvedValue({
      enabled: true,
      available: true,
      supported: true,
      persisted: true,
    })

    renderShell()

    await waitFor(() => {
      expect(screen.getByTestId('app-main-content')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('god-mode-active-banner')).not.toBeInTheDocument()
    expect(screen.queryByTestId('god-mode-banner-turn-off')).not.toBeInTheDocument()
    expect(screen.queryByTestId('god-mode-status-unknown-banner')).not.toBeInTheDocument()
  })

  it('renders no status-unknown banner when the god-mode fetch fails', async () => {
    vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    vi.mocked(api.fetchGodMode).mockRejectedValue(new Error('network error'))

    renderShell()

    await waitFor(() => {
      expect(screen.getByTestId('app-main-content')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('god-mode-status-unknown-banner')).not.toBeInTheDocument()
    expect(screen.queryByTestId('god-mode-active-banner')).not.toBeInTheDocument()
  })
})

// ── God Mode corner dot (founder decision 2026-09-25, revision 2) ────────────
//
// ONE indicator rendered from AppShell inside <main>, replacing the deleted
// per-hamburger dots: a small red dot in the top-left corner of the screen
// content, shown ONLY while god-mode is on AND the sidebar (and its pill) is
// off screen. Red when on, invisible in every other state — off, unknown
// (fetch error), loading, bypass; no amber variant exists anywhere. Because
// it mounts from the shell (not from any screen's header), it covers routes
// with no sidebar button too — Library, the live-browser view, admin chat.
// In this file Outlet renders null, which is exactly that no-screen-chrome
// shape: asserting the dot here IS the "present on a route without
// ScreenHeader" case.
describe('AppShell — God Mode corner dot (2026-09-25)', () => {
  // Sidebar visibility mirrors Sidebar.tsx: pinned only counts at ≥1024px
  // (a matchMedia stub), otherwise only the overlay's isOpen matters. jsdom
  // ships no matchMedia at all, so pin-viewport tests stub it per-test.
  const originalMatchMedia = window.matchMedia

  function stubMatchMedia(pinQueryMatches: boolean) {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        matches: query === `(min-width: 1024px)` ? pinQueryMatches : false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }),
    })
  }

  // Phone-takeover shape: answer BOTH queries AppShell/GodModeIndicators ask
  // — the sidebar pin breakpoint (1024px, false → sidebar never pinned) and
  // AppShell's own <640px phone signal (true → panel takeover active).
  function stubMatchMediaPhone() {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        matches:
          query === '(min-width: 1024px)'
            ? false
            : query === '(max-width: 639px)'
              ? true
              : false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }),
    })
  }

  const PLATFORM_APP_STATE: AppState = {
    onboarding_complete: true,
    dev_mode_bypass: false,
    identity: { mode: 'platform', edition: 'hosted', signed_in: true },
  }

  const GOD_MODE_ON = { enabled: true, available: true, supported: true, persisted: true }

  function mockAll(opts: { godMode?: typeof GOD_MODE_ON | Error; appState?: AppState } = {}) {
    vi.mocked(api.fetchAppState).mockResolvedValue(opts.appState ?? PLATFORM_APP_STATE)
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    if (opts.godMode instanceof Error) {
      vi.mocked(api.fetchGodMode).mockRejectedValue(opts.godMode)
    } else {
      vi.mocked(api.fetchGodMode).mockResolvedValue(opts.godMode ?? GOD_MODE_ON)
    }
  }

  // This file has no file-level clearAllMocks — without this, call counts
  // (and per-test mockResolvedValue overrides) leak between tests, which
  // matters for the "never calls fetchGodMode" assertion below.
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
    useUiStore.setState({ browserPanel: null, libraryPanel: null })
    if (originalMatchMedia === undefined) {
      delete (window as unknown as { matchMedia?: unknown }).matchMedia
    } else {
      Object.defineProperty(window, 'matchMedia', {
        configurable: true,
        writable: true,
        value: originalMatchMedia,
      })
    }
  })

  it('shows the corner dot when god-mode is on and the sidebar is hidden (no ScreenHeader — the Library/live-browser/admin-chat shape)', async () => {
    mockAll()
    // Default sidebar store state: closed overlay, unpinned → not visible.
    useSidebarStore.setState({ isOpen: false, isPinned: false })

    renderShell()

    const dot = await screen.findByTestId('god-mode-corner-dot')
    expect(dot).toHaveAttribute('aria-label', 'God Mode is on — open settings to turn it off')
    // The dot mounts at the SHELL ROOT (review round 2: a dot inside <main>
    // is clipped and inerted by phone-width takeover panels) — in the shell,
    // but never inside the screen-content region, the sidebar (mocked to
    // null here) or a screen header (Outlet renders none).
    expect(dot.closest('[data-app-shell]')).not.toBeNull()
    expect(dot.closest('main')).toBeNull()
  })

  // Review round 2, finding 3: below 640px the docked BrowserLivePanel /
  // LibraryPanel take over the full width, and AppShell collapses <main> to
  // zero width and inerts it. A dot living inside <main> is invisible and
  // unclickable exactly while God Mode is on — the highest-risk state shows
  // nothing. The dot must render at the shell root, above the panels,
  // outside the inert region.
  it('keeps the corner dot visible above a phone-width panel takeover, outside the inert <main> region', async () => {
    stubMatchMediaPhone()
    mockAll()
    useSidebarStore.setState({ isOpen: false, isPinned: false })
    useUiStore.setState({ browserPanel: { sessionId: 's1', agentId: 'a1' } })

    renderShell()

    const dot = await screen.findByTestId('god-mode-corner-dot')
    // The takeover really is active (same shape the inert tests pin)…
    const main = screen.getByTestId('app-main-content')
    expect(main.hasAttribute('inert')).toBe(true)
    // …and the dot is NOT inside the collapsed/inert region…
    expect(main.contains(dot)).toBe(false)
    // …but still in the shell, stacked above the static docked panels
    // (absolute + z-40 beats the plain <aside> flex siblings).
    expect(dot.closest('[data-app-shell]')).not.toBeNull()
    const anchor = screen.getByTestId('god-mode-corner-dot-anchor')
    expect(anchor.className).toContain('z-40')
  })

  // Review round 2, finding 4: the dot's hit area must never overlap the
  // sidebar-open hamburger's. Every hamburger-bearing screen fills the
  // top-left 44px band with the hamburger (the workspace one flush at x=0,
  // 44×44; ScreenHeader's spans x=8..48 at the same height) — no corner-
  // anchored hit area of ANY size can avoid eating part of it. The anchor
  // therefore starts BELOW the band, on the very token the hamburger rows
  // take their height from: --spacing-chrome-header backs h-chrome-header
  // (= 44px), so dot hit area y∈[44,68] and every hamburger ending at y=44
  // are disjoint BY CONSTRUCTION. Pixel geometry is e2e territory; this
  // pins the token contract both sides rely on.
  it('anchors the dot below the chrome-header band — never on top of the hamburger — at the pointer-minimum size', async () => {
    mockAll()
    useSidebarStore.setState({ isOpen: false, isPinned: false })

    renderShell()

    await screen.findByTestId('god-mode-corner-dot')
    const anchor = screen.getByTestId('god-mode-corner-dot-anchor')
    expect(anchor.className).toContain('left-0')
    expect(anchor.className).toContain('top-[var(--spacing-chrome-header)]')
    expect(anchor.className).not.toContain('top-0')
    // Accessible target size stays at the pointer minimum (WCAG 2.5.8):
    // the Link keeps its 24px square (--target-pointer-minimum).
    const dot = screen.getByTestId('god-mode-corner-dot')
    expect(dot.className).toContain('h-6')
    expect(dot.className).toContain('w-6')
  })

  it('renders no corner dot when god-mode is off', async () => {
    mockAll({ godMode: { enabled: false, available: true, supported: true, persisted: false } })

    renderShell()

    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-corner-dot')).not.toBeInTheDocument()
  })

  // Founder ruling 2026-09-25 revision 2: invisible when the status is
  // UNKNOWN (non-bypass fetch error) — this supersedes the deleted banner's
  // "never silence an unknown status" property, which the first revision of
  // the pill had carried as an amber variant.
  it('renders no corner dot when the status is unknown (god-mode fetch error)', async () => {
    mockAll({ godMode: new Error('network error') })

    renderShell()

    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-corner-dot')).not.toBeInTheDocument()
  })

  it('renders no corner dot while the sidebar overlay is open — the pill is on screen', async () => {
    mockAll()
    useSidebarStore.setState({ isOpen: true, isPinned: false })

    renderShell()

    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-corner-dot')).not.toBeInTheDocument()
  })

  it('renders no corner dot when the sidebar is pinned at a wide-enough viewport', async () => {
    stubMatchMedia(true)
    mockAll()
    useSidebarStore.setState({ isOpen: true, isPinned: true })

    renderShell()

    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-corner-dot')).not.toBeInTheDocument()
  })

  it('clicking the dot targets Settings → Gateway at the God Mode control', async () => {
    mockAll()

    renderShell()

    const dot = await screen.findByTestId('god-mode-corner-dot')
    // ?focus=god-mode is what lands the operator on the control (scroll +
    // focus, asserted in GodModeControl.test.tsx); URLSearchParams keeps
    // insertion order, hence tab before focus.
    expect(dot).toHaveAttribute('href', '/settings?tab=gateway&focus=god-mode')
  })

  // Ported from the deleted GodModeActiveBanner suite: under
  // dev_mode_bypass, bypass_gate.go 503s GET /api/v1/gateway/god-mode BY
  // DESIGN before the handler runs — that is not an outage and must not
  // surface as an indicator. The dot renders nothing for that 503, exactly
  // as the banner did.
  it('renders no corner dot when the god-mode fetch fails with a bypass-gate 503', async () => {
    mockAll({ godMode: new ApiError(503, 'this action is disabled while dev_mode_bypass is active') })

    renderShell()

    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-corner-dot')).not.toBeInTheDocument()
  })

  // Ported from the deleted banner suite (the 2026-09-24 fix it pinned):
  // once AppState.dev_mode_bypass is known true, the god-mode query must
  // never fire at all — firing it meant a real 503, retried 3× by the query
  // client, on every page load of a dev-mode-bypass install.
  it('never calls fetchGodMode when AppState.dev_mode_bypass is true, and renders no dot', async () => {
    mockAll({
      appState: { onboarding_complete: true, dev_mode_bypass: true, identity: DEFAULT_IDENTITY },
    })

    renderShell()

    await waitFor(() => {
      expect(api.fetchAppState).toHaveBeenCalled()
    })
    expect(api.fetchGodMode).not.toHaveBeenCalled()
    expect(screen.queryByTestId('god-mode-corner-dot')).not.toBeInTheDocument()
  })
})
