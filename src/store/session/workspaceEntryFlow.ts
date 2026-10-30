/**
 * enterWorkspaceChat body (FR-004).
 *
 * Validates the remembered pointer, then resolveWorkspaceEntry. Exact visible
 * pointer, else the first conversation agent's validated main in team order
 * (omnipus-system-agent FR-107), else the empty "Set up a team" state; a
 * first agent without a resolved main is unavailable / Retry with send off.
 * A transport failure keeps the committed chat and does not fall back. Entry attaches without acknowledgement. A newer navigation intent
 * wins; a late resolution must not split the tuple.
 */
import { fetchAgents, fetchSessionDetail, fetchSessions, fetchWorkspace, readSessionFetchCoverage } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import type { Session } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { resolveWorkspaceEntry, workspaceEntryBlocksSend, type EmptyTeamEntry, type EntryResult, type EntryTeam, type FailedAttempt, type PointerVerdict, type WelcomeEntry } from '@/lib/nav/workspaceEntry'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { clearPendingAutoApproveOnSessionChange, knownSessionDescriptor, readPersistedSessionByWorkspace, useSessionStore } from '@/store/session'

export type WorkspaceEntryView = (
  | { status: 'exact'; acknowledged: false }
  | WelcomeEntry
  | EmptyTeamEntry
  | Extract<EntryResult, { status: 'unavailable' }>
  | FailedAttempt
) & { linkedSessionId?: string } // not-wire-format: preserves an explicit browser-link target for Retry

let navigationIntent = 0
const inFlight = new Map<string, { linkedSessionId: string | undefined; job: Promise<void> }>()
const resolvingIntent = new Map<string, number>()
const visibilityWaiters = new Set<() => void>()
const socketWaiters = new Set<() => void>()
let resumeQueuedSends: (() => void) | null = null

/**
 * Longest an entry waits for a socket that is still opening before it attaches
 * anyway and, if that still cannot send, reports the ordinary failed attempt with
 * Retry (see waitForSocketOpen). A handshake takes a round trip or two; this is a
 * safety bound against a hang, not an expected delay.
 */
const SOCKET_OPEN_WAIT_MS = 15_000

/** The chat store registers without introducing a session/chat import cycle. */
export function registerWorkspaceEntryQueueResume(resume: () => void): void {
  resumeQueuedSends = resume
}

/** Called only after the destination tuple and its entry gates are committed. */
export function resumeWorkspaceEntryQueue(): void {
  const workspaceId = useWorkspacesStore.getState().activeWorkspaceId
  if (workspaceEntryBlocksSend(useSessionStore.getState(), workspaceId)) return
  resumeQueuedSends?.()
}

export function supersedeNavigationIntent(): number {
  navigationIntent += 1
  // Release superseded hidden entries so their listeners/in-flight jobs end.
  for (const finish of visibilityWaiters) finish()
  // Likewise an entry waiting for its socket: a lingering job would also make a
  // return to the same workspace re-use it (runWorkspaceEntry's in-flight map).
  for (const finish of socketWaiters) finish()
  return navigationIntent
}

/** Hidden prefetch is deferred, not an attach failure (FR-013). */
function waitForEntryVisibility(token: number): Promise<void> {
  if (typeof document === 'undefined' || !document.hidden || !navigationIntentCurrent(token)) {
    return Promise.resolve()
  }
  return new Promise((resolve) => {
    const finish = () => {
      document.removeEventListener('visibilitychange', onVisibilityChange)
      visibilityWaiters.delete(finish)
      resolve()
    }
    const onVisibilityChange = () => { if (!document.hidden) finish() }
    visibilityWaiters.add(finish)
    document.addEventListener('visibilitychange', onVisibilityChange)
  })
}

export function navigationIntentCurrent(token: number): boolean {
  return token === navigationIntent
}

/**
 * True while the chat socket exists and its handshake has not finished. Strictly
 * `true`: no connection at all (attachToSession records the chat offline), a socket
 * that is down or waiting out a reconnect backoff, and a connection object that does
 * not report readiness all keep the immediate attach and its ordinary failure path.
 */
