import { useEffect } from 'react'
import { Outlet } from '@tanstack/react-router'
import { Sidebar } from './Sidebar'
import { NotificationPanel } from './NotificationPanel'
import { ToastContainer } from '@/components/ui/toast-container'
import { ToolApprovalModal } from '@/components/agents/ToolApprovalModal'
import { MediaLightbox } from '@/components/chat/MediaLightbox'
import { BrowserLivePanel } from '@/components/browser/BrowserLivePanel'
import { OmnipusRuntimeProvider } from '@/components/chat/OmnipusRuntimeProvider'
import { ErrorBoundary } from '@/components/ui/error-boundary'
import { queryClient } from '@/lib/queryClient'
import { fetchTasks, fetchAgents, fetchAppState, fetchNotifications } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useNotificationsStore } from '@/store/notifications'
import { useQuery } from '@tanstack/react-query'
import { useVersionCheck } from '@/hooks/useVersionCheck'

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
  // CSS viewport units. Two iOS Safari behaviors make units insufficient:
  //  • At initial load iOS reports 100dvh as the LARGE viewport (until the first
  //    scroll), so an h-dvh shell is ~toolbar-height too tall → a load-time
  //    overscroll. `vv.height` is the exact visible height instead.
  //  • Focusing the chat input shows the keyboard and iOS scrolls the VISUAL
  //    viewport (not the document — that's locked) to reveal the input, so
  //    `vv.offsetTop` becomes > 0 and a shell anchored to the layout-viewport
  //    top has its header pushed above the visible area. We mirror `vv.offsetTop`
  //    into the shell's `top` so the shell follows the visible viewport and the
  //    header stays put.
  // Published as `--app-vh` / `--app-top`; the shell consumes them below and
  // falls back to (100dvh, 0) where visualViewport is unavailable / pre-hydrate.
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return undefined
    let raf = 0
    const setAppMetrics = () => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        document.documentElement.style.setProperty('--app-vh', `${Math.round(vv.height)}px`)
        document.documentElement.style.setProperty('--app-top', `${Math.round(vv.offsetTop)}px`)
      })
    }
    setAppMetrics()
    vv.addEventListener('resize', setAppMetrics)
    vv.addEventListener('scroll', setAppMetrics)
    return () => {
      cancelAnimationFrame(raf)
      vv.removeEventListener('resize', setAppMetrics)
      vv.removeEventListener('scroll', setAppMetrics)
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
      {/* Sidebar renders in both pinned (flex child) and overlay (fixed) modes */}
      <Sidebar />

      {/* Main content area — shrinks when sidebar is pinned; each screen owns its own top bar */}
      <div className="flex flex-1 flex-col min-w-0 overflow-hidden">
        {/* OmnipusRuntimeProvider: AssistantUI context + WebSocket connection for entire app */}
        <OmnipusRuntimeProvider>
          {/* Global connection error banner — visible on every screen */}
          {connectionError && (
            <div className="flex items-center justify-between gap-2 px-4 py-2 bg-[var(--color-error)]/10 border-b border-[var(--color-error)]/20 text-xs text-[var(--color-error)] shrink-0">
              <span>{connectionError}</span>
              <button
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
          <main className="flex-1 relative min-h-0 overflow-hidden">
            <ErrorBoundary>
              <Outlet />
            </ErrorBoundary>
          </main>
        </OmnipusRuntimeProvider>
      </div>

      {/* Global enlarged-media overlay (images + diagrams) — single instance,
          decoupled from the virtualized chat list so it survives row remounts */}
      <MediaLightbox />

      {/* Global toast notifications */}
      <ToastContainer />

      {/* Tool approval modal — FR-011, FR-082 */}
      <ToolApprovalModal />

      {/* Notification center panel — #264 */}
      <NotificationPanel />

      {/* Live interactive browser panel — ADR-038, Pin/side-by-side ADR-040 D4.
          Rendered here as a plain (non-portaled) child of this `flex` row
          deliberately: when the panel is open AND pinned, BrowserLivePanel's
          own root becomes a `flex-shrink-0` docked column (see its file for
          the width/min/max), which makes the chat region's `flex-1 min-w-0`
          above shrink automatically to share width with it — a real
          side-by-side split, no extra layout code needed here. When closed,
          or open-but-unpinned (the default), BrowserLivePanel instead
          renders its content through a Radix `Sheet` (Dialog + Portal),
          which detaches from this DOM position entirely (portals to
          `document.body`) — same overlay behaviour as before ADR-040. Do NOT
          move this render call outside the flex row, and do NOT wrap it in
          anything `fixed`/`absolute` — either would break the docked-pinned
          layout's participation in this flex row. */}
      <BrowserLivePanel />
    </div>
  )
}
