// #1428 step 2 (spec C r9, 4.4.1): the only two writers of the "no chat yet"
// state. Every place that used to call setActiveSession(null…) goes through
// one of them, so the open chat key (openChatKey) is always defined:
// - enterNewChat: a new chat the person can type into, with a fresh key K
//   (or the given K when "Open and send" re-enters an unsaved new chat);
// - enterBlockedNoChat: a screen with no chat and no composer to send from
//   (unavailable workspace entry, a stuck placeholder). It holds no K: whether
//   the blocked state should hold one is parked as C5 / #1896.
// scripts/check-bound-send.sh fails any other setActiveSession(null…) call.

import { generateId } from '@/lib/constants'
import { registerNewChat } from '@/store/chat/drafts'
import type { AgentKind } from '@/lib/api'
import { useSessionStore } from '@/store/session'

/** Mints a new-chat key: 16..128 characters of [A-Za-z0-9_-] (spec 4.2). */
export function mintNewChatKey(): string {
  return `nc-${generateId()}`
}

export function enterNewChat(
  workspaceId: string | null,
  agentId: string | null,
  agentType?: AgentKind,
  reuseKey?: string,
): string {
  const key = reuseKey ?? mintNewChatKey()
  registerNewChat(key, { workspaceId, agentId })
  useSessionStore.setState({ newChatKey: key })
  useSessionStore.getState().setActiveSession(null, agentId, agentType)
  return key
}

export function enterBlockedNoChat(reason: 'unavailable' | 'empty-team' | 'stuck-pending'): void {
  void reason
  useSessionStore.setState({ newChatKey: null })
  useSessionStore.getState().setActiveSession(null)
}
