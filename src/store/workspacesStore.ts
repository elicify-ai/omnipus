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
   * Shared Board⇄Graph plan FILTER (ADR-051). `null` = no plan filter — the
   * "All tasks" tile is active and the Board/Graph show the whole workspace.
   * A plan id = the Board and Graph are both scoped to that plan's tasks. Set
   * by `PlansFilterBand`'s tile selection; the Graph reads this directly so it
   * stays scope-synced with the Board.
   */
  activePlanId: string | null
  setActivePlanId: (id: string | null) => void
  /**
   * The currently-selected tag filters (ADR-051 — the toolbar tag filter is a
   * multiselect). Empty = no tag filter; a task matches if it has ANY selected
   * tag. See `PLAN_FILTER_UNTAGGED` (`src/lib/planFilter.ts`) for the "no tags
   * at all" sentinel.
   */
  activeTags: string[]
  setActiveTags: (tags: string[]) => void
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
  activeTags: [],
  setActiveTags: (tags) => set({ activeTags: tags }),
  boardAltitude: 'top-level',
  setBoardAltitude: (altitude) => set({ boardAltitude: altitude }),
}))
