// chatView.ts — which chat views are mounted right now, and the workspace each
// one recorded for its session (chat-links spec, "Watching" and "Workspace for
// bare links").
//
// The three chat views — `routes/_app/sessions.$sessionId.tsx`,
// `components/workspaces/WorkspaceChatTab.tsx` and
// `components/chat/OmnipusChatScreen.tsx` — register here (through
// `useChatPresence`) only while they actually render a ChatScreen for a
// session. Two readers:
//
//   - "Is a chat screen for S mounted?" — half of the Watching rule. Settings,
//     the full-page Library, the board, the calendar and group-chat rooms never
//     register, so a tab showing them is not watching any chat.
//   - "Which workspace do bare chat links resolve against?" — the workspace
//     the session on screen recorded, as declared by the view that shows it.
//     Omnipus's chat declares none (`useChatPresence` enforces it): Omnipus
//     works across every workspace, so a bare link there would usually point
//     at the wrong file (spec D-omnipus-links).
//
// Deliberately depends on nothing but zustand: the markdown renderers read it
// (useChatLinkWorkspace), and they are also used outside chat (Mail, Library
// previews) where the session store is not part of the module graph.
//
// In-memory only, per page load. Registrations are reference-counted so a
// StrictMode double mount, or two views of one session, never strand an entry.

import { create } from 'zustand'

interface MountedChatView { // not-wire-format: page-local registry entry, never sent anywhere
  /** The workspace the session recorded, or null when the view knows none. */
  workspaceId: string | null
  mounts: number
  /** Registration order; the most recent mounted view is the one on screen. */
  order: number
}

interface ChatViewState {
  views: Record<string, MountedChatView>
}

export const useChatViewStore = create<ChatViewState>(() => ({ views: {} }))

let registrations = 0

/**
 * Register a mounted chat view for `sessionId`. Returns the matching
 * unregister function (idempotent; call it on unmount or session change).
 */
export function registerChatView(sessionId: string, workspaceId: string | null): () => void {
  registrations += 1
  const order = registrations
  useChatViewStore.setState((state) => {
    const prior = state.views[sessionId]
    return {
      views: { ...state.views, [sessionId]: { workspaceId, mounts: (prior?.mounts ?? 0) + 1, order } },
    }
  })
  let released = false
  return () => {
    if (released) return
    released = true
    useChatViewStore.setState((state) => {
      const prior = state.views[sessionId]
      if (!prior) return {}
      const views = { ...state.views }
      if (prior.mounts <= 1) delete views[sessionId]
      else views[sessionId] = { ...prior, mounts: prior.mounts - 1 }
      return { views }
    })
  }
}

/** True while at least one chat view for `sessionId` is mounted. */
export function isChatViewMounted(sessionId: string): boolean {
  return (useChatViewStore.getState().views[sessionId]?.mounts ?? 0) > 0
}

function onScreenWorkspace(state: ChatViewState): string | null {
  let latest: MountedChatView | null = null
  for (const view of Object.values(state.views)) {
    if (latest === null || view.order > latest.order) latest = view
  }
  return latest?.workspaceId ?? null
}

/**
 * The workspace bare chat links resolve against: the one recorded by the chat
 * view on screen. Null when no chat view is mounted, when its session recorded
 * no workspace, and always in Omnipus's chat.
 */
export function useChatLinkWorkspace(): string | null {
  return useChatViewStore(onScreenWorkspace)
}
