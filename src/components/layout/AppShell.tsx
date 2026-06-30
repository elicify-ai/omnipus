import { useEffect } from 'react'
import { Outlet } from '@tanstack/react-router'
import { Sidebar } from './Sidebar'
import { NotificationPanel } from './NotificationPanel'
import { ToastContainer } from '@/components/ui/toast-container'
import { ToolApprovalModal } from '@/components/agents/ToolApprovalModal'
import { MediaLightbox } from '@/components/chat/MediaLightbox'
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

  const { data: appState } = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
    staleTime: 60_000,
  })
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

  // iOS Safari sizes 100dvh to the LARGE viewport at initial load (until the
  // first scroll), so a fixed h-dvh shell is ~browser-toolbar-height taller than
  // what's actually visible and the page can be dragged/scrolled by that amount
  // on first load. Drive the shell height from window.visualViewport.height —
  // the EXACT visible area (it accounts for the browser toolbar AND the soft
  // keyboard) — published as the `--app-vh` CSS var and consumed below. Falls
  // back to 100dvh where visualViewport is unavailable (older browsers / SSR).
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return undefined
    const setAppHeight = () => {
      document.documentElement.style.setProperty('--app-vh', `${Math.round(vv.height)}px`)
    }
    setAppHeight()
    vv.addEventListener('resize', setAppHeight)
    vv.addEventListener('scroll', setAppHeight)
    return () => {
      vv.removeEventListener('resize', setAppHeight)
      vv.removeEventListener('scroll', setAppHeight)
    }
  }, [])

  return (
    // `fixed inset-x-0 top-0` pins the shell to the viewport so iOS Safari can
    // never scroll the whole document (header included) away — `overflow-hidden`
    // on html/body is not a hard guarantee there. Height comes from the
    // `--app-vh` var (window.visualViewport.height — the exact visible area)
    // rather than `h-dvh`, because iOS reports 100dvh as the large viewport at
    // load and that left a ~toolbar-height overscroll. `--app-vh` also tracks the
    // soft keyboard (with the index.html interactive-widget=resizes-content meta)
    // so the composer stays in view. Falls back to 100dvh pre-hydration / where
    // visualViewport is unavailable. Overlays (toasts/modals/lightbox) use their
    // own fixed/portal positioning and are unaffected (no transform here, so
    // descendant `position:fixed` stays viewport-relative).
    <div
      data-app-shell
      className="fixed inset-x-0 top-0 flex overflow-hidden bg-[var(--color-primary)]"
      style={{ height: 'var(--app-vh, 100dvh)' }}
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
    </div>
  )
}