function socketIsOpening(): boolean {
  return useConnectionStore.getState().connection?.isConnecting === true
}

/**
 * A freshly loaded page publishes its WsConnection BEFORE the socket has opened
 * (OmnipusRuntimeProvider.tsx::WsLifecycle), and the entry's REST loads can finish
 * first. WsConnection.send() returns false on a socket that is not OPEN, so an
 * attach made now reports "connected but cannot send": the entry would latch
 * "Could not restore your last conversation" and nothing would retry it when the
 * socket opens a moment later (#1449). An entry that is about to attach therefore
 * waits for its socket, the way sessions.$sessionId.tsx gates its attach on `wsOpen`.
 *
 * The caller resumes after WsLifecycle.onConnected has run to the end (this promise
 * settles from the connection store update inside it, and the continuation is a
 * microtask), so its connect-time reattachActiveSession - which only re-binds an
 * already active session - has found nothing to re-bind and the attach that follows
 * sets the chat's type and agent itself.
 *
 * Bounded by SOCKET_OPEN_WAIT_MS; a newer navigation intent releases the wait early.
 * Either way the caller attaches next and any failure is reported as before.
 */
function waitForSocketOpen(token: number, workspaceId: string): Promise<void> {
  if (!socketIsOpening() || !navigationIntentCurrent(token)) return Promise.resolve()
  return new Promise((resolve) => {
    const finish = () => {
      clearTimeout(timer)
      unsubscribe()
      socketWaiters.delete(finish)
      resolve()
    }
    const unsubscribe = useConnectionStore.subscribe(() => {
      if (!socketIsOpening()) finish()
    })
    const timer = setTimeout(() => {
      logDiagnostic('sessionWorkspaceEntrySocketWaitTimedOut', { workspaceId })
      finish()
    }, SOCKET_OPEN_WAIT_MS)
    socketWaiters.add(finish)
  })
}

function rememberedId(workspaceId: string): string | null | undefined {
  const memory = useSessionStore.getState().sessionByWorkspace
  if (Object.prototype.hasOwnProperty.call(memory, workspaceId)) {
    const descriptor = memory[workspaceId]
    return descriptor == null ? null : descriptor.id
  }
  const persisted = readPersistedSessionByWorkspace()
  if (!Object.prototype.hasOwnProperty.call(persisted, workspaceId)) return undefined
  const descriptor = persisted[workspaceId]
  return descriptor == null ? null : descriptor.id
}

function classifyPointer(
  sessions: Session[],
  workspaceId: string,
  remembered: string | null | undefined,
  transportFailed: boolean,
  listIncomplete: boolean,
): PointerVerdict {
  if (transportFailed) return 'timeout'
  if (typeof remembered !== 'string' || remembered.trim() === '' || remembered === '__pending') return 'absent'
  const match = sessions.find((session) => session.id === remembered)
  // Absent from an INCOMPLETE list is unknown, not deleted (M3): the list
  // itself says pages failed to load, so the id may simply not have been
  // enumerated. Same coverage flag SearchModal/sessionOverview read.
  if (!match) return listIncomplete ? 'partial-list' : 'deleted'
  if (match.workspace_id !== workspaceId) return 'hidden'
  return 'visible'
}

/**
 * M3: the remembered id is absent from an INCOMPLETE list — unknown, not
 * deleted. (Also used for an explicit link absent from the roots-only list,
 * which may name a delegated child — see loadInputs.) Resolve it directly
 * (GET /sessions/{id}) before anything may overwrite the pointer: found
 * here → 'visible' (the fetched record is pushed onto the list so the exact
 * path has its owner); a 404 is the ONE truthful deletion (the welcome
 * path may then take over); any other
 * failure is a transport failure — the pointer is kept with Retry, never a
 * silent fallback to the main.
 */
