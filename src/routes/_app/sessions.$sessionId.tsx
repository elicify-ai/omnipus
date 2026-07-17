import { createFileRoute, useNavigate, Link } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { fetchSessionDetail, fetchWorkspaces, workspacesQueryKeys, isApiError } from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'

// A just-deleted session (confirmed 404, not merely still loading) previously
// fell through to a bare <ChatScreen /> — the loader caught the 404 and
// returned null, the effect below never ran (no detail.session), and nothing
// distinguished "loading" from "gone" — so the user saw what looked like a
// fresh, empty chat instead of any indication the session was deleted.
function SessionNotFound() {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-center px-4">
      <p className="text-sm font-medium text-[var(--color-secondary)]">Session not found</p>
      <p className="text-xs text-[var(--color-muted)] max-w-sm">
        This session may have been deleted.
      </p>
      <Link
        to="/"
        tabIndex={0}
        className="text-xs text-[var(--color-accent)] underline underline-offset-2"
      >
        Back to Chat
      </Link>
    </div>
  )
}

function SessionRoute() {
  const { sessionId } = Route.useParams()
  const navigate = useNavigate()
  const attachToSession = useSessionStore((s) => s.attachToSession)
  const setActiveSession = useSessionStore((s) => s.setActiveSession)
  const setAttachedContext = useSessionStore((s) => s.setAttachedContext)
  const setWorkspaceSessionDescriptor = useSessionStore((s) => s.setWorkspaceSessionDescriptor)
  const setActiveWorkspaceId = useWorkspacesStore((s) => s.setActiveWorkspaceId)
  const attachedRef = useRef<string | null>(null)
  const redirectedRef = useRef(false)

  const loaderData = Route.useLoaderData()

  const { data: detail, isError: detailIsError, error: detailError } = useQuery({
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

  // Deep-link adoption: redirect /sessions/{id} into the session's workspace.
  //
  // When a session has a workspace_id that resolves to an active workspace,
  // bind the workspace, attach the session, and navigate to /workspaces/{wsId}/chat
  // so WorkspaceTabContainer owns session lifecycle going forward.
  //
  // Race-free handoff contract (three-step guarantee):
  //   1. setActiveWorkspaceId(wsId) — bind workspace BEFORE writing the descriptor
  //      so the activeWorkspaceId key used by attachToSession matches wsId.
  //   2. setWorkspaceSessionDescriptor(wsId, descriptor) — write the descriptor
  //      unconditionally by explicit key, bypassing the activeWorkspaceId lookup
  //      inside attachToSession.  This write MUST happen before navigate() so that
  //      WorkspaceTabContainer.enterWorkspaceChat sees descriptor.id === activeSessionId
  //      on mount and takes the no-op path (never calls startNewSession).
  //   3. attach_session frame — sent via attachToSession (WS connected) or deferred
  //      to WsLifecycle.onConnected via reattachActiveSession (WS offline path).
  //      The frame ensures the backend's per-connection sessionID map is updated.
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

      // Step 1: bind workspace so subsequent store writes use the correct key.
      setActiveWorkspaceId(wsId)

      // Step 2: send attach_session frame (or defer to onConnected path).
      // Gate on connection.isConnected, not mere connection existence: on a
      // fresh page load the connection object is published synchronously
      // BEFORE the socket opens (WsLifecycle), so send() would fail and
      // attachToSession's failure branch would flash a spurious global
      // "connection dropped" banner for a socket that is merely still
      // CONNECTING. A non-open socket takes the offline branch instead —
      // reattachActiveSession sends the frame on connect.
      // The activeSessionId check guards against re-attaching an already-active
      // session id — mirrors enterWorkspaceChat's no-op guard (session.ts) —
      // so re-mounting this route for a session already attached this tab
      // doesn't fire a redundant attach_session + bucket wipe.
      const wsOpen = !!connection?.isConnected
      if (
        wsOpen &&
        attachedRef.current !== session.id &&
        useSessionStore.getState().activeSessionId !== session.id
      ) {
        attachedRef.current = session.id
        const attached = attachToSession(session.id, session.type, session.title ?? undefined, headerAgentId)
        if (!attached) {
          // connection.send() itself failed (e.g. mid reconnect-backoff) —
          // attachToSession left state untouched. Fall back to setActiveSession
          // so activeSessionId still records this session; otherwise
          // WsLifecycle.onConnected -> reattachActiveSession would reattach
          // whatever session was PREVIOUSLY active instead of this one.
          setActiveSession(session.id, headerAgentId, null)
        }
      } else if (!wsOpen) {
        // Offline / still-connecting: setActiveSession records the session so
        // reattachActiveSession can send the frame once the WS connects
        // (WsLifecycle.onConnected path).
        setActiveSession(session.id, headerAgentId, null)
      }

      // Step 3: write the descriptor by explicit key BEFORE navigate().
      // This guarantees WorkspaceTabContainer.enterWorkspaceChat sees the
      // descriptor and takes the no-op path regardless of WS state. Written
      // AFTER the attach/fallback block deliberately: the fallback/offline
      // setActiveSession above records a generic descriptor (type 'chat',
      // no title) under this workspace key — this explicit write restores
      // the session's REAL type/title so a later workspace re-entry
      // re-attaches with correct labeling (task sessions especially).
      const descriptor = {
        id: session.id,
        type: session.type,
        title: session.title ?? null,
        agentId: headerAgentId ?? null,
      }
      setWorkspaceSessionDescriptor(wsId, descriptor)

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

    // Fallback inline path — no workspace, or workspace not yet loaded. Attach
    // with the session's REAL type/title (not a hardcoded 'chat'/undefined —
    // that clobbered channel/scheduled/heartbeat sessions' labelling and, when
    // useSelectSession also attached before navigating here, wiped the real
    // values a second time). The activeSessionId check guards against
    // re-attaching an already-active session id — mirrors enterWorkspaceChat's
    // no-op guard (session.ts) — since useSelectSession no longer attaches for
    // Unfiled sessions, this route is now the SOLE attacher for them.
    // Same isConnected gate as the workspace branch above: a CONNECTING
    // socket must take the offline path, not a doomed send that flashes a
    // spurious global error banner on every fresh-tab deep link.
    const wsOpenInline = !!connection?.isConnected
    if (
      wsOpenInline &&
      attachedRef.current !== session.id &&
      useSessionStore.getState().activeSessionId !== session.id
    ) {
      attachedRef.current = session.id
      const attached = attachToSession(session.id, session.type, session.title ?? undefined, headerAgentId)
      if (!attached) {
        // connection.send() itself failed — attachToSession left state
        // untouched. Fall back to setActiveSession so activeSessionId still
        // records this session (see the identical comment in the
        // workspace-known branch above for why this matters on reconnect).
        setActiveSession(session.id, headerAgentId, null)
      }
    } else if (!wsOpenInline) {
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
    setWorkspaceSessionDescriptor,
    setActiveWorkspaceId,
    navigate,
  ])

  // Confirmed 404 (loader caught it and returned null, then the client-side
  // refetch above hit the same 404) — surface it instead of falling through
  // to a bare ChatScreen that looks like a fresh, empty chat.
  if (!detail?.session && detailIsError && isApiError(detailError) && detailError.status === 404) {
    return <SessionNotFound />
  }

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
    // Pre-fetch session detail ONLY — as `initialData` for the component's
    // useQuery — so the query doesn't waterfall behind mount.
    //
    // MUST NOT write to the session store here (no setActiveSession /
    // setAttachedContext). The component's attach effect below is the SOLE
    // attacher and it gates on `activeSessionId !== session.id` to avoid a
    // redundant re-attach when the route remounts for an already-attached
    // session. If the loader pre-set activeSessionId to this session, that
    // guard would see the session as "already attached" on the very first
    // mount and skip attachToSession entirely — no attach_session frame is
    // ever sent, so the backend never runs replay and the thread silently
    // falls back to the plain REST fetch below with no WS-driven replay,
    // subagent span reconstruction, or composer/replay lifecycle. This was a
    // real, confirmed bug for every session deep link (/#/sessions/{id}) —
    // do not reintroduce the store write here.
    try {
      const detail = await fetchSessionDetail(params.sessionId)
      return detail ?? null
    } catch (err) {
      if (isApiError(err) && err.status === 404) {
        return null
      }
      throw err
    }
  },
})
