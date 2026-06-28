import { useEffect } from 'react'
import { Outlet } from '@tanstack/react-router'
import { Sidebar } from './Sidebar'
import { NotificationPanel } from './NotificationPanel'
import { ToastContainer } from '@/components/ui/toast-container'
import { ToolApprovalModal } from '@/components/agents/ToolApprovalModal'
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

  return (
    <div data-app-shell className="flex h-dvh w-full overflow-hidden bg-[var(--color-primary)]">
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

      {/* Global toast notifications */}
      <ToastContainer />

      {/* Tool approval modal — FR-011, FR-082 */}
      <ToolApprovalModal />

      {/* Notification center panel — #264 */}
      <NotificationPanel />
    </div>
  )
}
