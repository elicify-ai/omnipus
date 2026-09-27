// The originating app owns one dock/popout pair. Other app documents do not
// restore it, and a child reload does not count as closing the popout.
import { useEffect, useRef } from 'react'
import { flushSync } from 'react-dom'
import { useUiStore } from '@/store/ui'
import { watchPopoutClosed } from '@/lib/browserLiveHandoff'
import { panelIdentityKey, resolvePanelOpen } from '@/lib/panelTabPresence'
import { BrowserLiveView } from './BrowserLiveView'

type OwnedPopout = {
  window: Window
  identityKey: string
  sessionId: string
  agentId: string
  stop: () => void
}

const browserPopoutHandles = new Map<string, Window>()

export function BrowserLivePanel() {
  const activePanel = useUiStore((s) => s.activePanel)
  const closePanel = useUiStore((s) => s.closePanel)
  const ownedPopout = useRef<OwnedPopout | null>(null)

  useEffect(() => {
    // Subscribe synchronously so Open browser cannot briefly mount another
    // viewer before an effect redirects it to the already-owned popout.
    const unsubscribe = useUiStore.subscribe((state) => {
      if (state.activePanel?.id !== 'browser') return
      const { sessionId, agentId } = state.activePanel.context
      if (!sessionId || !agentId) return
      const identityKey = panelIdentityKey({ panelId: 'browser', sessionId, agentId })
      const handle = browserPopoutHandles.get(identityKey)
      if (!handle) return
      if (handle.closed) {
        browserPopoutHandles.delete(identityKey)
        if (ownedPopout.current?.identityKey === identityKey) {
          ownedPopout.current.stop()
          ownedPopout.current = null
        }
        return
      }
      state.closePanel()
      try {
        handle.focus()
      } catch {
        // Window focus is best-effort; do not duplicate a live viewer.
      }
    })
    const closeOwned = () => {
      const owned = ownedPopout.current
      ownedPopout.current = null
      if (owned) {
        browserPopoutHandles.delete(owned.identityKey)
        owned.stop()
        owned.window.close()
      }
    }
    window.addEventListener('pagehide', closeOwned)
    return () => { unsubscribe(); window.removeEventListener('pagehide', closeOwned); closeOwned() }
  }, [])

  const popoutRoute = window.location.hash.split('?')[0] === '#/browser-live' || window.location.pathname === '/browser-live'
  // Narrow to the Browser's own context shape: sessionId/agentId are always
  // supplied by every openPanel('browser', …) call site; a context missing
  // them would render nothing attachable, so it is treated as closed.
  const browserCtx = activePanel?.id === 'browser' ? activePanel.context : null
  const browserPanel =
    browserCtx && browserCtx.sessionId && browserCtx.agentId
      ? { sessionId: browserCtx.sessionId, agentId: browserCtx.agentId }
      : null
  if (!browserPanel || popoutRoute) return null
  const browserIdentityKey = panelIdentityKey({
    panelId: 'browser',
    sessionId: browserPanel.sessionId,
    agentId: browserPanel.agentId,
  })
  const existingBrowserPopout = browserPopoutHandles.get(browserIdentityKey)
  if (existingBrowserPopout && !existingBrowserPopout.closed) return null

  const handlePopOut = () => {
    // A trusted blank tab preserves the synchronous user gesture and gives
    // the owner a reload-safe handle. Sever the child's opener immediately;
    // remote web content is still only video inside our same-origin route.
    const identity = {
      panelId: 'browser',
      sessionId: browserPanel.sessionId,
      agentId: browserPanel.agentId,
    }
    const identityKey = panelIdentityKey(identity)
    let popupCreationThrew = false
    const outcome = resolvePanelOpen({
      identity,
      handles: browserPopoutHandles,
      presence: [],
      open: () => {
        try {
          return window.open('about:blank', '_blank')
        } catch (error) {
          popupCreationThrew = true
          throw error
        }
      },
    })
    if (outcome.kind === 'blocked') {
      useUiStore.getState().addToast({
        message: popupCreationThrew
          ? 'The popout could not open. The browser remains here.'
          : 'The popout was blocked. Allow popups and try again.',
        variant: 'error',
      })
      return
    }
    if (outcome.kind === 'focused' || outcome.kind === 'affordance') {
      closePanel()
      return
    }

    const popup = browserPopoutHandles.get(identityKey)
    if (!popup) return
    const owned: OwnedPopout = { window: popup, identityKey, ...browserPanel, stop: () => {} }
    try {
      popup.opener = null
      ownedPopout.current = owned
      // Commit unmount and its synchronous socket/peer cleanup BEFORE the
      // child navigates to a route that can create its own live viewer.
      flushSync(closePanel)
      owned.stop = watchPopoutClosed(popup, () => {
        if (ownedPopout.current !== owned) return
        ownedPopout.current = null
        browserPopoutHandles.delete(owned.identityKey)
        if (useUiStore.getState().activePanel === null) {
          useUiStore.getState().openPanel('browser', { sessionId: owned.sessionId, agentId: owned.agentId })
        }
      })
      const params = new URLSearchParams({ session: owned.sessionId, agent: owned.agentId })
      popup.location.replace(`/#/browser-live?${params.toString()}`)
      document.querySelector<HTMLTextAreaElement>('[data-testid="chat-input"]')?.focus()
    } catch {
      owned.stop()
      ownedPopout.current = null
      browserPopoutHandles.delete(owned.identityKey)
      popup.close()
      useUiStore.getState().openPanel('browser', { sessionId: owned.sessionId, agentId: owned.agentId })
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
        onClose={closePanel}
        canAnnotate
        fillContainer
        onPopOut={handlePopOut}
      />
    </aside>
  )
}
