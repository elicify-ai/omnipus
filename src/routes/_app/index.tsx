import { useEffect, useRef } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { useSessionStore } from '@/store/session'

// RootChatScreen owns the "/" (new-chat) surface. The ROUTE is the single
// source of truth for which session is shown: "/" means "no active session",
// and /sessions/$id means that session. Every path that lands here — the
// "New Chat" button, the sidebar "Chat" link, the tasks-screen redirect,
// post-login/onboarding — gets a clean empty composer, not the previous
// conversation.
function RootChatScreen() {
  const startNewSession = useSessionStore((s) => s.startNewSession)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const navigate = useNavigate()
  // Flips true only once we've OBSERVED the cleared (null) session state after
  // the mount clear below. Until then the URL must not advance — see the
  // navigation effect for why this kills the #417 bounce.
  const clearedRef = useRef(false)

  useEffect(() => {
    startNewSession()
    // Run once on mount — startNewSession is a stable Zustand reference.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Advance the URL to /sessions/<id> ONLY for a session minted while on this
  // screen — i.e. a genuine null -> id transition observed AFTER the mount
  // clear (session_started following the first sent message, or doCreateSession).
  //
  // #417: previously this reflected ANY non-null activeSessionId to the URL,
  // so arriving at "/" with a STALE activeSessionId left over from a prior
  // route (the mount clear hasn't been observed yet on the first commit) bounced
  // the URL straight back to the old session. clearedRef gates that: on the
  // first commit the stale id is present but clearedRef is still false, so no
  // navigation fires; once the clear is observed (activeSessionId === null)
  // clearedRef flips true and only a subsequently-minted session navigates.
  // This makes the route authoritative for every entry point — no component
  // needs to pre-clear the store to compensate for effect ordering.
  //
  // The panel-attach path (attachedSessionType non-null, set by attachToSession)
  // is still skipped so seeded sessions opened via the SessionPanel don't get a
  // route-load round-trip that races their replay.
  useEffect(() => {
    if (activeSessionId === null) {
      clearedRef.current = true
      return
    }
    if (clearedRef.current && attachedSessionType === null) {
      navigate({
        to: '/sessions/$sessionId',
        params: { sessionId: activeSessionId },
        replace: true,
      })
    }
  }, [activeSessionId, attachedSessionType, navigate])

  return <ChatScreen />
}

export const Route = createFileRoute('/_app/')({
  component: RootChatScreen,
})