async function resolvePartialListPointer(
  remembered: string,
  workspaceId: string,
  sessions: Session[],
): Promise<PointerVerdict> {
  try {
    const detail = await fetchSessionDetail(remembered)
    const fetched = detail.session
    if (fetched?.workspace_id !== workspaceId) return 'hidden'
    sessions.push(fetched)
    return 'visible'
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return 'deleted'
    const fields = {
      workspaceId,
      sessionId: remembered,
      errorName: error instanceof Error ? error.name : 'UnknownError',
    }
    console.warn('[session] workspace entry pointer check failed', fields)
    logDiagnostic('sessionWorkspaceEntryPointerCheckFailed', fields)
    return 'timeout'
  }
}

async function loadInputs(
  workspaceId: string,
  remembered: string | null | undefined,
  explicitLink = false,
): Promise<{
  sessions: Session[]
  transportFailed: boolean
  team: EntryTeam
  pointerVerdict: PointerVerdict
}> {
  const [sessionsResult, workspaceResult, agentsResult] = await Promise.allSettled([
    fetchSessions(),
    fetchWorkspace(workspaceId),
    fetchAgents(),
  ])
  const sessions = sessionsResult.status === 'fulfilled' && Array.isArray(sessionsResult.value)
    ? sessionsResult.value
    : []
  const sessionsFailed = sessionsResult.status !== 'fulfilled' || !Array.isArray(sessionsResult.value)
  let pointerVerdict = classifyPointer(
    sessions,
    workspaceId,
    remembered,
    sessionsFailed,
    readSessionFetchCoverage(sessions).incomplete,
  )
  // The default GET /sessions listing pages over ROOT sessions only
  // (ADR-057 FR-091): a delegated child chat is never in it. Absence there is
  // therefore not proof of deletion for an explicit link, which may name a
  // child (train 3 hands-on row F4). Resolve it directly: 404 stays 'deleted',
  // another workspace stays 'hidden', so a missing link is still unavailable.
  const unlistedLink = explicitLink && pointerVerdict === 'deleted'
  if ((pointerVerdict === 'partial-list' || unlistedLink) && typeof remembered === 'string') {
    pointerVerdict = await resolvePartialListPointer(remembered, workspaceId, sessions)
  }
  // Exact restore needs only the visible session and its immutable owner.
  // The team (workspace + agents) is required only to resolve the
  // welcome / empty-team / unavailable path; a team that could not load is a
  // retryable failure, never a fallback (dataset D4).
  const exactAvailable = pointerVerdict === 'visible'
  const teamFailed = workspaceResult.status === 'rejected' || agentsResult.status === 'rejected'
  const transportFailed = sessionsFailed || (teamFailed && !exactAvailable)
  // A transport failure outranks every pointer classification — the pointer
  // could not actually be checked (previously done by re-classifying with the
  // flag at the call site; now applied once, here).
  if (transportFailed) pointerVerdict = 'timeout'
  for (const [resource, result] of [['sessions', sessionsResult], ['workspace', workspaceResult], ['agents', agentsResult]] as const) {
    if (result.status !== 'rejected') continue
    const fields = {
      workspaceId, resource,
      errorName: result.reason instanceof Error ? result.reason.name : 'UnknownError',
    }
    console.warn('[session] workspace entry input failed', fields)
    logDiagnostic('sessionWorkspaceEntryLoadFailed', fields)
  }
  const workspace = workspaceResult.status === 'fulfilled' ? workspaceResult.value : null
  const team: EntryTeam = {
    coreTeam: workspace?.core_team ?? [],
    memberConfigs: workspace?.member_configs ?? {},
    agents: agentsResult.status === 'fulfilled' ? agentsResult.value : [],
  }
  return { sessions, transportFailed, team, pointerVerdict }
}

function committedNow(): { sessionId: string; agentId: string } | null {
  const { activeSessionId, activeAgentId } = useSessionStore.getState()
  if (!activeSessionId || activeSessionId === '__pending') return null
  return { sessionId: activeSessionId, agentId: activeAgentId ?? '' }
}

