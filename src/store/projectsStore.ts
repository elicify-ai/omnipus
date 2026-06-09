import { create } from 'zustand'

interface ProjectsState {
  /** The currently-selected project ID for filtering the task board. null = "All projects". */
  activeProjectId: string | null
  setActiveProjectId: (id: string | null) => void
  /** The currently-active milestone filter ID. null = "All". */
  activeMilestoneId: string | null
  setActiveMilestoneId: (id: string | null) => void
}

export const useProjectsStore = create<ProjectsState>((set) => ({
  activeProjectId: null,
  setActiveProjectId: (id) => set({ activeProjectId: id }),
  activeMilestoneId: null,
  setActiveMilestoneId: (id) => set({ activeMilestoneId: id }),
}))
