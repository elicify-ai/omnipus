// demoPanelRegistry.tsx — the wave-0 registry: six PanelDefinitions in the
// §8.1 contract shape. Library is the REAL LibraryExplorer on fixture data
// with CRIT-001's beforeLeave wired to the existing unsaved-edits guard
// (reused, not rebuilt); Browser is the static placeholder (SP-19); Mail,
// Tasks, Team and Calendar are throwaway stand-ins. Adding a panel is one
// entry here — the shell changes nothing (SP-4's registration test).

import { LibraryExplorer } from '@/components/library/LibraryExplorer'
import { confirmDiscardLibraryEdits } from '@/components/library/preview/unsavedGuard'
import type { PanelContentProps, PanelDefinition } from '../types'
import { BrowserPanelPlaceholder } from './BrowserPanelPlaceholder'
import { CalendarPanelStandin, MailPanelStandin, TasksPanelStandin, TeamPanelStandin } from './standins'

const workspaceFullScreen: PanelDefinition['fullScreen'] = {
  toSearch: ({ workspaceId }) => {
    const search: Record<string, string> = {}
    if (workspaceId) search.workspace = workspaceId
    return search
  },
  fromSearch: (search) => typeof search.workspace === 'string'
    ? { workspaceId: search.workspace }
    : {},
}

function LibraryPanelContent({ context }: PanelContentProps) {
  return <LibraryExplorer initialWorkspaceId={context.workspaceId} className="h-full" />
}

function BrowserPanelContent() {
  return <BrowserPanelPlaceholder />
}

export const DEMO_PANELS: PanelDefinition[] = [
  {
    id: 'library',
    title: 'Library',
    content: LibraryPanelContent,
    fullScreen: workspaceFullScreen,
    // CRIT-001: the existing unsaved-edits guard, reused — the shell awaits
    // it before touching store/URL/content.
    beforeLeave: confirmDiscardLibraryEdits,
  },
  {
    id: 'browser',
    title: 'Browser',
    content: BrowserPanelContent,
    fullScreen: {
      toSearch: ({ sessionId, agentId }) => {
        const search: Record<string, string> = {}
        if (sessionId && agentId) {
          search.session = sessionId
          search.agent = agentId
        }
        return search
      },
      fromSearch: (search) => typeof search.session === 'string' && typeof search.agent === 'string'
        ? { sessionId: search.session, agentId: search.agent }
        : null,
    },
  },
  {
    id: 'mail',
    title: 'Mail',
    content: MailPanelStandin,
    fullScreen: workspaceFullScreen,
  },
  {
    id: 'tasks',
    title: 'Tasks',
    content: TasksPanelStandin,
    fullScreen: workspaceFullScreen,
  },
  {
    id: 'team',
    title: 'Team',
    content: TeamPanelStandin,
    fullScreen: workspaceFullScreen,
  },
  {
    id: 'calendar',
    title: 'Calendar',
    content: CalendarPanelStandin,
    fullScreen: workspaceFullScreen,
  },
]
