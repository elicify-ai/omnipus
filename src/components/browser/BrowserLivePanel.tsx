// The originating app owns one dock/popout pair. Other app documents do not
// restore it, and a child reload does not count as closing the popout.
import { useCallback, useEffect, useRef } from 'react'
import { flushSync } from 'react-dom'
import { useUiStore } from '@/store/ui'
import type { PanelContentProps } from '@/components/panel-shell/types'
import {
  armPanelFocusFallback,
  focusPanelTab,
  resolveExistingPanelTab,
  resolveRegisteredPanelOpen,
  type PanelIdentity,
} from '@/lib/panelTabPresence'
import {
  discardPanelPopout,
  registerPanelPopout,
  releasePanelPopoutWithoutAppOwner,
} from '@/lib/panelPopoutLifecycle'
import { BrowserLiveView } from './BrowserLiveView'

export interface BrowserLivePanelProps {
  shellProps?: PanelContentProps
}

export function BrowserLivePanel({ shellProps }: BrowserLivePanelProps = {}) {
  const activePanel = useUiStore((s) => s.activePanel)
  const closePanel = useUiStore((s) => s.closePanel)
  const isolatedCleanupRef = useRef<{
    identity: PanelIdentity
    handle: Window
  } | null>(null)

  useEffect(() => {
    // The app-level bridge owns this in production. Keep the same synchronous
    // protection for isolated mounts (component tests and embedders).
    const unsubscribe = useUiStore.subscribe((state) => {
      if (state.activePanel?.id !== 'browser') return
      const { sessionId, agentId } = state.activePanel.context
      if (!sessionId || !agentId) return
      const identity = {
        panelId: 'browser',
        sessionId,
        agentId,
      } as const
      if (resolveExistingPanelTab(identity) !== 'focused') return
      state.closePanel()
    })
    return () => {
      unsubscribe()
      const owned = isolatedCleanupRef.current
      if (owned) releasePanelPopoutWithoutAppOwner(owned.identity, owned.handle)
      isolatedCleanupRef.current = null
    }
  }, [])

  const popoutRoute =
    window.location.hash.split('?')[0] === '#/browser-live' || window.location.pathname === '/browser-live'
  // Narrow to the Browser's own context shape: sessionId/agentId are always
  // supplied by every openPanel('browser', …) call site; a context missing
  // them would render nothing attachable, so it is treated as closed.
  const browserCtx = activePanel?.id === 'browser' ? activePanel.context : (shellProps?.context ?? null)
  const browserPanel =
    browserCtx && browserCtx.sessionId && browserCtx.agentId
      ? { sessionId: browserCtx.sessionId, agentId: browserCtx.agentId }
      : null
  const handlePopOut = useCallback((): boolean => {
    if (!browserPanel) return false
    // A trusted blank tab preserves the synchronous user gesture and gives
    // the owner a reload-safe handle. Sever the child's opener immediately;
    // remote web content is still only video inside our same-origin route.
    const identity: PanelIdentity = {
      panelId: 'browser',
      sessionId: browserPanel.sessionId,
      agentId: browserPanel.agentId,
    }
    let popupCreationThrew = false
    let openedPopup: Window | null = null
    const outcome = resolveRegisteredPanelOpen({
      identity,
      open: () => {
        try {
          openedPopup = window.open('about:blank', '_blank')
          return openedPopup
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
      return false
    }
    if (outcome.kind === 'affordance') {
      const openHere = () => {
        armPanelFocusFallback(identity)
        useUiStore.getState().openPanel('browser', {
          sessionId: browserPanel.sessionId,
          agentId: browserPanel.agentId,
        })
      }
      useUiStore.getState().addToast({
        message: 'The Browser is already open in another tab — switch.',
        variant: 'default',
        duration: 10_000,
        action: {
          label: 'Switch',
          onClick: () => {
            if (!focusPanelTab(identity)) openHere()
          },
        },
        secondaryAction: { label: 'Open here', onClick: openHere },
      })
      return true
    }
    if (outcome.kind === 'focused') {
      return true
    }

    const popup = openedPopup as Window | null
    if (!popup) return false
    try {
      popup.opener = null
      isolatedCleanupRef.current = { identity, handle: popup }
      registerPanelPopout({
        identity,
        handle: popup,
        onClosed: () => {
          isolatedCleanupRef.current = null
          if (useUiStore.getState().activePanel === null) {
            useUiStore.getState().openPanel('browser', {
              sessionId: browserPanel.sessionId,
              agentId: browserPanel.agentId,
            })
          }
        },
      })
      // Commit unmount and its synchronous socket/peer cleanup BEFORE the
      // child navigates to a route that can create its own live viewer.
      flushSync(closePanel)
      const params = new URLSearchParams({
        session: browserPanel.sessionId,
        agent: browserPanel.agentId,
      })
      popup.location.replace(`/#/browser-live?${params.toString()}`)
      document.querySelector<HTMLTextAreaElement>('[data-testid="chat-input"]')?.focus()
      return true
    } catch (error) {
      console.error('Browser pop-out handover failed.', error)
      discardPanelPopout(identity, popup)
      isolatedCleanupRef.current = null
      popup.close()
      useUiStore.getState().openPanel('browser', {
        sessionId: browserPanel.sessionId,
        agentId: browserPanel.agentId,
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
    popoutRoute
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