function clearResolving(workspaceId: string): void {
  useSessionStore.setState((state) => {
    const next = { ...state.resolvingSessionForWorkspace }
    delete next[workspaceId]
    return { resolvingSessionForWorkspace: next }
  })
}

function applyEntry(
  workspaceId: string,
  result: EntryResult,
  sessions: Session[],
  token: number,
  linkedSessionId?: string,
): void {
  if (!navigationIntentCurrent(token)) return
  const stillViewing = useWorkspacesStore.getState().activeWorkspaceId === workspaceId
  const linkedTarget = linkedSessionId ? { linkedSessionId } : {}

  if (result.status === 'failed-attempt') {
    if (stillViewing) {
      useSessionStore.setState({ workspaceEntry: { ...result, ...linkedTarget } })
      useUiStore.getState().addToast({
        message: 'Could not restore your last conversation. Retry to try again.',
        variant: 'warning',
        action: {
          label: 'Retry',
          onClick: () => {
            void (linkedSessionId ? runWorkspaceEntry(workspaceId, linkedSessionId) : runWorkspaceEntry(workspaceId))
          },
        },
      })
    }
    return
  }

  if (result.status === 'unavailable' || result.status === 'empty-team') {
    if (!stillViewing) return
    useSessionStore.setState({ workspaceEntry: { ...result, ...linkedTarget } })
    if (useSessionStore.getState().activeSessionId !== null) {
      useSessionStore.getState().setActiveSession(null)
    }
    return
  }

  const match = sessions.find((session) => session.id === result.sessionId)
  // The list may not carry this session: GET /sessions pages over ROOT
  // sessions, so a team's main can sit on another page. The store may still
  // KNOW what this session is, and a fallback must never overwrite a known,
  // more specific descriptor for the SAME session with the generic 'chat' —
  // a downgraded MAIN reads "Extra chat" in the header line and its honest
  // "agent names unavailable" warning can never render (#1449 round 6).
  const known = knownSessionDescriptor(workspaceId, result.sessionId)
  const descriptor = {
    id: result.sessionId,
    type: match?.type ?? known?.type ?? 'chat' as const,
    title: match?.title ?? known?.title ?? null,
    agentId: result.agentId,
  }
  if (!stillViewing) {
    useSessionStore.getState().setWorkspaceSessionDescriptor(workspaceId, descriptor)
    return
  }
  const attached = useSessionStore.getState().attachToSession(
    descriptor.id,
    descriptor.type,
    descriptor.title ?? undefined,
    descriptor.agentId,
  )
  if (!navigationIntentCurrent(token)) return
  if (!attached) {
    const failed: FailedAttempt = {
      status: 'failed-attempt',
      committed: committedNow(),
      attempted: { sessionId: result.sessionId, sendEnabled: false },
      acknowledged: false,
      retry: true,
      fellBack: false,
    }
    useSessionStore.setState({ workspaceEntry: { ...failed, ...linkedTarget } })
    return
  }
  const view: WorkspaceEntryView = result.status === 'exact'
    ? { status: 'exact', acknowledged: false }
    : result
  useSessionStore.setState({ workspaceEntry: view })
}

/** A session id carried by the address (a /sessions/{id} deep link's
 *  resulting chat address, #1437). While present it IS this entry's chat
 *  pointer: the linked chat wins over whatever chat another tab last
 *  remembered for the workspace. */
function linkedPointer(linkedSessionId: string | null | undefined): string | null | undefined {
  if (typeof linkedSessionId !== 'string' || linkedSessionId.trim() === '' || linkedSessionId === '__pending') {
    return undefined
  }
  return linkedSessionId
}

