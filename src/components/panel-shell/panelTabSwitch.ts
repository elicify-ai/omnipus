import {
  armPanelFocusFallback,
  cancelPanelFocusFallback,
  switchToPanelTab,
  type PanelIdentity,
} from '@/lib/panelTabPresence'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from './leaveGate'

export function showPanelTabFocusDegraded(panelLabel: string): void {
  useUiStore.getState().addToast({
    message: `${panelLabel} is already open in another tab, but it could not be focused automatically. Switch to that tab manually.`,
    variant: 'default',
    duration: 10_000,
  })
}

/** SP-18: switching is exclusive; local fallback is permitted only after the target disappears. */
export function showPanelTabSwitch(
  identity: PanelIdentity,
  panelLabel: string,
  openWhenUnavailable: () => void,
): void {
  useUiStore.getState().addToast({
    message: `${panelLabel} is already open in another tab — switch.`,
    variant: 'default',
    duration: 10_000,
    action: {
      label: 'Switch',
      onClick: () => {
        const panelIntentRevision = useUiStore.getState().panelIntentRevision
        const isPanelIntentCurrent = (revision: number) =>
          useUiStore.getState().panelIntentRevision === revision
        const openIfCurrent = () => {
          const state = useUiStore.getState()
          if (!isPanelIntentCurrent(panelIntentRevision)) return
          leaveGateThen(state.activePanel?.id ?? null, () => {
            if (isPanelIntentCurrent(panelIntentRevision)) openWhenUnavailable()
          })
        }
        armPanelFocusFallback(
          identity,
          panelIntentRevision,
          isPanelIntentCurrent,
          openIfCurrent,
          () => showPanelTabFocusDegraded(panelLabel),
        )
        const result = switchToPanelTab(identity)
        if (result === 'requested') return
        cancelPanelFocusFallback(identity)
        if (result === 'absent') {
          openIfCurrent()
        } else if (result === 'failed') {
          showPanelTabFocusDegraded(panelLabel)
        }
      },
    },
  })
}
