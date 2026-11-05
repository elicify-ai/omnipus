import { createContext, useContext, useEffect, useRef, type ReactNode } from 'react'
import { Outlet, useNavigate, useLocation } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import type { Workspace } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { clearLibraryAttachments } from '@/lib/library-attachment'
import { resolveWorkspaceSwitch } from '@/components/panel-shell/workspaceSwitch'
import { getPanelDefinition } from '@/components/panel-shell/registry'
import { isWorkspaceScopedPanel } from '@/components/panel-shell/types'
import type { WorkspacePanelContext } from '@/components/panel-shell/types'
import { ChatControls } from '@/components/chat/ChatControls'
import { ChatHeaderSlot } from '@/components/chat/ChatHeaderSlot'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { Button } from '@/components/ui/button'
import { SidebarShowButton } from '@/components/layout/SidebarShowButton'
import { Card } from '@/components/ui/card'
import { WorkspaceTabBar, resolveActiveSegment } from './WorkspaceTabBar'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'

// React context carrying the resolved workspace to every tab.
const WorkspaceContext = createContext<Workspace | null>(null)

/** Hook for tab components to read the resolved workspace. */
export function useActiveWorkspace(): Workspace {
  const ws = useContext(WorkspaceContext)
  if (!ws) {
    throw new Error('useActiveWorkspace must be used within a WorkspaceTabContainer')
  }
  return ws
}

/** Binds a workspace as the active one for children that read
 *  `useActiveWorkspace()`, WITHOUT the workspace route shell — the side
 *  panel's TeamPanel resolves the workspace itself (fetchWorkspace by the
 *  panel context's workspaceId) and mounts WorkspaceTeamTab, which is
 *  written against the context, under this provider for both its docked and
 *  full-screen presentations. The route container above stays the only
 *  writer during normal workspace navigation. */
export function WorkspaceContextProvider({
  workspace,
  children,
}: {
  workspace: Workspace
  children: ReactNode
}) {
  return <WorkspaceContext.Provider value={workspace}>{children}</WorkspaceContext.Provider>
}

interface WorkspaceTabContainerProps {
  workspaceId: string
}

/**
 * The workspace detail shell. Resolves the workspace, binds it as the active
 * workspace, manages session lifecycle via enterWorkspaceChat, and renders:
 *
 *   Row 1 — top bar: [hamburger] [WorkspaceTabBar] [spacer] [ChatControls (chat only)]
 *   Row 2 — tab content (Outlet)
 *
 * Session lifecycle: enterWorkspaceChat(workspace.id) fires whenever the
 * workspaceId changes (NOT on tab switches within the same workspace), so
 * Chat→Board→Chat preserves the active conversation (Bug 1 fix).
 */
