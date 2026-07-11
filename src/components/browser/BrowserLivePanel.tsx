// BrowserLivePanel — app-root overlay wrapper around BrowserLiveView
// (ADR-038 D5). A SINGLE global instance mounted in AppShell.tsx, mirroring
// MediaLightbox.tsx: state lives in the `ui` store (`browserPanel`) so any
// "Watch live" affordance anywhere in the tree can open it without prop
// drilling, and the Sheet survives virtualized-list row remounts.
//
// Right-side overlay with `overlay={false}` (chat stays visible behind it),
// mirroring ActivityPanel.tsx's Sheet usage.

import { Sheet, SheetContent } from '@/components/ui/sheet'
import { useUiStore } from '@/store/ui'
import { BrowserLiveView } from './BrowserLiveView'

export function BrowserLivePanel() {
  const browserPanel = useUiStore((s) => s.browserPanel)
  const closeBrowserPanel = useUiStore((s) => s.closeBrowserPanel)

  return (
    <Sheet open={browserPanel !== null} onOpenChange={(open) => !open && closeBrowserPanel()}>
      {browserPanel && (
        <SheetContent side="right" overlay={false} className="w-[70vw] max-w-[56rem] p-0 flex flex-col">
          <BrowserLiveView
            // Keys the mount to the (session, agent) pair so switching targets
            // while the panel is already open (a second "Watch live" click)
            // tears down the old WS connection and starts a fresh one, rather
            // than leaving BrowserLiveView's internal WS effect pinned to the
            // props it captured on first mount.
            key={`${browserPanel.sessionId}:${browserPanel.agentId}`}
            sessionId={browserPanel.sessionId}
            agentId={browserPanel.agentId}
            onClose={closeBrowserPanel}
            onPopOut={() => {
              const params = new URLSearchParams({
                session: browserPanel.sessionId,
                agent: browserPanel.agentId,
              })
              window.open(`/browser-live?${params.toString()}`, '_blank', 'noopener,noreferrer')
            }}
          />
        </SheetContent>
      )}
    </Sheet>
  )
}
