import { create } from 'zustand'
import { generateId } from '@/lib/constants'

export interface Toast {
  id: string
  message: string
  variant: 'default' | 'error' | 'success' | 'warning'
  duration?: number
  testId?: string
}

interface UiStore {
  // Session panel
  sessionPanelOpen: boolean
  openSessionPanel: () => void
  closeSessionPanel: () => void

  // Create agent modal
  createAgentModalOpen: boolean
  /**
   * Lifecycle preset for the create-agent modal. Controls which wizard branch
   * the modal renders and which `type` value the create request sends. Reset
   * to 'Main' on every close so the next open defaults back to the chat-agent
   * shape unless an explicit opener (e.g. the per-section "+ Add" buttons on
   * the Agents screen, W6) sets it again.
   *
   * W4 (agent-form-requirements): widened from `'custom' | 'worker'` (the
   * legacy 2-tier enum) to the 3-type wire enum (`Main` / `Subagent` /
   * `subagent_3p`). The store is the single source of truth for the wizard's
   * locked type; the modal renders the corresponding wizard branch.
   */
  createAgentModalType: 'Main' | 'Subagent' | 'subagent_3p'
  openCreateAgentModal: (type?: 'Main' | 'Subagent' | 'subagent_3p') => void
  closeCreateAgentModal: () => void

  // Edit/view agent slide-over; null = closed.
  editAgentId: string | null
  openEditAgentSlideOver: (agentId: string) => void
  closeEditAgentSlideOver: () => void

  // Notification center panel (#264)
  notificationPanelOpen: boolean
  openNotificationPanel: () => void
  closeNotificationPanel: () => void
  toggleNotificationPanel: () => void

  // Toast
  toasts: Toast[]
  addToast: (toast: Omit<Toast, 'id'>) => void
  removeToast: (id: string) => void

  // SubagentBlock expansion state — keyed by spanId so the same span survives
  // a live→historical render-tree swap (when streaming ends and the
  // virtualizer takes over from the AssistantUI live message) and keeps its
  // user-chosen expanded/collapsed state. Previously held in component-local
  // useState which the parent-swap unmount reset to false.
  expandedSpans: Record<string, boolean>
  toggleSpanExpansion: (spanId: string) => void
}

// Tracks auto-dismiss timers outside state so they can be cleared on manual dismiss
const toastTimers = new Map<string, ReturnType<typeof setTimeout>>()

export const useUiStore = create<UiStore>((set, get) => ({
  sessionPanelOpen: false,
  openSessionPanel: () => set({ sessionPanelOpen: true }),
  closeSessionPanel: () => set({ sessionPanelOpen: false }),

  createAgentModalOpen: false,
  createAgentModalType: 'Main',
  openCreateAgentModal: (type) =>
    set({ createAgentModalOpen: true, createAgentModalType: type ?? 'Main' }),
  closeCreateAgentModal: () =>
    set({ createAgentModalOpen: false, createAgentModalType: 'Main' }),

  editAgentId: null,
  openEditAgentSlideOver: (agentId) => set({ editAgentId: agentId }),
  closeEditAgentSlideOver: () => set({ editAgentId: null }),

  notificationPanelOpen: false,
  openNotificationPanel: () => set({ notificationPanelOpen: true }),
  closeNotificationPanel: () => set({ notificationPanelOpen: false }),
  toggleNotificationPanel: () =>
    set((state) => ({ notificationPanelOpen: !state.notificationPanelOpen })),

  toasts: [],
  addToast: (toast) => {
    const id = generateId()
    set((state) => ({ toasts: [...state.toasts, { ...toast, id }] }))
    const duration = toast.duration ?? 4000
    const timer = setTimeout(() => {
      get().removeToast(id)
      toastTimers.delete(id)
    }, duration)
    toastTimers.set(id, timer)
  },
  removeToast: (id) => {
    const timer = toastTimers.get(id)
    if (timer !== undefined) {
      clearTimeout(timer)
      toastTimers.delete(id)
    }
    set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) }))
  },

  expandedSpans: {},
  toggleSpanExpansion: (spanId) =>
    set((state) => ({
      expandedSpans: { ...state.expandedSpans, [spanId]: !state.expandedSpans[spanId] },
    })),
}))