export function WorkspaceTabContainer({ workspaceId }: WorkspaceTabContainerProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const { activeWorkspaceId, setActiveWorkspaceId, setActivePlanId } = useWorkspacesStore()
  const enterWorkspaceChat = useSessionStore((s) => s.enterWorkspaceChat)
  const previousRouteWorkspaceRef = useRef(workspaceId)

  const {
    data: workspaces = [],
    isError: workspacesError,
    isLoading: workspacesLoading,
    refetch: refetchWorkspaces,
  } = useQuery({
    ...activeWorkspacesQueryOptions,
    staleTime: 30_000,
  })

  // Resolve 'inbox' alias to the real default workspace id.
  useEffect(() => {
    if (workspaceId !== 'inbox') return
    if (workspaces.length === 0) return
    const def = workspaces.find((w) => w.is_default)
    if (def) {
      void navigate({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: def.id },
        replace: true,
      })
    }
  }, [workspaceId, workspaces, navigate])

  // SP-29: preserve or re-target the open panel when the route changes
  // workspaces. The router blocker owns pre-navigation cancellation; this
  // call site applies the panel-specific state move after an allowed switch.
  useEffect(() => {
    const previousWorkspaceId = previousRouteWorkspaceRef.current
    previousRouteWorkspaceRef.current = workspaceId
    if (
      !previousWorkspaceId ||
      previousWorkspaceId === 'inbox' ||
      !workspaceId ||
      workspaceId === 'inbox' ||
      previousWorkspaceId === workspaceId
    ) {
      return
    }

    // A named panel in the incoming URL is an adoption, not an ordinary
    // workspace switch. Leave its mailbox decision to usePanelDeepLink: moving
    // Mail here first could make that hook mistake a bare link for an already
    // adopted panel and keep the previous workspace's mailbox.
    if (typeof location.search.panel === 'string') return

    const activePanel = useUiStore.getState().activePanel
    if (!activePanel) return
    const openedWorkspaceId =
      activePanel.context.workspaceId === previousWorkspaceId
        ? previousWorkspaceId
        : undefined

    void resolveWorkspaceSwitch({
      panelId: activePanel.id,
      openedWorkspaceId,
      nextWorkspaceId: workspaceId,
      beforeLeave: openedWorkspaceId ? getPanelDefinition(activePanel.id)?.beforeLeave : undefined,
    }).then((decision) => {
      if (previousRouteWorkspaceRef.current !== workspaceId) return
      if (decision.action === 'cancel') {
        void navigate({
          to: '/workspaces/$workspaceId/chat',
          params: { workspaceId: previousWorkspaceId },
          replace: true,
        })
        return
      }
      if (decision.action !== 'follow') return

      const latest = useUiStore.getState().activePanel
      if (latest !== activePanel) return
      if (!isWorkspaceScopedPanel(activePanel.id)) return
      const context = activePanel.context as WorkspacePanelContext
      useUiStore.getState().openPanel(activePanel.id, {
        ...context,
        workspaceId: decision.workspaceId,
      })
    })
  }, [workspaceId, navigate, location.search.panel])

  // Bind the active workspace from the route.
  useEffect(() => {
    if (workspaceId && workspaceId !== 'inbox' && activeWorkspaceId !== workspaceId) {
      setActiveWorkspaceId(workspaceId)
    }
  }, [workspaceId, activeWorkspaceId, setActiveWorkspaceId])

  // Reset the plan scope on workspace change so the Board/Graph don't stay
  // drilled into a plan that belongs to the previous workspace.
  useEffect(() => {
    setActivePlanId(null)
  }, [workspaceId, setActivePlanId])

  // ADR-051 Rev 4 (Slice H): drop any staged library attachments when the
  // active workspace changes — their media://workspace/<ws>/<id> refs are
  // scoped to the previous workspace and must not leak into the new one.
  // Keyed on the workspace only: a session directive appearing in the address
  // (#1437) re-runs the entry effect below but must NOT drop staged
  // attachments — the workspace has not changed.
  useEffect(() => {
    if (!workspaceId || workspaceId === 'inbox') return
    clearLibraryAttachments()
  }, [workspaceId])

  // Bug 1 fix: manage session lifecycle at the workspace level.
  // Fires only on workspaceId change — tab switches don't re-run this.
  //
  // #1437: a chat address that names its session (`?open=<id>`, written by
  // the /sessions/{id} deep-link redirect) drives the entry — the linked chat
  // wins over the workspace's remembered pointer, so a reload of the address
  // lands back in exactly that chat even when another tab has since changed
  // what this browser last remembered here. The directive is acted on only
  // while it is present: when the address drops it (New chat clears the
  // active session and the chat route's address sync follows), this effect
  // must NOT re-resolve — that would yank the fresh composer back to the
  // remembered chat. The ref below makes that disappearance a no-op while
  // keeping plain workspace entry (and a later workspace change) untouched.
  const linkedSessionId =
    typeof location.search.open === 'string' ? location.search.open : undefined
  const prevRunHadDirectiveRef = useRef(false)
  useEffect(() => {
    if (!workspaceId || workspaceId === 'inbox') return
    if (linkedSessionId && linkedSessionId !== '__pending') {
      prevRunHadDirectiveRef.current = true
      enterWorkspaceChat(workspaceId, { linkedSessionId })
      return
    }
    if (prevRunHadDirectiveRef.current) {
      // The directive just left the address (New chat) — the fresh composer
      // is the state the user chose; do not re-resolve over it. (A no-op
      // re-entry against an already-attached chat is harmless — execute()
      // early-returns — but a New chat's fresh composer must not be
      // re-resolved back to the remembered chat.)
      prevRunHadDirectiveRef.current = false
      return
    }
    enterWorkspaceChat(workspaceId)
  }, [workspaceId, linkedSessionId, enterWorkspaceChat])

  // Fall back to archived workspace list for direct-URL access.
  const { data: archivedWorkspaces = [], isLoading: archivedLoading } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'archived' }),
    queryFn: () => fetchWorkspaces({ status: 'archived' }),
    staleTime: 60_000,
    enabled: workspaces.length > 0 && !workspaces.find((w) => w.id === workspaceId),
  })

  const workspace =
    workspaces.find((w) => w.id === workspaceId) ??
    archivedWorkspaces.find((w) => w.id === workspaceId)

  if (workspaceId === 'inbox') return null

  if (workspacesError) {
    return (
      <QueryErrorState
        layout="fill"
        message="Failed to load workspace. Check your connection and try again."
        onRetry={() => void refetchWorkspaces()}
        testId="workspace-container-error"
      />
    )
  }

  if (workspacesLoading) return <WorkspaceShellSkeleton />

  if (!workspace) {
    if (archivedLoading) return <WorkspaceShellSkeleton />
    return <WorkspaceNotFoundState />
  }

  return (
    <WorkspaceTabContainerView workspace={workspace} />
  )
}

