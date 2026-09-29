import { lazy } from 'react'
import {
  confirmDiscardLibraryEdits,
  isLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
import { mailPanelDefinition } from '@/components/workspaces/mail/mailPanelDefinition'
import type { PanelContentProps, PanelDefinition, PanelId } from './types'

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

function LibraryPanelContent(props: PanelContentProps) {
  return <LibraryPanel shellProps={props} />
}

function BrowserPanelContent(props: PanelContentProps) {
  return <BrowserLivePanel shellProps={props} />
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
  // Wave 2 (side-panel-shell-spec.md §10 "Mail adopts the shell" + FR-014):
  // Mail's §8.1 PanelDefinition — the payload (id/title/expandTarget/no
  // beforeLeave) is owned by the mail module (mailPanelDefinition.tsx, pinned
  // by its own pack). One entry, no shell change (SP-4's test).
  mailPanelDefinition,
]

/** Resolve the single production definition for an external transition. */
export function getPanelDefinition(id: PanelId): PanelDefinition | undefined {
  return panels.find((panel) => panel.id === id)
}
