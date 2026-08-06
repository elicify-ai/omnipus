import React from 'react'
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
vi.mock('@tanstack/react-router', () => ({ Outlet: () => null, useNavigate: () => vi.fn(), useLocation: () => ({ pathname: '/' }), Link: ({ children, to, onClick, className }: { children: React.ReactNode; to: string; onClick?: () => void; className?: string }) => React.createElement('a', { href: to, onClick, className }, children) }))

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
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'

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
    })
    vi.mocked(api.fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)

    renderShell()

    const banner = await waitFor(() => screen.getByTestId('dev-mode-banner'))
    expect(banner).toHaveAttribute('role', 'alert')
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
