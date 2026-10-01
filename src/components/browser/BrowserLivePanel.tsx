import { useCallback, useEffect, useMemo } from 'react'
import { useUiStore } from '@/store/ui'
import type { BrowserPanelContext, PanelContentProps } from '@/components/panel-shell/types'
import { BrowserLiveView } from './BrowserLiveView'

export interface BrowserLivePanelProps {
  shellProps?: PanelContentProps
}

/** The live-browser content shared by the docked shell and full-screen route. */
export function BrowserLivePanel({ shellProps }: BrowserLivePanelProps = {}) {
  const activePanel = useUiStore((state) => state.activePanel)
  const suppliedContext = shellProps?.context ??
    (activePanel?.id === 'browser' ? activePanel.context : null)
  const browserPanel: BrowserPanelContext | null = useMemo(
    () => suppliedContext?.sessionId && suppliedContext.agentId
      ? { sessionId: suppliedContext.sessionId, agentId: suppliedContext.agentId }
      : null,
    [suppliedContext?.agentId, suppliedContext?.sessionId],
  )

  const getExpandContext = useCallback(
    () => browserPanel ?? { sessionId: '', agentId: '' },
    [browserPanel],
  )
  useEffect(() => {
    const register = shellProps?.registerExpandContext
    if (!register || !browserPanel) return undefined
    register(getExpandContext)
    return () => register(null)
  }, [browserPanel, getExpandContext, shellProps?.registerExpandContext])

  useEffect(() => {
    const onWidthSettle = shellProps?.onWidthSettle
    if (!onWidthSettle || shellProps?.presentation !== 'docked') return undefined
    onWidthSettle(() => window.dispatchEvent(new Event('resize')))
    return () => onWidthSettle(null)
  }, [shellProps?.onWidthSettle, shellProps?.presentation])

  if (!browserPanel) return null
  if (shellProps?.presentation === 'docked' && activePanel?.id !== 'browser') return null

  const fullscreen = shellProps?.presentation === 'fullscreen'
  const Root = shellProps ? 'div' : 'aside'
  return (
    <Root
      data-testid={fullscreen ? 'browser-live-panel-fullscreen' : 'browser-live-panel-docked'}
      aria-label="Live browser panel"
      className="flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-[var(--color-surface-0)]"
    >
      <BrowserLiveView
        key={`${browserPanel.sessionId}:${browserPanel.agentId}`}
        sessionId={browserPanel.sessionId}
        agentId={browserPanel.agentId}
        canAnnotate={!fullscreen}
        fillContainer
      />
    </Root>
  )
}
