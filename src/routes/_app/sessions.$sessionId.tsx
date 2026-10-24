import { createFileRoute, useNavigate, Link } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'
import { fetchSessionDetail } from '@/lib/api'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'
import { isApiError } from '@/lib/api-error'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatStore } from '@/store/chat'

// A just-deleted session (confirmed 404, not merely still loading) previously
// fell through to a bare <ChatScreen /> — the loader caught the 404 and
// returned null, the effect below never ran (no detail.session), and nothing
// distinguished "loading" from "gone" — so the user saw what looked like a
// fresh, empty chat instead of any indication the session was deleted.
function SessionNotFound() {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-[var(--space-2-5)] text-center px-[var(--space-3)]">
      <p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">Session not found</p>
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] max-w-sm">
        This session may have been deleted.
      </p>
      <Link
        to="/"
        tabIndex={0}
        className="text-[length:var(--type-utility-xs-size)] text-[var(--color-accent)] underline underline-offset-2"
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
  const isConnected = useConnectionStore((s) => s.isConnected)
  const isReplaying = useChatStore((s) => s.isReplaying)
  const attachedRef = useRef<string | null>(null)
  // An offline mount records the target session so WsLifecycle can reattach
  // it on connect. If that lifecycle callback ran just before the route wrote
  // the session, it has nothing to attach; retain this bit so the route sends
  // the deferred attach when connection state changes instead of mistaking an
  // active id for a completed attach.
  const pendingAttachRef = useRef(false)
  const redirectedRef = useRef(false)

  const loaderData = Route.useLoaderData()

  const { data: detail, isError: detailIsError, error: detailError } = useQuery({
    queryKey: ['session-detail', sessionId],
    initialData: loaderData ?? undefined,
    queryFn: ({ signal }) => fetchSessionDetail(sessionId, { signal }),
    enabled: !!sessionId,
    retry: false,
    staleTime: 10_000,
  })

  useEffect(() => {
    if (detail?.session.id === sessionId) {
      useChatStore.getState().seedSessionTokens(detail.session.total_tokens, sessionId)
    }
  }, [sessionId, detail?.session.id, detail?.session.total_tokens])

  // Fetch active workspaces to verify the session's workspace_id.
  const { data: workspaces = [], isPending: workspacesPending, isPaused: workspacesPaused, isError: workspacesFailed, refetch: refetchWorkspaces } = useQuery({
    // Shared definition: its signal reaches the request whichever component
    // started it, so cancelling the query really aborts a stalled lookup.
    ...activeWorkspacesQueryOptions,
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
    // Detail proves identity even when history is unavailable. Only the
    // page/snapshot owner commits continuation; this metadata read cannot
    // overwrite a newer reader frontier or turn unavailable into empty.
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
      // An already-active session normally needs no second attach. The one
      // exception is this route's own offline record, identified by
      // pendingAttachRef: that record may have missed WsLifecycle's connect
      // callback and must be attached now.
      const wsOpen = isConnected && !!connection?.isConnected
      const lifecycleAlreadyAttached =
        wsOpen &&
        pendingAttachRef.current &&
        useSessionStore.getState().activeSessionId === session.id &&
        isReplaying
      if (lifecycleAlreadyAttached) {
        // WsLifecycle's onConnected callback sends attach_session synchronously
        // after publishing isConnected. React observes that store update only
        // after the callback returns, so its replay marker is the existing
        // acknowledgement that this route's offline record has been attached.
        pendingAttachRef.current = false
        attachedRef.current = session.id
      } else if (
        wsOpen &&
        attachedRef.current !== session.id &&
        (useSessionStore.getState().activeSessionId !== session.id || pendingAttachRef.current)
      ) {
        attachedRef.current = session.id
        pendingAttachRef.current = false
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
        pendingAttachRef.current = true
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
    // values a second time). An active id normally avoids redundant attaches;
    // pendingAttachRef is the explicit exception for this route's earlier
    // offline record. Since useSelectSession no longer attaches for Unfiled
    // sessions, this route owns that deferred attach.
    // Same isConnected gate as the workspace branch above: a CONNECTING
    // socket must take the offline path, not a doomed send that flashes a
    // spurious global error banner on every fresh-tab deep link.
    const wsOpenInline = isConnected && !!connection?.isConnected
    const lifecycleAlreadyAttachedInline =
      wsOpenInline &&
      pendingAttachRef.current &&
      useSessionStore.getState().activeSessionId === session.id &&
      isReplaying
    if (lifecycleAlreadyAttachedInline) {
      pendingAttachRef.current = false
      attachedRef.current = session.id
    } else if (
      wsOpenInline &&
      attachedRef.current !== session.id &&
      (useSessionStore.getState().activeSessionId !== session.id || pendingAttachRef.current)
    ) {
      attachedRef.current = session.id
      pendingAttachRef.current = false
      const attached = attachToSession(session.id, session.type, session.title ?? undefined, headerAgentId)
      if (!attached) {
        // connection.send() itself failed — attachToSession left state
        // untouched. Fall back to setActiveSession so activeSessionId still
        // records this session (see the identical comment in the
        // workspace-known branch above for why this matters on reconnect).
        setActiveSession(session.id, headerAgentId, null)
      }
    } else if (!wsOpenInline) {
      pendingAttachRef.current = true
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
    isConnected,
    isReplaying,
  ])

  // refetch({ cancelRefetch }) does not cancel an unfinished FIRST request
  // that has no data yet (query-core 5.101): cancel this exact query first,
  // which aborts its in-flight request, then start a fresh one.
  const retryWorkspaceLookup = async () => {
    await queryClient.cancelQueries({ queryKey: activeWorkspacesQueryOptions.queryKey, exact: true })
    await refetchWorkspaces()
  }
  const redirectWaitExpired = useBoundedWait(!!detail?.session?.workspace_id && workspacesPending, REDIRECT_DECISION_WAIT_MS)

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
  // While the workspace list that decides the redirect is still loading, an
  // inline ChatScreen would start (and then abort) a redundant history read.
  // Show a visible status instead -- but only for a bounded time: a paused
  // (offline) or stalled list falls back to the inline thread, whose own
  // connection notice and history error/Retry surfaces stay reachable, and
  // the redirect still happens if the list arrives later.
  if (wsId && workspacesPending && !workspacesPaused && !redirectWaitExpired) {
    return <RedirectPendingStatus />
  }

  // The bounded wait ended (or the lookup is paused offline) while the
  // workspace is still unresolved: open inline, but say so, and offer a Retry
  // that replaces the stalled lookup. The later automatic move is announced.
  if (wsId && workspacesPending) {
    return (
      <div className="absolute inset-0 flex flex-col">
        <WorkspaceLookupNotice paused={workspacesPaused} onRetry={() => { void retryWorkspaceLookup() }} />
        {/* ChatScreen's root is absolute inset-0: give it its own positioned
            slot below the notice so the two never overlap. */}
        <div className="relative min-h-0 flex-1" data-testid="workspace-lookup-chat-slot">
          <ChatScreen agentRemoved={detail?.agent_removed ?? false} />
        </div>
      </div>
    )
  }

  // The lookup failed (e.g. 403, or 503s outlasting their retries): the chat
  // is open here, but say why it did not move and offer a real Retry.
  if (wsId && workspacesFailed && !workspaces.length) {
    return (
      <div className="absolute inset-0 flex flex-col">
        <div className="px-[var(--space-3)] py-[var(--space-1)]">
          <ErrorState message="Couldn't find this chat's workspace, so the chat is open here instead. Retry to move it into its workspace." onRetry={() => { void retryWorkspaceLookup() }} />
        </div>
        <div className="relative min-h-0 flex-1" data-testid="workspace-lookup-chat-slot">
          <ChatScreen agentRemoved={detail?.agent_removed ?? false} />
        </div>
      </div>
    )
  }

  return <ChatScreen agentRemoved={detail?.agent_removed ?? false} />
}

/** How long a deep link waits for the workspace list before opening inline. */
export const REDIRECT_DECISION_WAIT_MS = 4_000

/** True once `active` has stayed true for `ms`; resets whenever it turns false. */
function useBoundedWait(active: boolean, ms: number): boolean {
  const [expired, setExpired] = useState(false)
  useEffect(() => {
    setExpired(false)
    if (!active) return
    const timer = setTimeout(() => setExpired(true), ms)
    return () => clearTimeout(timer)
  }, [active, ms])
  return active && expired
}

function WorkspaceLookupNotice({ paused, onRetry }: { paused: boolean; onRetry: () => void }) {
  return (
    <div className="flex items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-1)]">
      <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {paused
          ? "Waiting for the connection to find this chat's workspace. The chat moves into its workspace once it is found."
          : "Still finding this chat's workspace. The chat moves into its workspace once it is found."}
      </p>
      <Button variant="ghost" size="sm" onClick={onRetry}>Retry</Button>
    </div>
  )
}

function RedirectPendingStatus() {
  return (
    <div className="flex flex-1 flex-col gap-[var(--space-2)] p-[var(--space-3)]">
      <Skeleton className="h-4 w-48" />
      <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Opening chat…</p>
    </div>
  )
}

export const Route = createFileRoute('/_app/sessions/$sessionId')({
  component: SessionRoute,
  loader: async ({ params, abortController }) => {
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
      const { fetchSessionDetail } = await import('@/lib/api')
      const detail = await fetchSessionDetail(params.sessionId, { signal: abortController.signal })
      return detail ?? null
    } catch (err) {
      if (isApiError(err) && err.status === 404) {
        return null
      }
      throw err
    }
  },
})
