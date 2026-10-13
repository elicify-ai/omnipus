import { create } from 'zustand'
import { generateId } from '@/lib/constants'
import type { WizardCli, WizardType } from '@/components/agents/wizard/types'
import type { ActivePanel, OpenPanel, PanelOpenArgs } from '@/components/panel-shell/types'
import { capturePanelTriggerOrigin } from '@/components/panel-shell/panelFocus'

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
  // Search modal — cross-workspace session search (step 6 of the sidebar-merge
  // plan). Opened from the sidebar search icon (Sidebar.tsx) and the /sessions
  // slash command (useSlashMenu.ts). The SearchModal component is mounted once
  // at the AppShell root, so either entry point drives the same single instance.
  //
  // TWO MODES, one panel. `searchModalMode` picks which behavior SearchModal
  // renders:
  //   - 'sessions' (default): the original session-search panel — sessions
  //     focus, ArrowUp/Down walks sessions, Enter opens the highlighted one.
  //     Entered via `openSearchModal` (sidebar search icon, a workspace's
  //     "More…" button with a workspaceId filter, /sessions).
  //   - 'workspaces': the workspace-switch panel — ALL workspaces listed
  //     (including zero-session ones), session groups start collapsed,
  //     ArrowUp/Down walks workspace headers, Enter switches to the
  //     highlighted workspace, typing filters by workspace name. Entered
  //     via `openWorkspaceSwitcher` (/workspace only).
  // Kept as a separate action rather than an `openSearchModal(workspaceId?,
  // mode?)` overload so every existing `openSearchModal` call site (sidebar
  // icon, "More…", /sessions) stays source-compatible with zero changes.
  searchModalOpen: boolean
  searchModalWorkspaceFilter: string | null
  /** Agent id for Past sessions. Null means every agent. */
  searchModalAgentFilter: string | null
  searchModalMode: 'sessions' | 'workspaces'
  openSearchModal: (workspaceId?: string, agentId?: string) => void
  setSearchModalAgentFilter: (agentId: string | null) => void
  openWorkspaceSwitcher: () => void
  closeSearchModal: () => void
  /**
   * One-shot: Sessions Open asked for this session's Activity panel.
   * ActivityBar opens the panel only after this id is the active chat, then clears it.
   * Null means nobody is waiting.
   */
  activityPanelRequest: string | null
  requestActivityPanel: (sessionId: string) => void
  consumeActivityPanelRequest: (sessionId: string) => void

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

  // Side-panel shell — ONE panel at a time (side-panel-shell-spec.md §8.1).
  // Replaces the two independent slices this store used to carry
  // (`browserPanel` / `libraryPanel`): they could hold values simultaneously,
  // which is exactly the SP-7/SC-005 violation the single slice makes
  // unrepresentable — opening a panel REPLACES whatever was open. The shell
  // UI (SidePanelShell, mounted at the AppShell root) renders the panel's
  // content from the registry (src/components/panel-shell/registry.tsx);
  // nothing outside the shell reads `context` directly.
  //
  // `openPanel(id, context?)` / `closePanel()` / `setPanelWidth(px)` are the
  // §8.1 contract, verbatim. Entry points (sidebar Library, ChatControls,
  // "Watch live", the tab-strip toggle, deep links) call `openPanel`; every
  // REPLACING transition goes through the outgoing panel's `beforeLeave`
  // guard FIRST (CRIT-001/FR-013) — the guard lives at the call sites via
  // `leaveGateThen` (src/components/panel-shell/leaveGate.ts), not in the
  // store action, so the store stays a plain reducer (the RED pack asserts
  // exactly this shape). `panelWidth` is the stored (unsettled) width the
  // shell clamps against live geometry (MAJ-009); `guardPending`/
  // `historyPushed` are the shell's transition flags.
  //
  // State table (F6):
  //   guardPending=false/historyPushed=false — ordinary docked or idle state
  //   guardPending=true /historyPushed=*     — leave decision pending; moves stop
  //   guardPending=false/historyPushed=true  — one phone takeover entry exists
  // A cancelled phone Back restores historyPushed=true; leaving takeover or
  // an external close collapses that entry and returns both flags to false.
  activePanel: ActivePanel | null
  /** Monotonic token invalidating delayed work after any newer panel intent. */
  panelIntentRevision: number
  panelWidth: number | null
  guardPending: boolean
  historyPushed: boolean
  openPanel: OpenPanel
  closePanel: () => void
  setPanelWidth: (px: number) => void
  resetPanelWidth: () => void
  setGuardPending: (pending: boolean) => void
  setHistoryPushed: (pushed: boolean) => void
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
  searchModalOpen: false,
  searchModalWorkspaceFilter: null,
  searchModalAgentFilter: null,
  searchModalMode: 'sessions',
  openSearchModal: (workspaceId?: string, agentId?: string) =>
    set({
      searchModalOpen: true,
      searchModalWorkspaceFilter: workspaceId ?? null,
      searchModalAgentFilter: agentId ?? null,
      searchModalMode: 'sessions',
    }),
  setSearchModalAgentFilter: (agentId) => set({ searchModalAgentFilter: agentId }),
  openWorkspaceSwitcher: () =>
    set({
      searchModalOpen: true,
      searchModalWorkspaceFilter: null,
      searchModalAgentFilter: null,
      searchModalMode: 'workspaces',
    }),
  // Reset the mode back to 'sessions' on every close so a prior /workspace
  // open can't leak into the next open via the sidebar search icon or
  // /sessions (both of which go through openSearchModal, which already sets
  // 'sessions' explicitly — this reset is the belt-and-braces default for
  // any other close path, e.g. Escape/outside-click, which don't call
  // openSearchModal at all). The agent filter resets with it.
  closeSearchModal: () => set({
    searchModalOpen: false,
    searchModalWorkspaceFilter: null,
    searchModalAgentFilter: null,
    searchModalMode: 'sessions',
  }),
  activityPanelRequest: null,
  requestActivityPanel: (sessionId) => set({ activityPanelRequest: sessionId }),
  consumeActivityPanelRequest: (sessionId) => {
    if (get().activityPanelRequest === sessionId) set({ activityPanelRequest: null })
  },

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

  activePanel: null,
  panelIntentRevision: 0,
  panelWidth: null,
  guardPending: false,
  historyPushed: false,
  openPanel: (...args: PanelOpenArgs) => {
    const [id, suppliedContext] = args
    const context = suppliedContext ?? {}
    capturePanelTriggerOrigin(id)
    set((state) => ({
      activePanel: { id, context } as ActivePanel,
      panelIntentRevision: state.panelIntentRevision + 1,
      // Opening a DIFFERENT panel re-reads that panel's own width (its stored
      // value for its own scope, or the SP-17 default). Same panel re-open =
      // keep the current width (a context refresh must not jump the divider).
      panelWidth: state.activePanel?.id === id ? state.panelWidth : null,
    }))
  },
  closePanel: () =>
    set((state) => ({
      activePanel: null,
      panelIntentRevision: state.panelIntentRevision + 1,
      panelWidth: null,
      guardPending: false,
      historyPushed: false,
    })),
  setPanelWidth: (px) => set({ panelWidth: px }),
  resetPanelWidth: () => set({ panelWidth: null }),
  setGuardPending: (pending) => set({ guardPending: pending }),
  setHistoryPushed: (pushed) => set({ historyPushed: pushed }),
}))
