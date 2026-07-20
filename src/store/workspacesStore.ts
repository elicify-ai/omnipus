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
   * Shared Board⇄Graph plan scope (Hierarchical Drill-Down board). `null` =
   * TOP LEVEL: the Board shows plan cards + loose tasks together and the Graph
   * shows the whole workspace. A plan id = DRILLED: the Board shows that plan's
   * own task board and the Graph shows that plan's DAG. Set by a plan card's
   * drill-in (title / ⤢) and its ⑂ view-graph button (which also navigates to
   * the Graph tab), plus the Graph tab's "By plan" selector.
   */
  activePlanId: string | null
  setActivePlanId: (id: string | null) => void
  /**
   * The currently-active tag filter (ADR-049). null = no tag filter. See
   * `PLAN_FILTER_UNTAGGED` (`src/lib/planFilter.ts`) for the "no tags at
   * all" sentinel.
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
