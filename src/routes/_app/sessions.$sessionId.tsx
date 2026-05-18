import { createFileRoute } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { fetchSessionDetail, isApiError } from '@/lib/api'
import { useSessionStore } from '@/store/session'

function SessionRoute() {
  const { sessionId } = Route.useParams()
  const setActiveSession = useSessionStore((s) => s.setActiveSession)

  // Loader pre-fetches session detail and activates the session synchronously
  // in the Zustand store before this component renders. The useQuery below is
  // kept for live data updates (e.g. agent_removed flag changing) but the
  // activeSessionId is guaranteed to be set by loader before first render.
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

  // Keep the store in sync when detail refreshes (e.g. agent_removed toggles).
  useEffect(() => {
    if (detail?.session) {
      setActiveSession(detail.session.id, detail.session.agent_id, null)
    }
  }, [detail?.session?.id, detail?.session?.agent_id, setActiveSession])

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
