// /library — fullscreen pop-out target for the Library panel (library-spec.md
// D-4). Opened via `window.open('/#/library?workspace=..')` from
// LibraryPanel.tsx's pop-out button. Renders the same LibraryExplorer core
// the docked panel uses, filling the AppShell content area.
//
// Nested under `/_app` so it reuses the existing onboarding/auth guard
// (`_app.tsx`'s beforeLoad). Auth rides the same-origin `omnipus-session`
// HttpOnly cookie (ADR-044), which a same-origin `window.open`'d tab inherits
// automatically — no token hand-off needed.
//
// `workspace` omitted → the virtual root (mirrors LibraryExplorer's own
// `initialWorkspaceId` contract exactly — undefined means "start at the
// virtual root", not an error state, unlike /browser-live's session/agent
// params which ARE required).

import { useEffect } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { LibraryExplorer } from '@/components/library/LibraryExplorer'
import { announceLibraryPopoutClosed } from '@/lib/libraryHandoff'

const librarySearchSchema = z.object({
  workspace: z.string().min(1).optional(),
})

export const Route = createFileRoute('/_app/library')({
  validateSearch: librarySearchSchema,
  component: LibraryRoute,
})

function LibraryRoute() {
  const { workspace } = Route.useSearch()

  // Tell the main app's docked panel (LibraryPanel.tsx) this pop-out went
  // away, so it can re-dock itself IF nothing is currently docked (see
  // libraryHandoff.ts's doc comment — this is a safety net, not a hand-over:
  // unlike /browser-live, the docked panel is never force-closed just
  // because this pop-out opened). `pagehide` fires for every teardown path
  // (native tab-close/Cmd+W, navigate-away, or this route's own unmount),
  // so this one listener covers all of them.
  useEffect(() => {
    const handlePageHide = () => announceLibraryPopoutClosed(workspace)
    window.addEventListener('pagehide', handlePageHide)
    return () => window.removeEventListener('pagehide', handlePageHide)
  }, [workspace])

  return (
    <LibraryExplorer
      key={workspace ?? 'root'}
      initialWorkspaceId={workspace}
      // onClose omitted: closing "the Library" from a standalone tab means
      // closing the tab itself, not returning to some other in-app view —
      // there's no Close-button affordance that makes sense here (mirrors
      // /browser-live's route only rendering a Close button because it
      // deliberately hands control back; the Library route has nothing
      // analogous to hand back).
      className="absolute inset-0"
    />
  )
}
