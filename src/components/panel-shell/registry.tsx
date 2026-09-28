import { lazy } from 'react'
import {
  confirmDiscardLibraryEdits,
  isLibraryEditorDirty,
} from '@/components/library/preview/unsavedGuard'
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
]

/** Resolve the single production definition for an external transition. */
export function getPanelDefinition(id: PanelId): PanelDefinition | undefined {
  return panels.find((panel) => panel.id === id)
}
