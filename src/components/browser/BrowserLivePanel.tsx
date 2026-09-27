// The originating app owns one dock/popout pair. Other app documents do not
// restore it, and a child reload does not count as closing the popout.
import { useCallback, useEffect, useRef } from 'react'
import { flushSync } from 'react-dom'
import { useUiStore } from '@/store/ui'
import { watchPopoutClosed } from '@/lib/browserLiveHandoff'
import type { PanelContentProps } from '@/components/panel-shell/types'
import { BrowserLiveView } from './BrowserLiveView'

type OwnedPopout = {
  window: Window
  sessionId: string
  agentId: string
  stop: () => void
}

export interface BrowserLivePanelProps {
  shellProps?: PanelContentProps
}

export function BrowserLivePanel({ shellProps }: BrowserLivePanelProps = {}) {
  const activePanel = useUiStore((s) => s.activePanel)
  const closePanel = useUiStore((s) => s.closePanel)
  const ownedPopout = useRef<OwnedPopout | null>(null)

  useEffect(() => {
    // Subscribe synchronously so Open browser cannot briefly mount another
    // viewer before an effect redirects it to the already-owned popout.
    const unsubscribe = useUiStore.subscribe((state) => {
      const owned = ownedPopout.current
      if (!state.activePanel || state.activePanel.id !== 'browser' || !owned) return
      if (owned.window.closed) {
        ownedPopout.current = null
        owned.stop()
      } else {
        state.closePanel()
        owned.window.focus()
      }
    })
    const closeOwned = () => {
      const owned = ownedPopout.current
      ownedPopout.current = null
      if (owned) {
        owned.stop()
        owned.window.close()
      }
    }
    window.addEventListener('pagehide', closeOwned)
    return () => {
      unsubscribe()
      window.removeEventListener('pagehide', closeOwned)
      closeOwned()
    }
  }, [])

  const popoutRoute =
    window.location.hash.split('?')[0] === '#/browser-live' || window.location.pathname === '/browser-live'
  // Narrow to the Browser's own context shape: sessionId/agentId are always
  // supplied by every openPanel('browser', …) call site; a context missing
  // them would render nothing attachable, so it is treated as closed.
  const browserCtx = shellProps?.context ?? (activePanel?.id === 'browser' ? activePanel.context : null)
  const browserPanel =
    browserCtx && browserCtx.sessionId && browserCtx.agentId
      ? { sessionId: browserCtx.sessionId, agentId: browserCtx.agentId }
      : null
  const handlePopOut = useCallback((): boolean => {
    if (!browserPanel) return false
    // A trusted blank tab preserves the synchronous user gesture and gives
    // the owner a reload-safe handle. Sever the child's opener immediately;
    // remote web content is still only video inside our same-origin route.
    let popup: Window | null
    try {
      popup = window.open('about:blank', '_blank')
    } catch {
      useUiStore.getState().addToast({
        message: 'The popout could not open. The browser remains here.',
        variant: 'error',
      })
      return false
    }
    if (!popup) {
      useUiStore.getState().addToast({
        message: 'The popout was blocked. Allow popups and try again.',
        variant: 'error',
      })
      return false
    }
    const owned: OwnedPopout = {
      window: popup,
      ...browserPanel,
      stop: () => {},
    }
    try {
      popup.opener = null
      ownedPopout.current = owned
      // Commit unmount and its synchronous socket/peer cleanup BEFORE the
      // child navigates to a route that can create its own live viewer.
      flushSync(closePanel)
      owned.stop = watchPopoutClosed(popup, () => {
        if (ownedPopout.current !== owned) return
        ownedPopout.current = null
        if (useUiStore.getState().activePanel === null) {
          useUiStore.getState().openPanel('browser', {
            sessionId: owned.sessionId,
            agentId: owned.agentId,
          })
        }
      })
      const params = new URLSearchParams({
        session: owned.sessionId,
        agent: owned.agentId,
      })
      popup.location.replace(`/#/browser-live?${params.toString()}`)
      document.querySelector<HTMLTextAreaElement>('[data-testid="chat-input"]')?.focus()
      return true
    } catch {
      owned.stop()
      ownedPopout.current = null
      popup.close()
      useUiStore.getState().openPanel('browser', {
        sessionId: owned.sessionId,
        agentId: owned.agentId,
      })
      useUiStore.getState().addToast({
        message: 'The popout could not open. The browser remains here.',
        variant: 'error',
      })
      return false
    }
  }, [browserPanel, closePanel])

  useEffect(() => {
    if (!shellProps) return
    shellProps.registerExpand(handlePopOut)
    return () => shellProps.registerExpand(null)
  }, [handlePopOut, shellProps])

  useEffect(() => {
    if (!shellProps) return
    shellProps.onWidthSettle(() => {
      window.dispatchEvent(new Event('resize'))
    })
    return () => shellProps.onWidthSettle(null)
  }, [shellProps])

  if (
    !browserPanel ||
    (shellProps && activePanel?.id !== 'browser') ||
    popoutRoute ||
    (ownedPopout.current && !ownedPopout.current.window.closed)
  )
    return null

  const Root = shellProps ? 'div' : 'aside'

  return (
    <Root
      data-testid="browser-live-panel-docked"
      aria-label="Live browser panel"
      className="flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-[var(--color-surface-0)]"
    >
      <BrowserLiveView
        key={`${browserPanel.sessionId}:${browserPanel.agentId}`}
        sessionId={browserPanel.sessionId}
        agentId={browserPanel.agentId}
        canAnnotate
        fillContainer
      />
    </Root>
  )
}
