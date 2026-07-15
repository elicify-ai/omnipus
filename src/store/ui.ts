import { create } from 'zustand'
import { generateId } from '@/lib/constants'
import type { WizardCli, WizardType } from '@/components/agents/wizard/types'

export interface Toast {
  id: string
  message: string
  variant: 'default' | 'error' | 'success' | 'warning'
  duration?: number
  testId?: string
  /** Optional primary action rendered inside the toast. */
  action?: {
    label: string
    onClick: () => void
  }
}

interface UiStore {
  // Session panel
  sessionPanelOpen: boolean
  openSessionPanel: () => void
  closeSessionPanel: () => void

  // Search modal — cross-workspace session search (step 6 of the sidebar-merge
  // plan). Same store-driven pattern as sessionPanelOpen; opened from the
  // sidebar search icon (Sidebar.tsx) and the /search slash command
  // (useSlashMenu.ts). The SearchModal component is mounted once at the AppShell
  // root, so either entry point drives the same single instance.
  searchModalOpen: boolean
  searchModalWorkspaceFilter: string | null
  openSearchModal: (workspaceId?: string) => void
  closeSearchModal: () => void

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
  /**
   * CLI choice for the create-agent modal. Only meaningful when
   * `createAgentModalType === 'subagent_3p'` — wizard pre-fills the executor
   * CLI chip (Step 1) and the executor.cli path (Step 3). Reset to `null`
   * on every close so the next open defaults back to the bare subagent_3p
   * shape unless an explicit opener (e.g. the per-CLI "+ Add" button on the
   * Agents roster, W6) sets it again. W4 of agent-form-requirements added
   * the second optional parameter to `openCreateAgentModal`.
   */
  createAgentModalCli: WizardCli | null
  openCreateAgentModal: (type?: WizardType, cli?: WizardCli) => void
  closeCreateAgentModal: () => void

  // Edit/view agent slide-over; null = closed.
  editAgentId: string | null
  /**
   * The workspace that opened the agent slide-over. Set when the editor is
   * opened from a workspace Team tab (FR-018 / A5); null when opened from
   * the global Agents screen. Drives the conditional Heartbeat tab
   * (US-5 / FR-016): the tab renders only when this is non-null AND the
   * agent is not a worker (FR-025).
   */
  editAgentWorkspaceId: string | null
  /**
   * Open the agent edit slide-over.
   *
   * @param agentId - The agent to edit.
   * @param workspaceId - When provided (opened from a workspace Team tab),
   *   the Heartbeat tab is shown for this (workspace, agent) pair. When
   *   omitted (opened from the global Agents screen), no Heartbeat tab.
   */
  openEditAgentSlideOver: (agentId: string, workspaceId?: string) => void
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

  // Model selector open state — set true by the /model slash command so the
  // chat-header model picker (composer-model-selector in ChatControls) opens
  // without the user having to click it directly.
  modelSelectorOpen: boolean
  setModelSelectorOpen: (open: boolean) => void

  // Agent selector open state — set true by the /agents slash command so the
  // chat-header agent picker opens without the user having to click it directly.
  agentSelectorOpen: boolean
  setAgentSelectorOpen: (open: boolean) => void

  // Media lightbox (enlarged image / diagram). A SINGLE global instance rendered
  // at the app root (AppShell) — NOT per-message — so it lives outside the
  // virtualized chat list. The list periodically remounts its rows; a per-row
  // lightbox would be torn down mid-view, and keying its open-state by content
  // (src/svg) cross-contaminated two identical images/diagrams. One store-owned
  // instance avoids both. `closeMediaLightbox` is idempotent.
  mediaLightbox: MediaLightboxContent | null
  openMediaLightbox: (content: MediaLightboxContent) => void
  closeMediaLightbox: () => void

  // Live browser panel (ADR-038) — overlay showing a real-time screencast of
  // an agent's shared browser session, with optional human take-control.
  // A SINGLE global instance mounted at the app root (AppShell), mirroring
  // mediaLightbox above. null = closed. Opened from the "Watch live"
  // affordance on a running browser tool-call (BrowserTool.tsx).
  browserPanel: { sessionId: string; agentId: string } | null
  openBrowserPanel: (sessionId: string, agentId: string) => void
  closeBrowserPanel: () => void

