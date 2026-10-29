// useChatPresence — tells the gateway whether this tab is WATCHING a chat
// (chat-links spec "Watching", FR-026, BS-6.16), and registers the mounted
// chat view the client-side rules read (store/chatView.ts).
//
// Mounted from the three chat views only — `routes/_app/sessions.$sessionId.tsx`,
// `components/workspaces/WorkspaceChatTab.tsx`, `components/chat/OmnipusChatScreen.tsx`
// — and only while they render a ChatScreen. Not from ChatScreen itself.
// Callers pass `null` while no ChatScreen is shown (loading, not found,
// redirecting), which unregisters and reports not watching.
//
// Watching = the document is visible AND `sessionId` is the active session
// (the mounted half is the call site's job). Frames, the generated
// ChatPresenceFrame `{ type, session_id, watching }`:
//   - watching:true  when the tab becomes watching, then every 15 s while it
//                    stays watching (the gateway expires a claim after 45 s
//                    without a refresh), and again after a reconnect (the new
//                    connection holds no claim yet);
//   - watching:false when it stops: hidden tab, another session made active,
//                    session change, unmount.
// Model: components/workspaces/mail/mailPanelPresence.ts — best-effort sends
// on the authenticated socket; nothing is sent while disconnected, and the
// gateway drops a connection's claim when the socket goes away.

import { useEffect } from 'react'
import type { ChatPresenceFrame } from '@/lib/api/generated/asyncapi-types'
import { isOmnipusAgentId } from '@/lib/omnipus'
import { registerChatView } from '@/store/chatView'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

/** Refresh cadence while watching. Three missed refreshes expire the claim server-side. */
export const CHAT_PRESENCE_REFRESH_MS = 15_000

/** The one frame constructor: exactly the generated frame's three keys. */
export function chatPresenceFrame(sessionId: string, watching: boolean): ChatPresenceFrame {
  return { type: 'chat_presence', session_id: sessionId, watching }
}

function sendPresence(sessionId: string, watching: boolean): void {
  const { connection, isConnected } = useConnectionStore.getState()
  if (connection === null || !isConnected) return
  // Best-effort: a false return (socket closing) is fine — the gateway drops
  // this connection's claim with the socket, and a reconnect re-sends.
  connection.send(chatPresenceFrame(sessionId, watching))
}

function isWatching(sessionId: string): boolean {
  if (typeof document === 'undefined' || document.visibilityState !== 'visible') return false
  return useSessionStore.getState().activeSessionId === sessionId
}

/** The optimistic first-send placeholder is never a real, attachable session. */
const PENDING_SESSION_ID = '__pending'

export function useChatPresence(requestedSessionId: string | null, workspaceId: string | null): void {
  const sessionId = requestedSessionId === PENDING_SESSION_ID ? null : requestedSessionId
  const omnipusChat = useSessionStore((s) => isOmnipusAgentId(s.activeAgentId))
  // Omnipus's chat never resolves bare links against a workspace (spec
  // D-omnipus-links), whichever view shows it.
  const linkWorkspaceId = omnipusChat ? null : workspaceId

  useEffect(() => {
    if (sessionId === null) return undefined
    return registerChatView(sessionId, linkWorkspaceId)
  }, [sessionId, linkWorkspaceId])

  useEffect(() => {
    if (sessionId === null) return undefined
    const sid = sessionId
    let watching = false
    let refresh: ReturnType<typeof setInterval> | null = null

    const stopRefresh = () => {
      if (refresh !== null) clearInterval(refresh)
      refresh = null
    }
    const evaluate = () => {
      const next = isWatching(sid)
      if (next === watching) return
      watching = next
      sendPresence(sid, next)
      stopRefresh()
      if (next) refresh = setInterval(evaluateOrRefresh, CHAT_PRESENCE_REFRESH_MS)
    }
    function evaluateOrRefresh() {
      if (watching && isWatching(sid)) sendPresence(sid, true)
      else evaluate()
    }

    let wasConnected = useConnectionStore.getState().isConnected
    const unsubscribeConnection = useConnectionStore.subscribe((state) => {
      const reconnected = state.isConnected && !wasConnected
      wasConnected = state.isConnected
      if (reconnected && watching) sendPresence(sid, true)
    })
    const unsubscribeSession = useSessionStore.subscribe(evaluate)
    document.addEventListener('visibilitychange', evaluate)
    evaluate()

    return () => {
      document.removeEventListener('visibilitychange', evaluate)
      unsubscribeSession()
      unsubscribeConnection()
      stopRefresh()
      if (watching) sendPresence(sid, false)
    }
  }, [sessionId])
}
