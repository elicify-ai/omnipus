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

function LibraryPanelContent({ context, close, expand }: PanelContentProps) {
  return (
    <LibraryExplorer
      initialWorkspaceId={context.workspaceId}
      onClose={close}
      onPopOut={expand}
      className="h-full"
    />
  )
}

function BrowserPanelContent() {
  return <BrowserPanelPlaceholder />
}

export const DEMO_PANELS: PanelDefinition[] = [
  {
    id: 'library',
    title: 'Library',
    content: LibraryPanelContent,
    expandTarget: (context) =>
      `/library?workspace=${context.workspaceId ?? ''}`,
    // CRIT-001: the existing unsaved-edits guard, reused — the shell awaits
    // it before touching store/URL/content.
    beforeLeave: confirmDiscardLibraryEdits,
  },
  { id: 'browser', title: 'Browser', content: BrowserPanelContent, expandTarget: () => '/browser' },
  { id: 'mail', title: 'Mail', content: MailPanelStandin, expandTarget: () => '/mail' },
  { id: 'tasks', title: 'Tasks', content: TasksPanelStandin, expandTarget: () => '/tasks' },
  { id: 'team', title: 'Team', content: TeamPanelStandin, expandTarget: () => '/team' },
  { id: 'calendar', title: 'Calendar', content: CalendarPanelStandin, expandTarget: () => '/calendar' },
]
