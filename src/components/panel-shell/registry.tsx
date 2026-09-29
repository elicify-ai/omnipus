import { lazy } from 'react'
import {
  confirmDiscardLibraryEdits,
  isLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
import { mailPanelDefinition } from '@/components/workspaces/mail/mailPanelDefinition'
import type { PanelContentProps, PanelDefinition, PanelId } from './types'

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
    expandTarget: ({ workspaceId }) =>
      `/#/library${workspaceId ? `?workspace=${encodeURIComponent(workspaceId)}` : ''}`,
    beforeLeave: confirmDiscardLibraryEdits,
    beforeLeaveRequired: isLibraryEditorDirty,
  },
  {
    id: 'browser',
    title: 'Browser',
    content: BrowserPanelContent,
    expandTarget: ({ sessionId, agentId }) => {
      const search = new URLSearchParams()
      if (sessionId) search.set('session', sessionId)
      if (agentId) search.set('agent', agentId)
      return `/#/browser-live${search.size > 0 ? `?${search.toString()}` : ''}`
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
