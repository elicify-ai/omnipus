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
   * The Plan a view should scope to (Swimlane board redesign — this field's
   * job changed from "Board filter" to "which plan the Graph tab focuses",
   * set by a lane header's ⑂ view-graph button via `setActivePlanId` before
   * navigating to the Graph tab). null = no scoped plan. The Board itself no
   * longer filters by this field — it groups ALL plans into swimlane bands
   * instead (see BoardView.tsx).
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
  /**
   * Swimlane board redesign — explicit collapse-state OVERRIDES, keyed by
   * lane id (a Plan's `id`, or BoardView's `LOOSE_LANE_ID` for the "no plan"
   * band). Absence of a key does NOT mean "expanded" — the caller (BoardView)
   * applies its own default (terminal plans default-collapsed, everything
   * else default-expanded per the swimlane spec) whenever a key is missing,
   * then calls `setLaneCollapsed` with the resolved effective value on
   * toggle. Keeping the map override-only (rather than baking a default in
   * here) avoids this store having to know about Plan states at all.
   */
  collapsedLanes: Record<string, boolean>
  setLaneCollapsed: (id: string, collapsed: boolean) => void
  /**
   * Clear every lane-collapse override. `collapsedLanes` is keyed by Plan id
   * (workspace-scoped) plus the shared `LOOSE_LANE_ID` sentinel — that
   * sentinel is the SAME literal across every workspace, so without a reset
   * on workspace change, collapsing Loose in workspace A leaks into
   * workspace B. Called by `WorkspaceTabContainer` alongside its
   * `setActivePlanId(null)` reset.
   */
  resetCollapsedLanes: () => void
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
  collapsedLanes: {},
  setLaneCollapsed: (id, collapsed) =>
    set((s) => ({ collapsedLanes: { ...s.collapsedLanes, [id]: collapsed } })),
  resetCollapsedLanes: () => set({ collapsedLanes: {} }),
}))
