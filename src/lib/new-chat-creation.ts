// #1428 step 2 (spec 4.4.1, 4.4.2): the one keyed creation of a new chat K.
// `creationByKey` replaces the global `sessionEnsure`. Every creator of K (a
// send with files through bound-send.ts, Open browser in ChatControls.tsx)
// shares this slot, so K is created once and S is aliased to K once.
import { createSession } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { useSessionStore } from '@/store/session'
import { aliasNewChat, currentAuthGeneration } from '@/store/chat/drafts'

/** One creation per logical chat (4.4.2). */
const creationByKey = new Map<string, Promise<string | null>>()

/**
 * Creates new chat K with `new_chat_key: K`, or returns the creation already
 * under way for K. Resolves to S, or null on failure or sign-out. Activation
 * rule (4.4.1): S becomes the open chat only if K still is.
 */
export function createForNewChat(key: string, agentId: string, workspaceId: string | null): Promise<string | null> {
  const existing = creationByKey.get(key)
  if (existing) return existing
  const generation = currentAuthGeneration()
  const creation = (async () => {
    try {
      const created = await createSession({
        agent_id: agentId,
        ...(workspaceId ? { workspace_id: workspaceId } : {}),
        new_chat_key: key,
      })
      if (generation !== currentAuthGeneration()) return null
      aliasNewChat(key, created.id)
      if (useSessionStore.getState().newChatKey === key && useSessionStore.getState().activeSessionId === null) {
        useSessionStore.getState().setActiveSession(created.id, agentId || undefined)
      }
      return created.id
    } catch (err) {
      logDiagnostic('boundSendCreateFailed', { message: err instanceof Error ? err.message : String(err) })
      creationByKey.delete(key)
      return null
    }
  })()
  creationByKey.set(key, creation)
  return creation
}

/** Sign-out (4.4.3): pending creations are forgotten. */
export function resetNewChatCreations(): void {
  creationByKey.clear()
}
