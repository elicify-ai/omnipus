import { lazy } from 'react'
import { confirmDiscardLibraryEdits } from '@/components/library/preview/unsavedGuard'
import type { PanelContentProps, PanelDefinition } from './types'

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
