import { useEffect } from 'react'
import { Outlet } from '@tanstack/react-router'
import { Sidebar } from './Sidebar'
import { NotificationPanel } from './NotificationPanel'
import { ToastContainer } from '@/components/ui/toast-container'
import { ToolApprovalModal } from '@/components/agents/ToolApprovalModal'
import { MediaLightbox } from '@/components/chat/MediaLightbox'
import { BrowserLivePanel } from '@/components/browser/BrowserLivePanel'
import { SearchModal } from '@/components/search/SearchModal'
import { OmnipusRuntimeProvider } from '@/components/chat/OmnipusRuntimeProvider'
import { ErrorBoundary } from '@/components/ui/error-boundary'
import { queryClient } from '@/lib/queryClient'
import { fetchTasks, fetchAgents, fetchAppState, fetchNotifications } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useNotificationsStore } from '@/store/notifications'
import { useUiStore } from '@/store/ui'
import { useQuery } from '@tanstack/react-query'
import { useVersionCheck } from '@/hooks/useVersionCheck'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { computeAppMetrics } from './appShellViewport'

// US-4: Application shell — sidebar + main content area
export function AppShell() {
  const connectionError = useConnectionStore((s) => s.connectionError)
  const reconnect = useConnectionStore((s) => s.reconnect)
  const hydrateNotifications = useNotificationsStore((s) => s.hydrate)

  const { data: appState, isError: appStateError } = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
    staleTime: 60_000,
  })
  // `dev_mode_bypass` is a security-relevant state — `appState` being
  // undefined on a fetch failure must NOT collapse to the same "bypass is
  // off" falsy value as a genuinely successful fetch that reports it off.
  // See the app-state-fetch-error-banner below for the fetch-failure case.
  const devModeBypass = appState?.dev_mode_bypass === true

  // Live browser panel open state — used below to inert the chat region when
  // it's collapsed to zero width by the panel's docked takeover on phones.
  const browserPanel = useUiStore((s) => s.browserPanel)
  // Tailwind's `sm:` breakpoint (640px) can gate CSS, but not a non-CSS HTML
  // attribute like `inert` — that needs an actual JS media-query signal.
  const isPhoneViewport = useMediaQuery('(max-width: 639px)')

  // #264: seed the notification center from REST on mount; the `notification`
  // WS frame keeps it live thereafter (see chatStore.handleFrame).
  const { data: notificationList } = useQuery({
    queryKey: ['notifications'],
    queryFn: fetchNotifications,
    staleTime: 60_000,
  })
  useEffect(() => {
    if (notificationList) hydrateNotifications(notificationList)
  }, [notificationList, hydrateNotifications])

  // Version-drift detection (#110): shows a toast when build_sha changes
  useVersionCheck()

  // Prefetch command center data on app load so it's cached when the user navigates there
  useEffect(() => {
    queryClient.prefetchQuery({ queryKey: ['tasks'], queryFn: () => fetchTasks(), staleTime: 30_000 })
    queryClient.prefetchQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  }, [])

  // Pin the shell to the actual VISUAL viewport via window.visualViewport, not
  // CSS viewport units — ALWAYS ON, not gated on focus. See
  // docs/internal/architecture/ios-scroll-stability.md (part 2) for the full
  // regression history; the short version:
  //  • iOS pans the VISUAL viewport (not the document — that's locked by
  //    `body{position:fixed}`) whenever the keyboard opens OR a tap lands on
  //    non-editable chrome (a message bubble, a tab, empty space). Either way
  //    `vv.offsetTop` can become > 0, and a shell anchored to the
  //    layout-viewport top has its header pushed above the visible area
  //    unless it mirrors `vv.offsetTop` into its own `top`.
  //  • A prior "fix" (`dec7713b`) gated var-publishing on an editable being
  //    focused. That REMOVED the vars (shell snaps to top:0/100dvh) the
  //    instant focus left the composer for anything non-editable — the
  //    exact "tap a message and the header jumps" regression this hook now
  //    fixes. The gate is gone; tracking runs on every vv resize/scroll
  //    regardless of `document.activeElement`.
  //  • The gate existed to guard against a DIFFERENT bug: always-on tracking
  //    could latch a stale-short `vv.height` after the keyboard closed (a
  //    missed final `resize` event), permanently shortening the shell
  //    (IMG_0616). That guard is now DETERMINISTIC instead of focus-based —
  //    see `computeAppMetrics` in `./appShellViewport`: `--app-vh` is
  //    removed (CSS `100dvh` fallback) whenever `|vv.height - innerHeight| <
  //    2px` (keyboard closed by height math, not by what's focused), and set
  //    to `vv.height` otherwise. `--app-top` is never gated at all.
  //  • iOS can still drop the FINAL `resize` event right as the keyboard
  //    closes — the same miss `dec7713b` was papering over. Instead of
  //    disabling tracking, a `focusout` listener schedules a trailing
  //    re-read (~250ms) that recomputes the vars from whatever
  //    `visualViewport` reports once things have settled.
  // Published as `--app-vh` / `--app-top`; the shell consumes them below and
  // falls back to (100dvh, 0) whenever visualViewport is unavailable
  // (desktop, older browsers, pre-hydrate).
  useEffect(() => {
    const vv = window.visualViewport
    // Only track visualViewport on touch devices (iOS Safari needs it for the
    // on-screen keyboard). On desktop the CSS fallback (100dvh, 0px) is
    // always correct.
    if (!vv || !window.matchMedia('(pointer: coarse)').matches) return undefined
    let raf = 0
    let trailingRead: ReturnType<typeof setTimeout> | undefined
    const applyMetrics = () => {
      const { appTop, appVh } = computeAppMetrics(vv, window.innerHeight)
      document.documentElement.style.setProperty('--app-top', appTop)
      if (appVh) {
        document.documentElement.style.setProperty('--app-vh', appVh)
      } else {
        document.documentElement.style.removeProperty('--app-vh')
      }
    }
    const setAppMetrics = () => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(applyMetrics)
    }
    // iOS occasionally drops the final vv `resize` right as the keyboard
    // closes on blur — re-read once things have settled to catch it.
    const handleFocusOut = () => {
      setAppMetrics()
      if (trailingRead) clearTimeout(trailingRead)
      trailingRead = setTimeout(() => {
        trailingRead = undefined
        applyMetrics()
      }, 250)
    }
    setAppMetrics()
    vv.addEventListener('resize', setAppMetrics)
    vv.addEventListener('scroll', setAppMetrics)
    document.addEventListener('focusout', handleFocusOut)
    return () => {
      cancelAnimationFrame(raf)
      if (trailingRead) clearTimeout(trailingRead)
      vv.removeEventListener('resize', setAppMetrics)
      vv.removeEventListener('scroll', setAppMetrics)
      document.removeEventListener('focusout', handleFocusOut)
    }
  }, [])

  return (
    // Shell pinned to the VISUAL viewport (see the --app-vh / --app-top hook
    // above): `fixed inset-x-0` for width, `top`/`height` from the vars so it
    // tracks the visible area through toolbar collapse + keyboard. `overflow-
    // hidden` (with the html/body locks) keeps the inner message list the sole
    // scroller. NB: do NOT add a transform here — overlays (toasts/modals/
    // lightbox) rely on descendant `position:fixed` staying viewport-relative.
    <div
      data-app-shell
      className="fixed inset-x-0 flex overflow-hidden bg-[var(--color-primary)]"
      style={{ top: 'var(--app-top, 0px)', height: 'var(--app-vh, 100dvh)' }}
    >
      {/* Skip-to-content link — first focusable element in the shell, visually
          hidden until it receives keyboard focus (WCAG 2.4.1 Bypass Blocks).
          tabIndex={1}: the composer's positive tab ring (see the map in
          ChatControls.tsx) starts at 2, so this link is guaranteed to be the
          FIRST stop document-wide regardless of DOM position — without an
          explicit positive index here, the chat screen's positive-tabIndex
          controls would win the first Tab and the skip link would never be
          reachable (WCAG 2.4.1 functionally dead). */}
      <a
        href="#main-content"
        tabIndex={1}
        className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:top-2 focus:left-2 focus:px-3 focus:py-2 focus:rounded-md focus:bg-[var(--color-surface-2)] focus:text-[var(--color-secondary)] focus:text-sm"
      >
        Skip to content
      </a>

      {/* Sidebar renders in both pinned (flex child) and overlay (fixed) modes */}
      <Sidebar />

      {/* Main content area — shrinks when sidebar is pinned; each screen owns its own top bar.
          `inert` when the docked BrowserLivePanel takeover has collapsed this
          region to zero width on a phone viewport (<640px): the flex layout
          still keeps its controls in the DOM (and thus in the Tab order) even
          though they're visually gone, so without this the invisible chat
          controls would still catch focus/Tab stops. `sm:` can't gate a
          non-CSS attribute, hence the matchMedia-backed useMediaQuery above. */}
      <div
        data-testid="app-main-content"
        className="flex flex-1 flex-col min-w-0 overflow-hidden"
        inert={Boolean(browserPanel) && isPhoneViewport}
      >
        {/* OmnipusRuntimeProvider: AssistantUI context + WebSocket connection for entire app */}
        <OmnipusRuntimeProvider>
          {/* Global connection error banner — visible on every screen */}
          {connectionError && (
            <div
              role="alert"
              className="flex items-center justify-between gap-2 px-4 py-2 bg-[var(--color-error)]/10 border-b border-[var(--color-error)]/20 text-xs text-[var(--color-error)] shrink-0"
            >
              <span>{connectionError}</span>
              <button tabIndex={0}
                type="button"
                onClick={reconnect}
                className="px-2 py-1 rounded text-xs hover:bg-[var(--color-error)]/20 transition-colors"
              >
                Retry
              </button>
            </div>
          )}

          {/* Dev-mode bypass banner — persistent red warning when dev_mode_bypass=true */}
          {devModeBypass && (
            <div
              data-testid="dev-mode-banner"
              role="alert"
              className="flex items-center gap-2 px-4 py-2 bg-[var(--color-error)] text-white text-xs font-medium shrink-0"
            >
              <span>Development mode active — authentication bypass enabled</span>
            </div>
          )}

          {/* App-state fetch failed — dev-mode-bypass status is unknown, not
              confirmed off. Must not silently vanish like the security-relevant
              GodModeActiveBanner must not on its own fetch failure: show an
              explicit "status unknown" indicator instead of nothing. */}
          {appStateError && (
            <div
              data-testid="app-state-fetch-error-banner"
              role="alert"
              className="flex items-center gap-2 px-4 py-2 bg-amber-500/10 border-b border-amber-500/30 text-amber-400 text-xs font-medium shrink-0"
            >
              <span>
                Could not fetch gateway state — security status (e.g. development-mode bypass) is
                unknown. Check your connection and reload.
              </span>
            </div>
          )}

          {/* Screen content — relative so children can use absolute inset-0 for bounded scrolling */}
          <main id="main-content" tabIndex={-1} className="flex-1 relative min-h-0 overflow-hidden">
            <ErrorBoundary>
              <Outlet />
            </ErrorBoundary>
          </main>
        </OmnipusRuntimeProvider>
      </div>

      {/* Global enlarged-media overlay (images + diagrams) — single instance,
          decoupled from the virtualized chat list so it survives row remounts */}
      <MediaLightbox />

      {/* Cross-workspace session search — store-driven single instance opened
          from the sidebar search icon and the /resume slash command (step 6). */}
      <SearchModal />

      {/* Global toast notifications */}
      <ToastContainer />

      {/* Tool approval modal — FR-011, FR-082 */}
      <ToolApprovalModal />

      {/* Notification center panel — #264 */}
      <NotificationPanel />

      {/* Live interactive browser panel — ADR-038; always-docked since
          2026-07-16 (operator direction, amends ADR-040 D4 — the unpinned
          Sheet overlay + pin toggle are retired). Rendered here as a plain
          (non-portaled) child of this `flex` row deliberately: when open,
          BrowserLivePanel's root is a `flex-shrink-0` docked column (see its
          file for the width/min/max), which makes the chat region's
          `flex-1 min-w-0` above shrink automatically to share width with it
          — a real side-by-side split, no extra layout code needed here.
          Closed = renders null. Do NOT move this render call outside the
          flex row, and do NOT wrap it in anything `fixed`/`absolute` —
          either would break the docked layout's participation in this flex
          row. */}
      <BrowserLivePanel />
    </div>
  )
}