async function execute(workspaceId: string, linkedSessionId?: string): Promise<void> {
  clearPendingAutoApproveOnSessionChange()
  const token = supersedeNavigationIntent()
  const state = useSessionStore.getState()
  const memoryDescriptor = state.sessionByWorkspace[workspaceId]
  // An explicit address wins BEFORE the already-attached shortcut. An older
  // committed chat is not proof that a different linked target was adopted.
  const linked = linkedPointer(linkedSessionId)
  const remembered = linked ?? rememberedId(workspaceId)
  if (
    memoryDescriptor
    && memoryDescriptor.id !== '__pending'
    && memoryDescriptor.id === state.activeSessionId
    && remembered === state.activeSessionId
    && !workspaceEntryBlocksSend(state, workspaceId)
  ) {
    return
  }
  const descriptorAtStart = memoryDescriptor
  const committed = committedNow()
  resolvingIntent.set(workspaceId, token)
  useSessionStore.setState((current) => ({
    resolvingSessionForWorkspace: { ...current.resolvingSessionForWorkspace, [workspaceId]: true },
  }))
  try {
    while (typeof document !== 'undefined' && document.hidden && navigationIntentCurrent(token)) {
      await waitForEntryVisibility(token)
    }
    if (!navigationIntentCurrent(token)) return
    const loaded = await loadInputs(workspaceId, remembered, typeof linked === 'string')
    // A missing explicit link is an error, not permission to open the team's
    // main chat. Ordinary remembered-pointer entry keeps its existing fallback.
    const linkedUnavailable = linked && ['deleted', 'hidden', 'forbidden'].includes(loaded.pointerVerdict)
    const result: EntryResult = linkedUnavailable
      ? { status: 'unavailable', sessionId: null, sendEnabled: false, acknowledged: false, retry: true }
      : resolveWorkspaceEntry({
        workspaceId,
        entryKind: 'cold-reload',
        rememberedSessionId: remembered,
        pointerVerdict: loaded.pointerVerdict,
        sessions: loaded.sessions,
        team: loaded.team,
        committed,
      })
    // Only an entry that attaches a chat (applyEntry) needs the socket; a failure,
    // unavailable or empty-team entry must still report at once.
    if (result.status === 'exact' || result.status === 'welcome') await waitForSocketOpen(token, workspaceId)
    // The tab may have become hidden while the network requests were in flight or
    // the socket was opening.
    while (typeof document !== 'undefined' && document.hidden && navigationIntentCurrent(token)) {
      await waitForEntryVisibility(token)
    }
    if (!navigationIntentCurrent(token)) return
    if (useSessionStore.getState().sessionByWorkspace[workspaceId] !== descriptorAtStart) return
    applyEntry(workspaceId, result, loaded.sessions, token, linked ?? undefined)
  } catch (error) {
    // Not a network failure (loadInputs absorbs those): something threw. Never
    // leave the entry silently unresolved — show the same unavailable state with Retry.
    logDiagnostic('sessionWorkspaceEntryThrew', {
      workspaceId,
      errorName: error instanceof Error ? error.name : 'UnknownError',
    })
    if (navigationIntentCurrent(token) && useWorkspacesStore.getState().activeWorkspaceId === workspaceId) {
      useSessionStore.setState({
        workspaceEntry: { status: 'unavailable', sessionId: null, sendEnabled: false, acknowledged: false, retry: true, ...(linked ? { linkedSessionId: linked } : {}) },
      })
    }
  } finally {
    // A superseded request for the same workspace must not clear its newer
    // linked target's loading/send gate.
    if (resolvingIntent.get(workspaceId) === token) {
      resolvingIntent.delete(workspaceId)
      clearResolving(workspaceId)
    }
    if (navigationIntentCurrent(token) && useWorkspacesStore.getState().activeWorkspaceId === workspaceId) {
      resumeWorkspaceEntryQueue()
    }
  }
}

export function runWorkspaceEntry(workspaceId: string, linkedSessionId?: string): Promise<void> {
  const linked = linkedPointer(linkedSessionId) ?? undefined
  const existing = inFlight.get(workspaceId)
  if (existing && existing.linkedSessionId === linked) return existing.job
  const job = execute(workspaceId, linked)
  inFlight.set(workspaceId, { linkedSessionId: linked, job })
  return job.finally(() => {
    if (inFlight.get(workspaceId)?.job === job) inFlight.delete(workspaceId)
  })
}
