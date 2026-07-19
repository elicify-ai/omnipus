import { create } from 'zustand'

/**
 * Board altitude — controls whether the Board shows only top-level tasks
 * (default, keeps the board meaningful) or expands nested children inline.
 * Persisted per-session in Zustand; resets to 'top-level' on page reload.
 */
export type BoardAltitude = 'top-level' | 'show-all'

interface WorkspacesState {
  /** The currently-selected workspace ID for filtering the task board. null = "All workspaces". */
  activeWorkspaceId: string | null
  setActiveWorkspaceId: (id: string | null) => void
  /**
   * The currently-active Plan filter ID (ADR-049 — replaces the removed
   * milestone filter). null = "All". See `PLAN_FILTER_ALL`/`PLAN_FILTER_UNTAGGED`
   * sentinels in `PlanFilterBar.tsx` (SD-C2).
   */
  activePlanId: string | null
  setActivePlanId: (id: string | null) => void
  /**
   * The currently-active tag filter (ADR-049). null = no tag filter. Composes
   * (AND) with `activePlanId` — see `PlanFilterBar`'s `PLAN_FILTER_UNTAGGED`
   * sentinel for "no tags at all".
   */
  activeTagFilter: string | null
  setActiveTagFilter: (tag: string | null) => void
  /**
   * Board depth/altitude toggle. 'top-level' = root tasks only (children
   * nested-collapsed under their parent). 'show-all' = children expanded
   * inline under their parent card. Visibility is a property of the VIEW.
   */
  boardAltitude: BoardAltitude
  setBoardAltitude: (altitude: BoardAltitude) => void
}

export const useWorkspacesStore = create<WorkspacesState>((set) => ({
  activeWorkspaceId: null,
  setActiveWorkspaceId: (id) => set({ activeWorkspaceId: id }),
  activePlanId: null,
  setActivePlanId: (id) => set({ activePlanId: id }),
  activeTagFilter: null,
  setActiveTagFilter: (tag) => set({ activeTagFilter: tag }),
  boardAltitude: 'top-level',
  setBoardAltitude: (altitude) => set({ boardAltitude: altitude }),
}))
