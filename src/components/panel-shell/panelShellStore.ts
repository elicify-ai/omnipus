// panelShellStore.ts — wave-1 shim. The single-panel slice this module used
// to own as a wave-0 demo-local store now lives in the REAL ui store
// (src/store/ui.ts::UiStore — side-panel-shell-spec.md §8.1): one
// `activePanel` state ("at most one open", SP-7) replaced the two
// independent slices (`browserPanel` / `libraryPanel`). The wave-0 shell
// components (usePanelShell.ts, SidePanelShell.tsx, the demo stories) keep
// importing `usePanelShellStore` from here, which is now the ui store
// instance — one store, one truth, no parallel slice (SC-005).
//
// `ActivePanel` and `PANEL_WIDTH_UNSET` moved to ./types (the shape-level
// contract module) so both stores and the shell import one definition.

export { PANEL_WIDTH_UNSET } from './types'
export type { ActivePanel } from './types'
export { useUiStore as usePanelShellStore } from '@/store/ui'
