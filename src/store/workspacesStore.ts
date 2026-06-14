import { create } from 'zustand'

interface WorkspacesState {
  /** The currently-selected workspace ID for filtering the task board. null = "All workspaces". */
  activeWorkspaceId: string | null
  setActiveWorkspaceId: (id: string | null) => void
  /** The currently-active milestone filter ID. null = "All". */
  activeMilestoneId: string | null
  setActiveMilestoneId: (id: string | null) => void
}

export const useWorkspacesStore = create<WorkspacesState>((set) => ({
  activeWorkspaceId: null,
  setActiveWorkspaceId: (id) => set({ activeWorkspaceId: id }),
  activeMilestoneId: null,
  setActiveMilestoneId: (id) => set({ activeMilestoneId: id }),
}))
