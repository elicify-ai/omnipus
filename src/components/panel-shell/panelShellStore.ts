// panelShellStore.ts — wave-1 shim. The single-panel slice this module used
// to own as a wave-0 demo-local store now lives in the REAL ui store
// (src/store/ui.ts::UiStore — side-panel-shell-spec.md §8.1): one
// `activePanel` state ("at most one open", SP-7) replaced the two
// independent slices (`browserPanel` / `libraryPanel`). The wave-0 shell
// components (usePanelShell.ts, SidePanelShell.tsx, the demo stories) keep
// importing `usePanelShellStore` from here, which is now the ui store
// instance — one store, one truth, no parallel slice (SC-005).
//
// `ActivePanel` lives in ./types so the store and shell import one definition.

export type { ActivePanel } from './types'

import type { StoreApi, UseBoundStore } from 'zustand'
import type { PanelContext, PanelId } from './types'
import { useUiStore } from '@/store/ui'

// Compatibility boundary for the wave-0 shell harnesses, whose helpers pass
// a union PanelId plus a record-shaped context. The production store keeps
// the discriminated OpenPanel signature; only this legacy alias is broad.
type PanelShellState = Omit<ReturnType<typeof useUiStore.getState>, 'openPanel'> & {
  openPanel: (id: PanelId, context?: PanelContext | Record<string, unknown>) => void
}

export const usePanelShellStore = useUiStore as unknown as UseBoundStore<StoreApi<PanelShellState>>
