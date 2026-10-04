import { lazy, type ReactNode } from 'react'
import { useWorkspacesStore } from '@/store/workspacesStore'
import {
  confirmDiscardLibraryEdits,
  isLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
import type {
  PanelContentProps,
  PanelContext,
  PanelDefinition,
  PanelId,
  WorkspacePanelContext,
} from './types'

function optionalSearchString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined
}

const LibraryPanel = lazy(async () => {
  const module = await import('@/components/library/LibraryPanel')
  return { default: module.LibraryPanel }
})

const BrowserLivePanel = lazy(async () => {
  const module = await import('@/components/browser/BrowserLivePanel')
  return { default: module.BrowserLivePanel }
})

const WorkspaceTasksPanel = lazy(async () => {
  const module = await import('@/components/workspaces/WorkspaceTasksTab')
  return { default: module.WorkspaceTasksTab }
})

const WorkspaceTeamPanel = lazy(async () => {
  const module = await import('@/components/workspaces/WorkspaceTeamTab')
  return { default: module.WorkspaceTeamTab }
})

const CalendarPanel = lazy(async () => {
  const module = await import('@/components/screens/CalendarScreen')
  return { default: module.CalendarScreen }
})

function LibraryPanelContent(props: PanelContentProps) {
  return <LibraryPanel shellProps={props} />
}

function BrowserPanelContent(props: PanelContentProps) {
  return <BrowserLivePanel shellProps={props} />
}

/** Shell seam for the workspace-scoped screens (Tasks/Team/Calendar — wave 3,
 * SP-6): resolves the panel context's workspace (falling back to the active
 * workspace the way the full-screen route's own re-dock does) and hands the
 * real screen component just the `workspaceId` it already takes. The screens'
 * internal layout is content work — this wrapper only adapts the contract. */
function WorkspaceScopedPanelContent({
  context,
  render,
}: {
  context: PanelContext
  render: (workspaceId: string) => ReactNode
}) {
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const workspaceId = (context as WorkspacePanelContext).workspaceId ?? activeWorkspaceId
  if (!workspaceId) {
    return (
      <div
        role="status"
        className="flex h-full items-center justify-center p-[var(--space-4)] text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]"
      >
        Open this panel from inside a workspace to see its content here.
      </div>
    )
  }
  return <>{render(workspaceId)}</>
}

function TasksPanelContent(props: PanelContentProps) {
  return <WorkspaceScopedPanelContent context={props.context} render={(id) => <WorkspaceTasksPanel workspaceId={id} />} />
}

function TeamPanelContent(props: PanelContentProps) {
  return <WorkspaceScopedPanelContent context={props.context} render={(id) => <WorkspaceTeamPanel workspaceId={id} />} />
}

function CalendarPanelContent(props: PanelContentProps) {
  return <WorkspaceScopedPanelContent context={props.context} render={(id) => <CalendarPanel workspaceId={id} />} />
}

/** The workspace search codec shared by every workspace-scoped panel: the
 * workspace is REQUIRED on the full-screen route (a bare /panel/&lt;id&gt; link
 * is an invalid link — the route shows its recovery page), and it round-trips
 * exactly. */
function workspaceFullScreenCodec() {
  return {
    toSearch: ({ workspaceId }: PanelContext) => ({
      ...(workspaceId ? { workspace: workspaceId } : {}),
    }),
    fromSearch: (search: Record<string, unknown>) => {
      const workspaceId = optionalSearchString(search.workspace)
      return workspaceId ? { workspaceId } : null
    },
  }
}

export const panels: readonly PanelDefinition[] = [
  {
    id: 'library',
    title: 'Library',
    content: LibraryPanelContent,
    fullScreen: {
      toSearch: ({ workspaceId, path }) => ({
        ...(workspaceId ? { workspace: workspaceId } : {}),
        ...(path ? { path } : {}),
      }),
      fromSearch: (search) => {
        const workspaceId = optionalSearchString(search.workspace)
        const path = optionalSearchString(search.path)
        return {
          ...(workspaceId ? { workspaceId } : {}),
          ...(path ? { path } : {}),
        }
      },
    },
    beforeLeave: () => confirmDiscardLibraryEdits(),
    beforeLeaveRequired: () => isLibraryEditorDirty(),
  },
  {
    id: 'browser',
    title: 'Browser',
    content: BrowserPanelContent,
    fullScreen: {
      toSearch: ({ sessionId, agentId }) => ({
        ...(sessionId ? { session: sessionId } : {}),
        ...(agentId ? { agent: agentId } : {}),
      }),
      fromSearch: (search) => {
        const sessionId = optionalSearchString(search.session)
        const agentId = optionalSearchString(search.agent)
        return sessionId && agentId ? { sessionId, agentId } : null
      },
    },
  },
  {
    id: 'tasks',
    title: 'Tasks',
    content: TasksPanelContent,
    fullScreen: {
      ...workspaceFullScreenCodec(),
      // SP-38: Tasks full screen is the shared chrome-less "Back to chat"
      // route in the SAME tab — never a new browser tab.
      expand: 'route',
    },
  },
  {
    id: 'team',
    title: 'Team',
    content: TeamPanelContent,
    fullScreen: {
      ...workspaceFullScreenCodec(),
      expand: 'route',
    },
  },
  {
    id: 'calendar',
    title: 'Calendar',
    content: CalendarPanelContent,
    fullScreen: {
      ...workspaceFullScreenCodec(),
      expand: 'route',
    },
  },
]

/** Resolve the single production definition for an external transition. */
export function getPanelDefinition(id: PanelId): PanelDefinition | undefined {
  return panels.find((panel) => panel.id === id)
}

/** The ids with a shell registration in the PRODUCTION registry — derived
 * from `panels` itself, never a hand-maintained list, so a registration can
 * never drift from the `?panel=` deep-link contract (§8.2: the valid
 * `?panel=` values are the REGISTERED ids). `mail` is absent until wave 2
 * lands its definition here. */
export const registeredPanelIds: readonly PanelId[] = panels.map((panel) => panel.id)
