import {
  armPanelFocusFallback,
  cancelPanelFocusFallback,
  switchToPanelTab,
  type PanelIdentity,
} from '@/lib/panelTabPresence'
import { useUiStore } from '@/store/ui'

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
        armPanelFocusFallback(
          identity,
          openWhenUnavailable,
          () => showPanelTabFocusDegraded(panelLabel),
        )
        const result = switchToPanelTab(identity)
        if (result === 'requested') return
        cancelPanelFocusFallback(identity)
        if (result === 'absent') {
          openWhenUnavailable()
        } else if (result === 'failed') {
          showPanelTabFocusDegraded(panelLabel)
        }
      },
    },
  })
}