/** Inner view — extracted so useLocation can run unconditionally. */
function WorkspaceTabContainerView({
  workspace,
}: {
  workspace: Workspace
}) {
  const location = useLocation()
  const activeSegment = resolveActiveSegment(location.pathname, workspace.id)

  return (
    <WorkspaceContext.Provider value={workspace}>
      <div className="absolute inset-0 flex flex-col overflow-hidden">
        {/* Top bar — semantic <header> (banner landmark) for accessible
            landmark navigation, reachable via both getByRole('banner') and a
            `header` CSS selector. @container enables container-query variants
            on children (used by ChatControls' "Open browser" label).
            WorkspaceTabBar measures the remaining flex slot instead of
            using a fixed container breakpoint. data-header-line marks this
            row as the line it measures against: the chat title in the slot
            at the right end is room the strip may take back
            (useWorkspaceHeaderMode).
            No secondary breadcrumb row: the tab strip already names the
            workspace + active view, so a second chrome line was redundant and
            broke the flat 44px shell alignment.
            The God Mode corner dot that used to live on this hamburger is
            deleted (founder decision 2026-09-25 revision 2) — AppShell
            renders one app-wide corner indicator instead; see
            GodModeIndicators.tsx. */}
        <header
          role="banner"
          className="@container flex items-center h-chrome-header min-h-chrome-header bg-[var(--color-surface-0)] flex-shrink-0"
          data-testid="workspace-top-bar"
          data-header-line=""
        >
          <SidebarShowButton
            testId="workspace-hamburger"
            className="h-chrome-header min-h-chrome-header w-11 rounded-none text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0"
          />

          {/* The measured strip owns the flexible space. The sidebar launcher
              and browser controls keep their natural width at every mode. */}
          <WorkspaceTabBar workspaceId={workspace.id} workspaceName={workspace.name} />

          {activeSegment === 'chat' && (
            <div className="flex shrink-0 items-center justify-end px-[var(--space-2-5)]" data-testid="workspace-chat-controls">
              <ChatControls />
            </div>
          )}

          {/* #1449 D13 (founder 2026-10-28): the chat's own chrome sits at
              the right end of THIS header line — the same line as the
              workspace strip, which stays where it is on the left. No new
              row, no bar, no border, no background: the slot carries the
              chat label / helper breadcrumb and the chat controls (today
              "Add participant"; the group chat's Participants button plugs
              into the same slot later). On a room page the slot stays
              empty — the room owns its own chrome until the group work
              lands. */}
          {activeSegment === 'chat' && <ChatHeaderSlot workspaceId={workspace.id} />}
        </header>

        {/* Tab content */}
        <div className="flex-1 min-h-0 relative">
          <Outlet />
        </div>
      </div>
    </WorkspaceContext.Provider>
  )
}

function WorkspaceNotFoundState() {
  const navigate = useNavigate()
  const { data: workspaces = [] } = useQuery({
    ...activeWorkspacesQueryOptions,
    staleTime: 30_000,
  })

  function handleBackToWorkspace() {
    const defaultWs = workspaces.find((w) => w.is_default) ?? workspaces[0]
    if (defaultWs) {
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: defaultWs.id } })
    } else {
      void navigate({ to: '/' })
    }
  }

  return (
    <div
      className="flex flex-col items-center justify-center h-full gap-[var(--space-3)] p-[var(--space-5)] text-center"
      data-testid="workspace-not-found"
    >
      <p className="text-[var(--color-muted)] text-[length:var(--type-body-compact-size)]">Workspace not found.</p>
      <p className="text-[var(--color-muted)] text-[length:var(--type-utility-xs-size)] max-w-xs">
        This workspace may have been deleted or the link may be outdated.
      </p>
      <Button
        onClick={handleBackToWorkspace}
        data-testid="workspace-not-found-back-btn"
        className="hover:opacity-90"
      >
        Back to my workspace
      </Button>
    </div>
  )
}

function WorkspaceShellSkeleton() {
  return (
    <div className="absolute inset-0 flex flex-col">
      <div className="flex gap-[var(--space-2)] px-[var(--space-2-5)] h-chrome-header min-h-chrome-header bg-[var(--color-surface-0)]">
        {[1, 2, 3, 4, 5, 6, 7].map((i) => (
          <div key={i} className="h-11 w-20 my-[var(--space-1)] rounded bg-[var(--color-surface-2)] animate-pulse" />
        ))}
      </div>
      <div className="flex gap-[var(--space-2-5)] p-[var(--space-3)] flex-1">
        {[1, 2, 3, 4, 5].map((i) => (
          <Card
            key={i}
            variant="default"
            className="flex-1 min-w-[180px] h-48 rounded-xl animate-pulse"
          />
        ))}
      </div>
    </div>
  )
}