  // Live browser panel PIN state (ADR-040 D4) — false (default): the
  // BrowserLivePanel above renders as today's right-side overlay `Sheet`.
  // true: it renders as a docked flex column beside the chat instead (see
  // BrowserLivePanel.tsx + AppShell.tsx). Lives in this store — not local
  // component state — because AppShell also reads it indirectly (the docked
  // panel becomes a normal flex sibling there) and because BrowserLiveView's
  // header 📌 toggle (a different component) is what flips it.
  //
  // Deliberately NOT localStorage-persisted, unlike useSidebarStore's
  // `isPinned` (a SEPARATE store that wraps itself in zustand's `persist`
  // middleware for exactly that one field): no other field in THIS store
  // persists across a reload either (sessionPanelOpen, notificationPanelOpen,
  // mediaLightbox, browserPanel itself, …) — adding persistence here would
  // make this the one and only persisted field of an otherwise entirely
  // session-scoped store, for a narrow toggle nobody asked to survive a
  // reload. Revisit (wrap this store in `persist` + `partialize`, mirroring
  // sidebar.ts) if product wants the pin choice to survive a reload.
  browserPanelPinned: boolean
  toggleBrowserPanelPinned: () => void
}

/** Discriminated payload for the global media lightbox: a raster image (by URL)
 * or an already-sanitized SVG string (diagrams). The renderer derives the
 * copy/share/download toolbar from `kind`. */
export type MediaLightboxContent =
  | { kind: 'image'; src: string; alt?: string; filename?: string }
  | { kind: 'svg'; svg: string; title?: string; filename?: string }

// Tracks auto-dismiss timers outside state so they can be cleared on manual dismiss
const toastTimers = new Map<string, ReturnType<typeof setTimeout>>()

export const useUiStore = create<UiStore>((set, get) => ({
  sessionPanelOpen: false,
  openSessionPanel: () => set({ sessionPanelOpen: true }),
  closeSessionPanel: () => set({ sessionPanelOpen: false }),

  searchModalOpen: false,
  searchModalWorkspaceFilter: null,
  openSearchModal: (workspaceId?: string) => set({ searchModalOpen: true, searchModalWorkspaceFilter: workspaceId ?? null }),
  closeSearchModal: () => set({ searchModalOpen: false, searchModalWorkspaceFilter: null }),

  createAgentModalOpen: false,
  createAgentModalType: 'Main',
  createAgentModalCli: null,
  openCreateAgentModal: (type, cli) =>
    set({
      createAgentModalOpen: true,
      createAgentModalType: type ?? 'Main',
      createAgentModalCli: cli ?? null,
    }),
  closeCreateAgentModal: () =>
    set({
      createAgentModalOpen: false,
      createAgentModalType: 'Main',
      createAgentModalCli: null,
    }),

  editAgentId: null,
  editAgentWorkspaceId: null,
  openEditAgentSlideOver: (agentId, workspaceId) =>
    set({ editAgentId: agentId, editAgentWorkspaceId: workspaceId ?? null }),
  closeEditAgentSlideOver: () => set({ editAgentId: null, editAgentWorkspaceId: null }),

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

  modelSelectorOpen: false,
  setModelSelectorOpen: (open) => set({ modelSelectorOpen: open }),

  agentSelectorOpen: false,
  setAgentSelectorOpen: (open) => set({ agentSelectorOpen: open }),

  mediaLightbox: null,
  openMediaLightbox: (content) => set({ mediaLightbox: content }),
  closeMediaLightbox: () => set({ mediaLightbox: null }),

  browserPanel: null,
  openBrowserPanel: (sessionId, agentId) => set({ browserPanel: { sessionId, agentId } }),
  closeBrowserPanel: () => set({ browserPanel: null }),

  browserPanelPinned: false,
  toggleBrowserPanelPinned: () => set((state) => ({ browserPanelPinned: !state.browserPanelPinned })),
}))
