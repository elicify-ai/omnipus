// The originating app owns one dock/popout pair. Other app documents do not
// restore it, and a child reload does not count as closing the popout.
import { useEffect, useRef } from 'react'
import { flushSync } from 'react-dom'
import { useUiStore } from '@/store/ui'
import { watchPopoutClosed } from '@/lib/browserLiveHandoff'
import { BrowserLiveView } from './BrowserLiveView'

type OwnedPopout = { window: Window; sessionId: string; agentId: string; stop: () => void }

export function BrowserLivePanel() {
  const browserPanel = useUiStore((s) => s.browserPanel)
  const closeBrowserPanel = useUiStore((s) => s.closeBrowserPanel)
  const ownedPopout = useRef<OwnedPopout | null>(null)

  useEffect(() => {
    // Subscribe synchronously so Open browser cannot briefly mount another
    // viewer before an effect redirects it to the already-owned popout.
    const unsubscribe = useUiStore.subscribe((state) => {
      const owned = ownedPopout.current
      if (!state.browserPanel || !owned) return
      if (owned.window.closed) {
        ownedPopout.current = null
        owned.stop()
      } else {
        state.closeBrowserPanel()
        owned.window.focus()
      }
    })
    const closeOwned = () => {
      const owned = ownedPopout.current
      ownedPopout.current = null
      if (owned) { owned.stop(); owned.window.close() }
    }
    window.addEventListener('pagehide', closeOwned)
    return () => { unsubscribe(); window.removeEventListener('pagehide', closeOwned); closeOwned() }
  }, [])

  const popoutRoute = window.location.hash.split('?')[0] === '#/browser-live' || window.location.pathname === '/browser-live'
  if (!browserPanel || popoutRoute || (ownedPopout.current && !ownedPopout.current.window.closed)) return null

  const handlePopOut = () => {
    // A trusted blank tab preserves the synchronous user gesture and gives
    // the owner a reload-safe handle. Sever the child's opener immediately;
    // remote web content is still only video inside our same-origin route.
    let popup: Window | null
    try {
      popup = window.open('about:blank', '_blank')
    } catch {
      useUiStore.getState().addToast({ message: 'The popout could not open. The browser remains here.', variant: 'error' })
      return
    }
    if (!popup) {
      useUiStore.getState().addToast({ message: 'The popout was blocked. Allow popups and try again.', variant: 'error' })
      return
    }
    const owned: OwnedPopout = { window: popup, ...browserPanel, stop: () => {} }
    try {
      popup.opener = null
      ownedPopout.current = owned
      // Commit unmount and its synchronous socket/peer cleanup BEFORE the
      // child navigates to a route that can create its own live viewer.
      flushSync(closeBrowserPanel)
      owned.stop = watchPopoutClosed(popup, () => {
        if (ownedPopout.current !== owned) return
        ownedPopout.current = null
        if (useUiStore.getState().browserPanel === null) {
          useUiStore.getState().openBrowserPanel(owned.sessionId, owned.agentId)
        }
      })
      const params = new URLSearchParams({ session: owned.sessionId, agent: owned.agentId })
      popup.location.replace(`/#/browser-live?${params.toString()}`)
      document.querySelector<HTMLTextAreaElement>('[data-testid="chat-input"]')?.focus()
    } catch {
      owned.stop()
      ownedPopout.current = null
      popup.close()
      useUiStore.getState().openBrowserPanel(owned.sessionId, owned.agentId)
      useUiStore.getState().addToast({ message: 'The popout could not open. The browser remains here.', variant: 'error' })
    }
  }

  return (
    <aside
      data-testid="browser-live-panel-docked"
      aria-label="Live browser panel"
      className="flex h-full w-full min-w-0 sm:w-[45%] sm:min-w-[320px] sm:max-w-[720px] flex-shrink-0 flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-0)]"
    >
      <BrowserLiveView
        key={`${browserPanel.sessionId}:${browserPanel.agentId}`}
        sessionId={browserPanel.sessionId}
        agentId={browserPanel.agentId}
        onClose={closeBrowserPanel}
        canAnnotate
        fillContainer
        onPopOut={handlePopOut}
      />
    </aside>
  )
}
