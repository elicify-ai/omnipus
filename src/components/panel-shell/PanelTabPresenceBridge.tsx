import { useEffect } from 'react'
import {
  resolveExistingPanelTab,
  startPanelTabPresenceMonitor,
  type PanelIdentity,
} from '@/lib/panelTabPresence'
import { startPanelPopoutLifecycleOwner } from '@/lib/panelPopoutLifecycle'
import { useUiStore } from '@/store/ui'
import type { ActivePanel } from './types'
import { showPanelTabFocusDegraded, showPanelTabFocused, showPanelTabSwitch } from './panelTabSwitch'

function panelIdentity(activePanel: ActivePanel): PanelIdentity | null {
  if (activePanel.id === 'browser') {
    const { sessionId, agentId } = activePanel.context
    if (!sessionId || !agentId) return null
    return { panelId: 'browser', sessionId, agentId }
  }
  return {
    panelId: activePanel.id,
    workspaceId: activePanel.context.workspaceId,
  }
}

function panelLabel(panelId: string): string {
  return panelId.charAt(0).toUpperCase() + panelId.slice(1)
}

/** Applies SP-18/SP-30 before a store-open can render a duplicate docked panel. */
export function PanelTabPresenceBridge() {
  useEffect(() => {
    const stopMonitor = startPanelTabPresenceMonitor()
    const stopLifecycleOwner = startPanelPopoutLifecycleOwner()
    const unsubscribe = useUiStore.subscribe((state, previous) => {
      const activePanel = state.activePanel
      if (activePanel === null || activePanel === previous.activePanel) return
      const identity = panelIdentity(activePanel)
      if (!identity) return
      const existing = resolveExistingPanelTab(identity)
      if (!existing) return

      state.closePanel()
      const label = panelLabel(activePanel.id)
      if (existing === 'focused') {
        showPanelTabFocused(label)
        return
      }
      const openWhenUnavailable = () => {
        if (activePanel.id === 'browser') {
          useUiStore.getState().openPanel('browser', activePanel.context)
        } else {
          useUiStore.getState().openPanel(activePanel.id, activePanel.context)
        }
      }
      if (existing === 'focus-failed') {
        showPanelTabFocusDegraded(label)
      } else {
        showPanelTabSwitch(identity, label, openWhenUnavailable)
      }
    })
    return () => {
      unsubscribe()
      stopMonitor()
      stopLifecycleOwner()
    }
  }, [])

  return null
}
