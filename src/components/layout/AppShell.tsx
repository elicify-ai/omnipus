import { useEffect } from 'react'
import { Outlet, useLocation } from '@tanstack/react-router'
import { List, CaretLeft } from '@phosphor-icons/react'
import { Sidebar } from './Sidebar'
import { useSidebarStore } from '@/store/sidebar'
import { SessionBar } from '@/components/chat/SessionBar'
import { ToastContainer } from '@/components/ui/toast-container'
import { ToolApprovalModal } from '@/components/agents/ToolApprovalModal'
import { OmnipusRuntimeProvider } from '@/components/chat/OmnipusRuntimeProvider'
import { ErrorBoundary } from '@/components/ui/error-boundary'
import { queryClient } from '@/lib/queryClient'
import { fetchTasks, fetchAgents, fetchAppState } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useQuery } from '@tanstack/react-query'
import { useVersionCheck } from '@/hooks/useVersionCheck'

// US-4: Application shell — hamburger + sidebar + main content area
export function AppShell() {
  const { toggle } = useSidebarStore()
  const location = useLocation()
  const connectionError = useConnectionStore((s) => s.connectionError)
  const reconnect = useConnectionStore((s) => s.reconnect)
  const { openSessionPanel } = useUiStore()

  const { data: appState } = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
    staleTime: 60_000,
  })
  const devModeBypass = appState?.dev_mode_bypass === true

  // Version-drift detection (#110): shows a toast when build_sha changes
  useVersionCheck()

  // Prefetch command center data on app load so it's cached when the user navigates there
  useEffect(() => {
    queryClient.prefetchQuery({ queryKey: ['tasks'], queryFn: () => fetchTasks(), staleTime: 30_000 })
    queryClient.prefetchQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  }, [])

  // Show SessionBar on the root chat route and on named session routes
  // (/#/sessions/:sessionId deep-links should also show the agent picker).
  const isChatScreen =
    location.pathname === '/' ||
    location.pathname === '' ||
    location.pathname.startsWith('/sessions/')

  return (
    <div className="flex h-dvh w-full overflow-hidden bg-[var(--color-primary)]">
      {/* Sidebar renders in both pinned (flex child) and overlay (fixed) modes */}
      <Sidebar />

      {/* Main content area — shrinks when sidebar is pinned */}
      <div className="flex flex-1 flex-col min-w-0 overflow-hidden">
        {/* OmnipusRuntimeProvider: AssistantUI context + WebSocket connection for entire app */}
        <OmnipusRuntimeProvider>
          {/* Top bar: on chat screen, left button is Sessions back-arrow; elsewhere it is the hamburger */}
          <header className="flex items-center gap-3 px-4 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
            {/* Hamburger — always on left */}
            <button
              id="sidebar-hamburger"
              onClick={toggle}
              aria-label="Toggle navigation sidebar"
              className="flex items-center justify-center h-8 w-8 rounded-md text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0"
            >
              <List size={20} />
            </button>

            {/* Session bar — wired only on chat screen */}
            <div className="flex-1 min-w-0">
              {isChatScreen ? (
                <SessionBar />
              ) : (
                <div id="session-bar-slot" className="flex-1" />
              )}
            </div>

            {/* Sessions button — top-right on chat screen, opens session drawer */}
            {isChatScreen && (
              <button
                type="button"
                onClick={openSessionPanel}
                aria-label="Open sessions panel"
                className="flex items-center justify-center h-8 px-2 gap-1 rounded-md text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0 text-xs"
              >
                <span className="hidden sm:inline">Sessions</span>
                <CaretLeft size={14} className="rotate-180" />
              </button>
            )}
          </header>

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
    </div>
  )
}
