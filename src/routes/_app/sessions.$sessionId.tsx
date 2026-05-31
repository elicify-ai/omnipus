import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { fetchSessionDetail, isApiError } from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

function SessionRoute() {
  const { sessionId } = Route.useParams()
  const attachToSession = useSessionStore((s) => s.attachToSession)
  const setActiveSession = useSessionStore((s) => s.setActiveSession)
  // Track which session we last attached to so a re-render doesn't re-send
  // the attach_session frame (causing a double replay).
  const attachedRef = useRef<string | null>(null)

  // Loader pre-fetches session detail and activates the session synchronously
  // in the Zustand store before this component renders. The useQuery below is
  // kept for live data updates (e.g. agent_removed flag changing).
  const loaderData = Route.useLoaderData()

  const { data: detail } = useQuery({
    queryKey: ['session-detail', sessionId],
    // Loader already fetched session detail — use it as initialData so
    // the query doesn't fire a redundant network request on mount.
    initialData: loaderData ?? undefined,
    queryFn: () => fetchSessionDetail(sessionId),
    enabled: !!sessionId,
    retry: false,
    staleTime: 10_000,
  })

  // #250: When the WS is already open and we navigate to a session URL (e.g.
  // "Open in Chat" from TaskDetailPanel, or a direct deep-link), trigger the
  // full attach pipeline so the gateway replays the transcript and live tokens
  // stream into this bucket. setActiveSession alone does NOT send attach_session;
  // only attachToSession does (session.ts:108).
  //
  // attachToSession is used when the WS is connected. When the WS is not yet
  // open (cold load), setActiveSession is sufficient because WsLifecycle will
  // call attachToSession once the connection opens.
  useEffect(() => {
    if (!detail?.session) return
    const { session } = detail
    const { connection } = useConnectionStore.getState()

    if (connection && attachedRef.current !== session.id) {
      attachedRef.current = session.id
      attachToSession(session.id, 'chat', undefined, session.agent_id)
    } else if (!connection) {
      // WS not open — fall back to setActiveSession so the store is consistent.
      // WsLifecycle sends attach_session once the WS connects (onConnected path).
      setActiveSession(session.id, session.agent_id, null)
    }
  }, [detail?.session?.id, detail?.session?.agent_id, attachToSession, setActiveSession])

  return <ChatScreen agentRemoved={detail?.agent_removed ?? false} />
}

export const Route = createFileRoute('/_app/sessions/$sessionId')({
  component: SessionRoute,
  loader: async ({ params }) => {
    // Pre-fetch session detail and activate the session in the Zustand store
    // BEFORE the component renders. This guarantees that activeSessionId is
    // set when the ChatScreen mounts — eliminating the race where a quickly
    // typed message is routed by the gateway to a new orphan session instead
    // of the URL session (because activeSessionId was still null from useEffect
    // firing after the first render).
    //
    // Note: the loader runs on every navigation to this route. Using
    // setActiveSession here (not attachToSession) is intentional — the loader
    // runs before the component mounts, so the WS connection state is not yet
    // observable. The component's useEffect above handles the attach_session
    // WS frame once the component is mounted and the connection is available.
    try {
      const detail = await fetchSessionDetail(params.sessionId)
      if (detail?.session) {
        useSessionStore.getState().setActiveSession(detail.session.id, detail.session.agent_id, null)
      }
      return detail ?? null
    } catch (err) {
      // Only swallow 404 — session genuinely does not exist, let ChatScreen
      // render with an empty/default state.
      // Re-throw everything else (ApiSchemaError, 5xx ApiError, network errors)
      // so the route error boundary surfaces the real problem instead of
      // silently hiding contract violations.
      if (isApiError(err) && err.status === 404) {
        return null
      }
      throw err
    }
  },
})
