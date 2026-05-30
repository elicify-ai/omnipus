import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { useSessionStore } from '@/store/session'

// Wrapper that starts a fresh session whenever the root chat route mounts.
// This ensures that navigating to '/' (e.g. via page.goto('/') in tests, or
// the user clicking the logo) always presents an empty composer rather than
// resuming the previous session.  Sessions.$sessionId route sets its own
// activeSessionId, so this only fires on the unparameterised root route.
function RootChatScreen() {
  const startNewSession = useSessionStore((s) => s.startNewSession)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const navigate = useNavigate()

  useEffect(() => {
    startNewSession()
    // Run once on mount — startNewSession is a stable Zustand reference.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Once a session is minted from this route (either via "New Chat" →
  // doCreateSession or via session_started after the first sent message),
  // reflect it in the URL so back/forward and refresh can restore the
  // conversation. Skip the panel-attach path (attachedSessionType is non-null
  // only when attachToSession ran), so seeded test sessions opened via the
  // SessionPanel don't get a route-load round-trip that races their replay.
  useEffect(() => {
    if (activeSessionId && attachedSessionType === null) {
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
