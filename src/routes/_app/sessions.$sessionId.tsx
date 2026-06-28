import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { fetchSessionDetail, fetchWorkspaces, workspacesQueryKeys, isApiError } from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'

function SessionRoute() {
  const { sessionId } = Route.useParams()
  const navigate = useNavigate()
  const attachToSession = useSessionStore((s) => s.attachToSession)
  const setActiveSession = useSessionStore((s) => s.setActiveSession)
  const setAttachedContext = useSessionStore((s) => s.setAttachedContext)
  const setActiveWorkspaceId = useWorkspacesStore((s) => s.setActiveWorkspaceId)
  const attachedRef = useRef<string | null>(null)
  const redirectedRef = useRef(false)

  const loaderData = Route.useLoaderData()

  const { data: detail } = useQuery({
    queryKey: ['session-detail', sessionId],
    initialData: loaderData ?? undefined,
    queryFn: () => fetchSessionDetail(sessionId),
    enabled: !!sessionId,
    retry: false,
    staleTime: 10_000,
  })

  // Fetch active workspaces to verify the session's workspace_id.
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
    enabled: !!detail?.session?.workspace_id,
  })

  // Bug 2 fix: redirect /sessions/{id} into the session's workspace.
  //
  // When a session has a workspace_id that resolves to an active workspace,
  // bind the workspace, attach the session, and navigate to /workspaces/{wsId}/chat
  // so WorkspaceTabContainer owns session lifecycle going forward.
  //
  // The redirect writes sessionByWorkspace[wsId] = descriptor before navigating,
  // so when WorkspaceTabContainer's enterWorkspaceChat fires on mount it sees
  // descriptor.id === activeSessionId and is a no-op (handoff contract).
  //
  // Fallback: if workspace_id is absent, or the workspace doesn't exist in the
  // active list, render ChatScreen inline (legacy behaviour).
  useEffect(() => {
    if (!detail?.session) return
    if (redirectedRef.current) return

    const { session } = detail
    const { connection } = useConnectionStore.getState()
    const headerAgentId = session.active_agent_id ?? session.agent_id

    const wsId = session.workspace_id
    const targetWorkspace = wsId ? workspaces.find((w) => w.id === wsId) : null

    if (wsId && targetWorkspace) {
      redirectedRef.current = true
      // Bind workspace first so sessionByWorkspace writes land under the right key.
      setActiveWorkspaceId(wsId)

      if (connection && attachedRef.current !== session.id) {
        attachedRef.current = session.id
        attachToSession(session.id, session.type, session.title ?? undefined, headerAgentId)
      } else if (!connection) {
        setActiveSession(session.id, headerAgentId, null)
      }

      if (session.type === 'task') {
        setAttachedContext('task', session.title)
      }

      void navigate({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: wsId },
        replace: true,
      })
      return
    }

    // Fallback inline path — no workspace, or workspace not yet loaded.
    if (connection && attachedRef.current !== session.id) {
      attachedRef.current = session.id
      attachToSession(session.id, 'chat', undefined, headerAgentId)
    } else if (!connection) {
      setActiveSession(session.id, headerAgentId, null)
    }

    if (session.type === 'task') {
      setAttachedContext('task', session.title)
    }
  }, [
    detail?.session?.id,
    detail?.session?.agent_id,
    detail?.session?.active_agent_id,
    detail?.session?.type,
    detail?.session?.title,
    detail?.session?.workspace_id,
    workspaces,
    attachToSession,
    setActiveSession,
    setAttachedContext,
    setActiveWorkspaceId,
    navigate,
  ])

  // Suppress inline render while workspace redirect is in flight.
  const wsId = detail?.session?.workspace_id
  if (wsId && workspaces.find((w) => w.id === wsId)) {
    return null
  }

  return <ChatScreen agentRemoved={detail?.agent_removed ?? false} />
}

export const Route = createFileRoute('/_app/sessions/$sessionId')({
  component: SessionRoute,
  loader: async ({ params }) => {
    // Pre-fetch session detail and activate the session in the Zustand store
    // BEFORE the component renders.
    try {
      const detail = await fetchSessionDetail(params.sessionId)
      if (detail?.session) {
        const store = useSessionStore.getState()
        const headerAgentId = detail.session.active_agent_id ?? detail.session.agent_id
        store.setActiveSession(detail.session.id, headerAgentId, null)
        if (detail.session.type === 'task') {
          store.setAttachedContext('task', detail.session.title)
        }
      }
      return detail ?? null
    } catch (err) {
      if (isApiError(err) && err.status === 404) {
        return null
      }
      throw err
    }
  },
})
